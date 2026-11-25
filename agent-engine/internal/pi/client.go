package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Update is an unmodified native Pi session event.
type Update map[string]any
type reply struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}
type Client struct {
	cmd      *exec.Cmd
	in       io.WriteCloser
	mu       sync.Mutex
	writeMu  sync.Mutex
	promptMu sync.Mutex
	next     int
	pending  map[string]chan reply
	updates  chan Update
	active   bool
	readDone chan struct{}
	done     chan struct{}
	closing  chan struct{}
	once     sync.Once
	session  string
	readErr  error
	waitErr  error
}

func Start(ctx context.Context, cfg Config, session string) (*Client, error) {
	env, err := cfg.prepare()
	if err != nil {
		return nil, err
	}
	record, err := prepareSession(cfg.Directory, session)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, cfg.Launcher, "--mode", "rpc", "--session", record.File, "--system-prompt", cfg.systemPromptPath())
	if cfg.Lifetime != nil {
		cmd.ExtraFiles = []*os.File{cfg.Lifetime}
	}
	cmd.Dir, cmd.Env = filepath.Join(cfg.RuntimeDirectory, "work"), env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if e := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); errors.Is(e, syscall.ESRCH) {
			return os.ErrProcessDone
		} else {
			return e
		}
	}
	cmd.WaitDelay = 3 * time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard // Provider diagnostics can contain prompts or credentials.
	c := &Client{cmd: cmd, in: in, pending: map[string]chan reply{}, updates: make(chan Update, 1024), readDone: make(chan struct{}), done: make(chan struct{}), closing: make(chan struct{})}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	go c.read(out)
	go func() { <-c.readDone; c.waitErr = cmd.Wait(); close(c.done) }()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = c.bindSession(startup, cfg.Directory, record); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	return c, nil
}

func (c *Client) SessionID() string { return c.session }
func (c *Client) Alive() bool {
	select {
	case <-c.readDone:
		return false
	case <-c.closing:
		return false
	default:
		return true
	}
}
func (c *Client) read(r io.Reader) {
	defer close(c.readDone)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 65536), 16<<20)
	for scanner.Scan() {
		if err := c.dispatch(scanner.Bytes()); err != nil {
			c.readErr = err
			return
		}
	}
	c.readErr = errors.Join(errors.New("pi RPC stream closed"), scanner.Err())
}
func (c *Client) dispatch(data []byte) error {
	var envelope reply
	if json.Unmarshal(data, &envelope) != nil || envelope.Type == "" {
		return errors.New("invalid Pi RPC record")
	}
	if envelope.Type == "response" {
		c.mu.Lock()
		ch := c.pending[envelope.ID]
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- envelope:
			default:
			}
		}
		return nil
	}
	c.mu.Lock()
	active := c.active
	c.mu.Unlock()
	if envelope.Type == "extension_ui_request" {
		var request struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(data, &request); err != nil {
			return err
		}
		switch request.Method {
		case "confirm", "select", "input", "editor":
			if request.Method != "confirm" || !active {
				return c.send(map[string]any{"type": "extension_ui_response", "id": envelope.ID, "cancelled": true})
			}
		default:
			return nil
		}
	}
	if !active {
		return nil
	}
	var event Update
	if err := json.Unmarshal(data, &event); err != nil {
		return errors.New("invalid Pi event")
	}
	select {
	case c.updates <- event:
		return nil
	case <-c.closing:
		return errors.New("pi client closing")
	}
}
func (c *Client) Confirm(ctx context.Context, id string, confirmed bool) error {
	return c.sendCommand(ctx, map[string]any{"type": "extension_ui_response", "id": id, "confirmed": confirmed})
}

func (c *Client) send(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return json.NewEncoder(c.in).Encode(v)
}
func (c *Client) sendCommand(ctx context.Context, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sent := make(chan error, 1)
	go func() { sent <- c.send(v) }()
	select {
	case err := <-sent:
		return err
	case <-ctx.Done():
		// Closing stdin interrupts a blocked pipe write. An uncertain write must
		// retire the process, never leave it able to accept a delayed command.
		return errors.Join(ctx.Err(), c.Close())
	case <-c.closing:
		return errors.New("pi client closing")
	}
}

var ErrCommandRejected = errors.New("pi rejected RPC command")

func (c *Client) call(ctx context.Context, command string, params map[string]any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.next++
	id := strconv.Itoa(c.next)
	ch := make(chan reply, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	request := map[string]any{"id": id, "type": command}
	for k, v := range params {
		request[k] = v
	}
	if err := c.sendCommand(ctx, request); err != nil {
		return nil, fmt.Errorf("send Pi %s: %w", command, err)
	}
	select {
	case r := <-ch:
		if r.Command != command {
			return nil, errors.New("pi response command mismatch")
		}
		if !r.Success {
			return nil, fmt.Errorf("%w (%s): %s", ErrCommandRejected, command, r.Error)
		}
		return r.Data, nil
	case <-c.readDone:
		return nil, c.processFailure()
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closing:
		return nil, errors.New("pi client closing")
	}
}

func (c *Client) processFailure() error {
	select {
	case <-c.done:
		return errors.Join(c.readErr, c.waitErr)
	default:
		return c.readErr
	}
}

func (c *Client) Close() (result error) {
	c.once.Do(func() {
		close(c.closing)
		if err := c.in.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			result = err
		}
		select {
		case <-c.done:
			return
		default:
		}
		if err := syscall.Kill(-c.cmd.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			result = errors.Join(result, err)
		}
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
			if err := syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				result = errors.Join(result, err)
			}
			<-c.done
		}
	})
	return result
}
