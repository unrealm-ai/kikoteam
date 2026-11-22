package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/testpg"
)

func TestSnapshotRejectsOlderDumpClient(t *testing.T) {
	dsn, db := testpg.New(t)
	var serverVersion int
	if err := db.QueryRow("SHOW server_version_num").Scan(&serverVersion); err != nil {
		t.Fatal(err)
	}
	if serverVersion%10000 == 0 {
		t.Skip("test needs a PostgreSQL 16 minor release newer than 16.0")
	}
	t.Setenv("EVENT_HUB_TEST_DSN", dsn)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pg_dump"), []byte("#!/bin/sh\nprintf 'pg_dump (PostgreSQL) 16.0\\n'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	output := filepath.Join(t.TempDir(), "schema.sql")
	err := run(output, false, fstest.MapFS{})
	if err == nil || !strings.Contains(err.Error(), "at least as new as the test server") {
		t.Fatalf("older dump client was not rejected clearly: %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("rejected client created a snapshot: %v", err)
	}
}

func TestSnapshotReplayExcludesGooseStateAndDetectsDrift(t *testing.T) {
	dsn, _ := testpg.New(t)
	t.Setenv("EVENT_HUB_TEST_DSN", dsn)
	output := filepath.Join(t.TempDir(), "schema.sql")
	files := fstest.MapFS{"000001_init.sql": {Data: []byte("-- +goose Up\nCREATE TABLE snapshot_probe (id bigint PRIMARY KEY);")}}
	if err := run(output, false, files); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "SET client_encoding = 'UTF8';") {
		t.Fatal("schema snapshot must use UTF8 regardless of test server default encoding")
	}
	if !strings.Contains(string(data), "CREATE TABLE public.snapshot_probe") || strings.Contains(string(data), "goose_db_version") {
		t.Fatalf("snapshot missing business table or retaining migration state: %s", data)
	}
	if err := run(output, true, files); err != nil {
		if replayErr := run(output, false, files); replayErr == nil {
			replayed, readErr := os.ReadFile(output)
			if readErr == nil {
				t.Logf("initial snapshot:\n%s\nreplayed snapshot:\n%s", data, replayed)
			}
		}
		t.Fatalf("snapshot is not reproducible: %v", err)
	}
	if err := os.WriteFile(output, append(data, []byte("-- unexpected change\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	if err := run(output, true, files); err == nil {
		t.Fatal("snapshot drift was accepted")
	}
}

func TestDumpMinorGuardNormalizationPreservesSQL(t *testing.T) {
	old := []byte("--\n-- PostgreSQL database dump\n--\n\n-- Dumped from database version 16.6\n-- Dumped by pg_dump version 16.6\n\nSET statement_timeout = 0;\n\nCREATE FUNCTION f() RETURNS text AS $$\nline one\n\nline three\n$$ LANGUAGE sql;\n\n-- PostgreSQL database dump complete\n\n")
	newer := strings.Replace(string(old), "-- Dumped from", `\restrict randomized`+"\n\n-- Dumped from", 1) + `\unrestrict randomized` + "\n\n"
	if string(normalizeDump(old)) != string(normalizeDump([]byte(newer))) {
		t.Fatalf("minor pg_dump guard changed snapshot\nold=%q\nnew=%q", normalizeDump(old), normalizeDump([]byte(newer)))
	}
	if !strings.Contains(string(normalizeDump([]byte(newer))), "line one\n\nline three") {
		t.Fatal("SQL body whitespace changed")
	}
}
