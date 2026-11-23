package runtime

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/integration"
	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Sandboxes live independently of a Session worker, so idle assignment release
// cannot orphan the environment manager or prevent later parent reclamation.
type Sandboxes struct {
	Backend               *integration.Backend
	Lifetime              *os.File
	Root, Binary, Builtin string
	Log                   *slog.Logger
	mu                    sync.Mutex
	processes             map[string]*sandboxProcess
}
type sandboxProcess struct {
	runner *integration.Runner
	cmd    *exec.Cmd
	done   chan struct{}
	dir    string
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func (s *Sandboxes) For(ctx context.Context, work repository.Work) (*integration.Runner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.Backend.Sandboxes(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range items {
		if sandboxMatches(v, work) {
			if p := s.processes[v.ID]; p != nil {
				if p.runner == nil {
					return nil, errors.New("runner startup unresolved; inspect environment before retry")
				}
				select {
				case <-p.done:
					return nil, errors.New("runner exited unexpectedly; environment unresolved")
				default:
					return p.runner, nil
				}
			}
			if v.Lifecycle != "active" {
				return nil, errors.New("parent archived")
			}
			if s.processes == nil {
				s.processes = map[string]*sandboxProcess{}
			}
			p, err := s.start(ctx, v)
			if err != nil {
				return nil, err
			}
			if s.processes == nil {
				s.processes = map[string]*sandboxProcess{}
			}
			s.processes[v.ID] = p
			if err = s.Backend.SandboxState(ctx, v.ID, "ready"); err != nil {
				s.Log.Warn("Sandbox ready report pending", "event", "sandbox.report_pending", "sandbox_id", v.ID)
			}
			return p.runner, nil
		}
	}
	return nil, errors.New("sandbox not registered for this Engine")
}
func (s *Sandboxes) start(ctx context.Context, v integration.Sandbox) (*sandboxProcess, error) {
	temp, err := nodeDirectory("temp")
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(temp, "e-")
	if err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			if err := os.RemoveAll(dir); err != nil {
				s.Log.Warn("Runner launch cleanup failed", "error", err)
			}
		}
	}()
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	config := filepath.Join(dir, "runner.json")
	data, err := json.Marshal(map[string]string{"backend_url": s.Backend.URL, "config_token": v.ConfigToken, "management_token": token, "sandbox_id": v.ID, "engine_id": s.Backend.EngineID, "root": s.Root, "builtin": s.Builtin})
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(config, data, 0600); err != nil {
		return nil, err
	}
	// A detached cancel context lets worker cleanup finish before Runner exit.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), s.Binary, "--config", config)
	cmd.Env = []string{}
	if s.Lifetime != nil {
		cmd.ExtraFiles = []*os.File{s.Lifetime}
		cmd.Env = append(cmd.Env, fmt.Sprintf("KIKO_RUNNER_PARENT_PID=%d", os.Getpid()))
	}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_PATH"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	started = true
	p := &sandboxProcess{cmd: cmd, done: make(chan struct{}), dir: dir}
	s.processes[v.ID] = p // Retain startup failures; never race a second Runner.
	ready := make(chan string, 1)
	go func() {
		scan := bufio.NewScanner(out)
		for scan.Scan() {
			var v struct {
				Event string `json:"event"`
				URL   string `json:"url"`
			}
			if json.Unmarshal(scan.Bytes(), &v) == nil && v.Event == "runner.ready" {
				select {
				case ready <- v.URL:
				default:
				}
			}
		}
		if scan.Err() != nil {
			s.Log.Warn("runner startup stream closed", "event", "runner.log_closed")
		}
	}()
	go func() {
		if e := cmd.Wait(); e != nil {
			s.Log.Warn("Runner process exited", "event", "runner.exited", "sandbox_id", v.ID)
		}
		close(p.done)
	}()
	select {
	case address := <-ready:
		p.runner = &integration.Runner{URL: address, Token: token, HTTP: &http.Client{Timeout: 27 * time.Second}}
		return p, nil
	case <-p.done:
		return nil, errors.New("runner startup failed")
	case <-ctx.Done():
	case <-time.After(20 * time.Second):
	}
	if err = cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return nil, err
	}
	return nil, errors.New("runner startup timed out")
}
func (s *Sandboxes) Reap(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.Backend.Sandboxes(ctx)
	if err != nil {
		return err
	}
	for _, v := range items {
		if !v.ReclaimAllowed || v.State == "stopped" {
			continue
		}
		p := s.processes[v.ID]
		if p != nil {
			if err = s.close(ctx, p, true); err != nil {
				return err
			}
			delete(s.processes, v.ID)
		}
		if err = s.Backend.SandboxState(ctx, v.ID, "stopped"); err != nil {
			return err
		}
	}
	return nil
}
func (s *Sandboxes) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result error
	// Registration can assign an environment without ever spawning its Runner.
	// Include these in the handoff even when a previous Close report was lost.
	items, listErr := s.Backend.Sandboxes(ctx)
	result = errors.Join(result, listErr)
	for _, item := range items {
		if s.processes[item.ID] == nil && item.State == "preparing" {
			result = errors.Join(result, s.Backend.SandboxState(ctx, item.ID, "stopped"))
		}
	}
	for id, p := range s.processes {
		state := "stopped"
		err := s.close(ctx, p, false)
		if err != nil {
			state = "unknown"
			result = errors.Join(result, err)
		}
		if err = s.Backend.SandboxState(ctx, id, state); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}
func (s *Sandboxes) close(ctx context.Context, p *sandboxProcess, reclaim bool) error {
	if p.runner == nil {
		return errors.New("runner readiness was never confirmed")
	}
	if err := p.runner.Call(ctx, "POST", "/shutdown", map[string]bool{"reclaim": reclaim}, nil); err != nil {
		return err
	}
	select {
	case <-p.done:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return errors.New("runner exit not confirmed")
	}
	if err := os.RemoveAll(p.dir); err != nil {
		return fmt.Errorf("runner temporary state: %w", err)
	}
	return nil
}

func sandboxMatches(v integration.Sandbox, w repository.Work) bool {
	if v.WorkspaceID != w.WorkspaceID || v.AgentID != w.AgentID {
		return false
	}
	if w.TaskSpaceID != "" {
		return w.ConversationID == "" && w.TaskID != "" && v.ConversationID == "" && v.TaskSpaceID == w.TaskSpaceID && v.TaskID == w.TaskID
	}
	return w.ConversationID != "" && v.ConversationID == w.ConversationID && v.TaskSpaceID == "" && v.TaskID == ""
}
