package app

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
)

func TestTwoInstancesMigrateOnceAndHandoff(t *testing.T) {
	dsn, db := testpg.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	files := fstest.MapFS{"000001_init.sql": {Data: []byte("-- +goose Up\nCREATE TABLE migrated (id integer);\nINSERT INTO migrated VALUES (42);")}}
	workers := [2]*worker{}
	stops := [2]context.CancelFunc{}
	done := [2]chan error{}
	for i := range workers {
		workers[i] = &worker{db: db, started: make(chan int, 1), stopped: make(chan bool, 1)}
		child, stop := context.WithCancel(ctx)
		stops[i] = stop
		defer stop()
		done[i] = make(chan error, 1)
		go func() { done[i] <- Run(child, config(dsn), files, workers[i], testLogger()) }()
	}
	leader := 0
	select {
	case <-workers[0].started:
	case <-workers[1].started:
		leader = 1
	case err := <-done[0]:
		t.Fatalf("instance 0: %v", err)
	case err := <-done[1]:
		t.Fatalf("instance 1: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	standby := 1 - leader
	select {
	case <-workers[standby].started:
		t.Fatal("two active workers")
	case err := <-done[standby]:
		t.Fatalf("standby exited: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	stops[leader]()
	select {
	case err := <-done[leader]:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-workers[standby].started:
	case err := <-done[standby]:
		t.Fatalf("takeover: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stops[standby]()
	select {
	case err := <-done[standby]:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM migrated").Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration replayed or skipped: count=%d err=%v", count, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id=1 AND is_applied").Scan(&count); err != nil || count != 1 {
		t.Fatalf("version recorded more than once: count=%d err=%v", count, err)
	}
}
