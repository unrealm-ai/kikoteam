package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
)

// Execute the actual entry point in a subprocess so signal handling and os.Exit
// are tested without terminating the parent test runner.
func TestProcessHelper(t *testing.T) {
	if os.Getenv("EVENT_HUB_PROCESS_TEST") != "1" {
		return
	}
	main()
}

type process struct {
	cmd    *exec.Cmd
	states chan string
	done   chan error
}

func startProcess(t *testing.T, dsn string) *process {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
	var env []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "EVENT_HUB_") {
			env = append(env, v)
		}
	}
	cmd.Env = append(env, "EVENT_HUB_PROCESS_TEST=1", "EVENT_HUB_DATABASE_URL="+dsn,
		"EVENT_HUB_HEALTH_ADDR=127.0.0.1:0", "EVENT_HUB_RETRY_INTERVAL=20ms",
		"EVENT_HUB_HEARTBEAT_INTERVAL=20ms", "EVENT_HUB_HEARTBEAT_TIMEOUT=200ms")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &process{cmd: cmd, states: make(chan string, 64), done: make(chan error, 1)}
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			var record struct {
				Phase string `json:"phase"`
			}
			if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Phase != "" {
				p.states <- record.Phase
			}
		}
		p.done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-p.done: //nolint:errcheck // Cleanup only joins a process that may have been deliberately killed.
		case <-time.After(3 * time.Second):
		}
	})
	return p
}

func (p *process) waitState(t *testing.T, state string) {
	t.Helper()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case phase := <-p.states:
			if phase == state {
				return
			}
		case err := <-p.done:
			t.Fatalf("process exited before %s: %v", state, err)
		case <-timeout.C:
			t.Fatalf("timeout waiting for %s", state)
		}
	}
}

func TestProcessSignalAndCrashTakeover(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		t.Run(signal.String(), func(t *testing.T) {
			dsn, db := testpg.New(t)
			leader := startProcess(t, dsn)
			leader.waitState(t, "running")
			standby := startProcess(t, dsn)
			standby.waitState(t, "standby")
			if err := leader.cmd.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-leader.done:
				if (signal == syscall.SIGTERM && err != nil) || (signal == syscall.SIGKILL && err == nil) {
					t.Fatalf("signal exit: %v", err)
				}
				// Cleanup also waits on done.
				leader.done <- err
			case <-time.After(10 * time.Second):
				t.Fatal("leader did not exit")
			}
			standby.waitState(t, "running")
			if err := standby.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-standby.done:
				standby.done <- err
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("successor did not exit")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var count int
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_locks WHERE database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND locktype='advisory'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("lock survived process exits: count=%d err=%v", count, err)
			}
		})
	}
}
