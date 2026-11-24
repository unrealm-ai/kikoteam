package runner

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type server struct {
	config              Config
	scope               scope
	ctx                 context.Context
	boot, project, temp string
	backend             *http.Client
	log                 *slog.Logger
	mu                  sync.Mutex
	bindings            map[string]*binding
	closing             bool
	stopped             chan struct{}
	lifetime            *os.File
}

func Run(ctx context.Context, path string) error {
	c, err := load(path)
	if err != nil {
		return err
	}
	s := &server{config: c, ctx: ctx, boot: uuid(), backend: &http.Client{Timeout: 10 * time.Second}, log: slog.New(slog.NewJSONHandler(os.Stderr, nil)), bindings: map[string]*binding{}, stopped: make(chan struct{})}
	if raw := os.Getenv("KIKO_RUNNER_PARENT_PID"); raw != "" {
		parent, err := strconv.Atoi(raw)
		if err != nil || parent < 1 || os.Getppid() != parent {
			return errors.New("engine parent exited before runner startup")
		}
		s.lifetime = os.NewFile(3, "engine-lifetime")
		defer func() { _ = s.lifetime.Close() }() //nolint:errcheck // Inherited descriptor is also closed on process exit.
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		s.ctx = ctx
		go watchParent(ctx, parent, cancel)
	}
	s.scope, err = s.fetch(ctx)
	if err != nil {
		return err
	}
	s.project, err = s.scope.directory(c.Root)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(s.project, 0700); err != nil {
		return err
	}
	// Unix socket paths include this directory and must fit even with a long Engine temp root.
	s.temp, err = os.MkdirTemp("/tmp", "kr-")
	if err != nil {
		return err
	}
	defer func() {
		if e := os.RemoveAll(s.temp); e != nil {
			s.log.Warn("Runner temporary directory cleanup failed", "event", "runner.cleanup_failed")
		}
	}()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions/{session_id}/prepare", s.handle(s.prepare))
	mux.HandleFunc("POST /sessions/{session_id}/execution-window", s.handle(s.open))
	mux.HandleFunc("GET /sessions/{session_id}/state", s.handle(s.stateOf))
	mux.HandleFunc("POST /sessions/{session_id}/stop", s.handle(s.stop))
	mux.HandleFunc("POST /sessions/{session_id}/release", s.handle(s.release))
	mux.HandleFunc("POST /shutdown", s.handle(s.shutdown))
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(listener) }()
	if err = json.NewEncoder(os.Stdout).Encode(map[string]string{"event": "runner.ready", "url": "http://" + listener.Addr().String(), "boot_id": s.boot, "project": s.project}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-s.stopped:
	case err = <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("Runner listener failed", "event", "runner.listener_failed")
		}
	}
	cleanup, cancel := deadline(ctx)
	defer cancel()
	stopErr := s.closeBindings(cleanup)
	return errors.Join(stopErr, httpServer.Shutdown(cleanup))
}

type operation func(*http.Request) (any, error)

func watchParent(ctx context.Context, parent int, cancel context.CancelFunc) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if os.Getppid() != parent {
				cancel()
				return
			}
		}
	}
}

func (s *server) handle(op operation) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.config.ManagementToken)) != 1 {
			w.WriteHeader(401)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		out, err := op(r.WithContext(ctx))
		if err != nil {
			w.WriteHeader(409)
			out = map[string]any{"error": map[string]string{"code": "execution_unresolved", "message": "Runner operation could not be confirmed"}}
			s.log.Warn("Runner operation not confirmed", "event", "runner.request_failed", "route", r.Pattern)
		}
		if err = json.NewEncoder(w).Encode(out); err != nil {
			s.log.Debug("Runner response closed", "event", "http.closed")
		}
	}
}
func (s *server) get(ctx context.Context, id string, create bool) (*binding, error) {
	if !validID(id) {
		return nil, errors.New("invalid session")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, errors.New("runner closing")
	}
	b := s.bindings[id]
	if b != nil || !create {
		return b, nil
	}
	cfg, err := s.fetch(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.Lifecycle != "active" {
		return nil, errors.New("parent archived")
	}
	allowed := false
	for _, v := range cfg.Sessions {
		if v.SessionID == id && v.Lifecycle == "active" && v.EngineID != nil && *v.EngineID == s.config.EngineID {
			allowed = true
		}
	}
	if !allowed {
		return nil, errors.New("session not assigned")
	}
	b, err = s.startBuiltin(ctx, id)
	if err == nil {
		s.bindings[id] = b
	}
	return b, err
}
func (s *server) prepare(r *http.Request) (any, error) {
	b, err := s.get(r.Context(), r.PathValue("session_id"), true)
	if err != nil {
		return nil, err
	}
	return map[string]string{"url": b.url, "token": b.token}, nil
}
func (s *server) open(r *http.Request) (any, error) {
	var in struct {
		AssignmentID string `json:"assignment_id"`
		Generation   int64  `json:"generation,string"`
		HarnessID    string `json:"harness_instance_id"`
		TurnID       string `json:"turn_id"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if !validID(in.AssignmentID) || !validID(in.HarnessID) || !validID(in.TurnID) || in.Generation < 1 {
		return nil, errors.New("invalid execution identity")
	}
	b, err := s.get(r.Context(), r.PathValue("session_id"), true)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	cfg, err := s.fetch(r.Context())
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, v := range cfg.Sessions {
		if v.SessionID == r.PathValue("session_id") && v.Lifecycle == "active" && v.AssignmentID != nil && *v.AssignmentID == in.AssignmentID && v.Generation == in.Generation && v.EngineID != nil && *v.EngineID == s.config.EngineID {
			allowed = true
		}
	}
	if cfg.Lifecycle != "active" || !allowed {
		return nil, errors.New("execution no longer admitted")
	}
	old := b.window
	if !old.Open || old.AssignmentID != in.AssignmentID || old.Generation != in.Generation || old.HarnessID != in.HarnessID || old.TurnID != in.TurnID {
		b.window = window{Revision: old.Revision + 1, AssignmentID: in.AssignmentID, Generation: in.Generation, HarnessID: in.HarnessID, TurnID: in.TurnID, Open: true, Token: b.token}
	}
	return b.call(r.Context(), "/execution-window", b.window)
}
func (s *server) stateOf(r *http.Request) (any, error) {
	b, err := s.get(r.Context(), r.PathValue("session_id"), false)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return activity{Verified: true}, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.call(r.Context(), "/state", nil)
}
func (s *server) stop(r *http.Request) (any, error) {
	b, err := s.get(r.Context(), r.PathValue("session_id"), false)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return activity{Verified: true}, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stop(r.Context())
}
func (s *server) release(r *http.Request) (any, error) {
	id := r.PathValue("session_id")
	if !validID(id) {
		return nil, errors.New("invalid session")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.bindings[id]
	if b == nil {
		return activity{Verified: true}, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state, err := b.stop(r.Context())
	if err != nil {
		return nil, err
	}
	if err = b.abort(r.Context()); err != nil {
		return nil, err
	}
	b.transport.CloseIdleConnections()
	delete(s.bindings, id)
	return state, nil
}
func (s *server) shutdown(r *http.Request) (any, error) {
	var in struct {
		Reclaim bool `json:"reclaim"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.Reclaim {
		cfg, err := s.fetch(r.Context())
		if err != nil {
			return nil, err
		}
		if !cfg.ReclaimAllowed {
			return nil, errors.New("reclaim not authorized")
		}
	}
	if err := s.closeBindings(r.Context()); err != nil {
		return nil, err
	}
	select {
	case <-s.stopped:
	default:
		close(s.stopped)
	}
	return map[string]bool{"stopped": true}, nil
}
func (s *server) closeBindings(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closing = true
	var result error
	for id, b := range s.bindings {
		b.mu.Lock()
		_, err := b.stop(ctx)
		err = errors.Join(err, b.abort(ctx))
		if b.transport != nil {
			b.transport.CloseIdleConnections()
		}
		b.mu.Unlock()
		if err != nil {
			result = errors.Join(result, fmt.Errorf("session %s cleanup unresolved", id))
		} else {
			delete(s.bindings, id)
		}
	}
	return result
}
func decode(r *http.Request, out any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}
