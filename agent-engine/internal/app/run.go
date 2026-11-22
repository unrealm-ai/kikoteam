// Package app assembles and runs the Agent Engine process.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/runtime"
)

type Config struct {
	DSN, BackendURL, Token, Address, HostID, Root, RepoRoot string
	Capacity                                                int
	Idle                                                    time.Duration
}

func Load() (Config, error) {
	c := Config{DSN: os.Getenv("DATABASE_URL"), BackendURL: strings.TrimRight(os.Getenv("KIKO_BACKEND_URL"), "/"), Token: os.Getenv("KIKO_ENGINE_TOKEN"), Address: os.Getenv("KIKO_ENGINE_ADDRESS"), HostID: os.Getenv("KIKO_ENGINE_HOST_ID"), Root: os.Getenv("KIKO_STORAGE_LOCAL_ROOT"), RepoRoot: os.Getenv("KIKO_REPO_ROOT"), Capacity: 8, Idle: time.Minute}
	if c.DSN == "" || c.BackendURL == "" || len(c.Token) < 32 {
		return c, errors.New("DATABASE_URL, KIKO_BACKEND_URL and KIKO_ENGINE_TOKEN (32+ bytes) required")
	}
	if c.Address == "" {
		c.Address = "127.0.0.1:0"
	}
	if c.HostID == "" {
		v, err := os.Hostname()
		if err != nil {
			return c, err
		}
		c.HostID = v
	}
	if c.Root == "" {
		c.Root = "~/.kiko"
	}
	if c.Root == "~" || strings.HasPrefix(c.Root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return c, err
		}
		c.Root = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(c.Root, "~"), "/"))
	}
	var err error
	c.Root, err = filepath.Abs(c.Root)
	if err != nil {
		return c, err
	}
	if c.RepoRoot == "" {
		c.RepoRoot = "."
	}
	c.RepoRoot, err = filepath.Abs(c.RepoRoot)
	if err != nil {
		return c, err
	}
	if raw := os.Getenv("KIKO_ENGINE_CAPACITY"); raw != "" {
		c.Capacity, err = strconv.Atoi(raw)
		if err != nil || c.Capacity < 1 {
			return c, errors.New("invalid Engine capacity")
		}
	}
	if raw := os.Getenv("KIKO_ENGINE_IDLE_TIMEOUT"); raw != "" {
		c.Idle, err = time.ParseDuration(raw)
		if err != nil || c.Idle < time.Second {
			return c, errors.New("invalid Engine idle timeout")
		}
	}
	return c, nil
}

func Run(ctx context.Context, c Config, log *slog.Logger) error {
	ctx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()
	for _, name := range []string{"agent-engine/node_modules/.bin/pi", "agent-engine/scripts/pi.sh"} {
		if _, err := os.Stat(filepath.Join(c.RepoRoot, name)); err != nil {
			return fmt.Errorf("runtime dependency missing: %s", name)
		}
	}
	db, err := sql.Open("pgx", c.DSN)
	if err != nil {
		return err
	}
	defer func() {
		if e := db.Close(); e != nil {
			log.Warn("Engine database close failed", "event", "shutdown.database_failed")
		}
	}()
	db.SetMaxOpenConns(20)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", c.Address)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	var ready atomic.Bool
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		if e := server.Shutdown(cleanup); e != nil {
			log.Warn("Engine HTTP close failed", "event", "shutdown.http_failed")
		}
	}()
	id := runtime.NewID()
	lifetime, err := lockLifetime(c.Root, id)
	if err != nil {
		return fmt.Errorf("lock Engine lifetime: %w", err)
	}
	defer func() { _ = lifetime.Close() }() //nolint:errcheck // The kernel releases this descriptor on exit.
	backend := &integration.Backend{URL: c.BackendURL, Token: c.Token, EngineID: id, HTTP: &http.Client{Timeout: 15 * time.Second}}
	sandboxes := &runtime.Sandboxes{Backend: backend, Lifetime: lifetime, Root: c.Root, Binary: filepath.Join(c.RepoRoot, "sandbox-runner/bin/sandbox-runner"), Builtin: filepath.Join(c.RepoRoot, "sandbox-runner/builtin-mcp/dist/http-main.js"), Log: log}
	worker := &runtime.Worker{Store: &repository.Store{DB: db, EngineID: id}, Backend: backend, Sandboxes: sandboxes, Root: c.Root, Pi: filepath.Join(c.RepoRoot, "agent-engine/node_modules/.bin/pi"), Launcher: filepath.Join(c.RepoRoot, "agent-engine/scripts/pi.sh"), Idle: c.Idle, Log: log}
	endpoint := "http://" + listener.Addr().String()
	registered := false
	seq := int64(0)
	running := map[string]<-chan struct{}{}
	var wg sync.WaitGroup
	defer func() {
		cancelWorkers()
		wg.Wait()
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer stop()
		if err := sandboxes.Close(cleanup); err != nil {
			log.Error("Sandbox cleanup unresolved", "event", "sandbox.cleanup_failed")
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			ready.Store(false)
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			seq++
			err = backend.Call(cleanup, "POST", "/engines/"+id+"/heartbeat", map[string]any{"heartbeat_seq": strconv.FormatInt(seq, 10), "accepting": false}, nil)
			stop()
			if err != nil {
				log.Warn("Engine drain notification pending", "event", "engine.drain_pending")
			}
			return nil
		}
		if !registered {
			err = backend.Call(ctx, "POST", "/engines", map[string]any{"engine_id": id, "host_id": c.HostID, "endpoint": endpoint, "capacity": c.Capacity, "accepting": true, "heartbeat_seq": "0"}, nil)
			if err == nil {
				registered = true
				log.Info("Engine registered", "event", "engine.registered", "engine_id", id)
			}
		}
		if registered {
			for key, done := range running {
				select {
				case <-done:
					delete(running, key)
				default:
				}
			}
			seq++
			err = backend.Call(ctx, "POST", "/engines/"+id+"/heartbeat", map[string]any{"heartbeat_seq": strconv.FormatInt(seq, 10), "accepting": true}, nil)
			ready.Store(err == nil)
			if err == nil {
				var assignments []repository.Assignment
				assignments, err = backend.Assignments(ctx)
				if err == nil {
					for _, a := range assignments {
						key := a.AssignmentID
						if done, ok := running[key]; ok {
							select {
							case <-done:
								delete(running, key)
							default:
								continue
							}
						}
						done := make(chan struct{})
						running[key] = done
						wg.Add(1)
						go func() { defer wg.Done(); defer close(done); worker.Run(ctx, a) }()
					}
				}
			}
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("Backend execution control temporarily unavailable", "event", "engine.backend_unavailable")
		}
		if registered && ctx.Err() == nil {
			if e := sandboxes.Reap(ctx); e != nil {
				log.Warn("Sandbox reclamation pending", "event", "sandbox.reclaim_pending")
			}
		}
		select {
		case <-ctx.Done():
		case err = <-serveErr:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		case <-tick.C:
		}
	}
}
