package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type window struct {
	Revision     int64  `json:"control_revision"`
	AssignmentID string `json:"assignment_id"`
	Generation   int64  `json:"generation"`
	HarnessID    string `json:"harness_instance_id"`
	TurnID       string `json:"turn_id"`
	Open         bool   `json:"open"`
	Token        string `json:"access_token"`
}
type activity struct {
	Verified      bool `json:"verified"`
	Active        int  `json:"active"`
	Unknown       int  `json:"unknown"`
	PendingWrites int  `json:"pending_writes"`
}

func (a activity) quiet() bool {
	return a.Verified && a.Active == 0 && a.Unknown == 0 && a.PendingWrites == 0
}

type binding struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	done       chan struct{}
	transport  *http.Transport
	client     *http.Client
	url, token string
	window     window
}

func (s *server) startBuiltin(ctx context.Context, id string) (*binding, error) {
	dir := filepath.Join(s.config.Root, "workspaces", s.scope.WorkspaceID, "sessions", id, "runner")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	tokenPath := filepath.Join(dir, "token")
	data, err := os.ReadFile(tokenPath)
	if errors.Is(err, os.ErrNotExist) {
		v, e := token()
		if e != nil {
			return nil, e
		}
		data = []byte(v)
		err = os.WriteFile(tokenPath, data, 0600)
	}
	if err != nil {
		return nil, err
	}
	if len(data) != 64 {
		return nil, errors.New("invalid stored tool credential")
	}
	socketDir, err := os.MkdirTemp(s.temp, "b-")
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		if err := os.RemoveAll(socketDir); err != nil {
			s.log.Warn("Builtin temporary cleanup failed", "session_id", id, "error", err)
		}
	}
	started := false
	defer func() {
		if !started {
			cleanup()
		}
	}()
	socket := filepath.Join(socketDir, "m.sock")
	args := []string{"--cwd", s.project, "--workspace-id", s.scope.WorkspaceID, "--session-id", id, "--sandbox-instance-id", s.scope.ID, "--runner-boot-id", s.boot, "--state-dir", filepath.Join(dir, "processes"), "--runtime-dir", socketDir, "--connection-id", uuid(), "--binding-id", "builtin", "--config-revision", "1"}
	path := filepath.Join(socketDir, "launch.json")
	if err = writePrivate(path, map[string]any{"host": "127.0.0.1", "port": 0, "allowed_hosts": []string{"127.0.0.1"}, "discovery_token": string(data), "launch_args": args}); err != nil {
		return nil, err
	}
	// Child lifetime is the Runner, not this management HTTP request.
	cmd := exec.CommandContext(s.ctx, "node", "--disable-warning=ExperimentalWarning", s.config.Builtin, "--config", path) //nolint:contextcheck // Runner context is the long-lived process owner.
	cmd.Env = runtimeEnvironment(socketDir)
	if s.lifetime != nil {
		cmd.ExtraFiles = []*os.File{s.lifetime}
	}
	cmd.Dir = s.project
	cmd.Stdout = io.Discard
	// The Runner owns graceful shutdown; its context cancellation does not kill
	// builtin before the process ledger has been checked.
	cmd.Cancel = func() error { return nil }
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	started = true
	b := &binding{cmd: cmd, done: make(chan struct{}), token: string(data), window: window{Revision: 1, AssignmentID: uuid(), Generation: 1, HarnessID: uuid(), TurnID: uuid(), Token: string(data)}}
	ready := make(chan int, 1)
	go func() {
		scan := bufio.NewScanner(stderr)
		for scan.Scan() {
			var v struct {
				Event  string `json:"event"`
				Port   int    `json:"port"`
				Level  string `json:"level"`
				Reason string `json:"reason"`
			}
			if json.Unmarshal(scan.Bytes(), &v) == nil && v.Event == "server.started" && v.Port > 0 {
				select {
				case ready <- v.Port:
				default:
				}
			}
			if v.Level == "ERROR" || v.Level == "WARN" {
				s.log.Warn("Builtin diagnostic", "event", v.Event, "reason", v.Reason, "session_id", id)
			}
		}
		if scan.Err() != nil {
			s.log.Warn("Builtin log stream closed", "event", "builtin.log_closed", "session_id", id)
		}
	}()
	go func() {
		if e := cmd.Wait(); e != nil {
			s.log.Warn("Builtin exited", "event", "builtin.exited", "session_id", id)
		}
		cleanup()
		close(b.done)
	}()
	select {
	case port := <-ready:
		b.url = "http://127.0.0.1:" + strconv.Itoa(port) + "/mcp"
	case <-b.done:
		return nil, errors.New("builtin exited before ready")
	case <-ctx.Done():
		return nil, errors.Join(errors.New("builtin startup canceled"), b.abort(ctx))
	case <-time.After(15 * time.Second):
		return nil, errors.Join(errors.New("builtin startup timed out"), b.abort(ctx))
	}
	b.transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	b.client = &http.Client{Transport: b.transport, Timeout: 10 * time.Second}
	return b, nil
}
func (b *binding) call(ctx context.Context, path string, v any) (activity, error) {
	var out activity
	select {
	case <-b.done:
		return out, errors.New("builtin not running")
	default:
	}
	var data []byte
	var err error
	method := "GET"
	if v != nil {
		method = "POST"
		data, err = json.Marshal(v)
		if err != nil {
			return out, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := b.client.Do(req)
	if err != nil {
		return out, err
	}
	defer func() { _ = res.Body.Close() }() //nolint:errcheck // Bounded decode below handles read errors.
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, fmt.Errorf("builtin management HTTP %d", res.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&out)
	return out, err
}
func (b *binding) stop(ctx context.Context) (activity, error) {
	b.window.Revision++
	b.window.Open = false
	out, err := b.call(ctx, "/stop", b.window)
	if err != nil {
		return out, err
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for !out.quiet() {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-tick.C:
		}
		out, err = b.call(ctx, "/state", nil)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
func (b *binding) abort(ctx context.Context) error {
	select {
	case <-b.done:
		return errors.New("builtin exited")
	default:
	}
	if err := b.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
	case <-time.After(14 * time.Second):
	}
	if err := b.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	<-b.done
	return errors.New("builtin forced exit; verify retained ledger")
}
