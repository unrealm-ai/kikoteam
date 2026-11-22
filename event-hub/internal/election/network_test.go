package election

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
)

// Keep the server-facing socket alive after blackholing traffic. This deliberately
// separates the client's timeout from PostgreSQL actually releasing the lock.
func TestSilentNetworkFailureDoesNotMeanLockReleased(t *testing.T) {
	dsn, db := testpg.New(t)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var blackhole atomic.Bool
	serverReady := make(chan net.Conn, 1)
	proxyError := make(chan error, 1)
	go func() {
		client, err := listener.Accept()
		if err != nil {
			proxyError <- err
			return
		}
		server, err := (&net.Dialer{}).DialContext(ctx, "tcp", u.Host)
		if err != nil {
			_ = client.Close()
			proxyError <- err
			return
		}
		serverReady <- server
		copyTraffic := func(dst, src net.Conn) {
			buffer := make([]byte, 8192)
			for {
				n, err := src.Read(buffer)
				if n > 0 && !blackhole.Load() {
					if _, writeErr := dst.Write(buffer[:n]); writeErr != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}
		go copyTraffic(client, server)
		copyTraffic(server, client)
		_ = client.Close()
	}()
	proxied := *u
	proxied.Host = listener.Addr().String()
	cfg := testConfig(proxied.String())
	leader, err := Acquire(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanupSession(t, leader)
	var server net.Conn
	select {
	case server = <-serverReady:
	case err := <-proxyError:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	t.Cleanup(func() { _ = server.Close() })
	_ = listener.Close() // The elected session must not be replaced by another connection.
	blackhole.Store(true)
	if err := leader.Monitor(ctx, 10*time.Millisecond, 80*time.Millisecond); err == nil {
		t.Fatal("silent failure went undetected")
	}
	if leader.Context().Err() == nil {
		t.Fatal("old work was not canceled")
	}
	var held bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory')", leader.PID()).Scan(&held); err != nil || !held {
		t.Fatalf("test must retain the server-side lock after local timeout: held=%v err=%v", held, err)
	}
	next := make(chan *Session, 1)
	standby := make(chan struct{}, 1)
	go func() {
		s, err := Acquire(ctx, testConfig(dsn), func(state string) {
			if state == "standby" {
				select {
				case standby <- struct{}{}:
				default:
				}
			}
		})
		if err != nil {
			proxyError <- err
			return
		}
		next <- s
	}()
	select {
	case <-standby:
	case err := <-proxyError:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case s := <-next:
		cleanupSession(t, s)
		t.Fatal("standby ignored server lock")
	default:
	}
	// Close the server leg to model PostgreSQL finally detecting disconnection.
	if err := server.Close(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	select {
	case s := <-next:
		cleanupSession(t, s)
	case err := <-proxyError:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
