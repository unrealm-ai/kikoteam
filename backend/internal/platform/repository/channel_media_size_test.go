package repository

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestInboundChannelMediaSizeAtManagerBoundary(t *testing.T) {
	db, m, store, _, _, _, binding := channelManagerFixture(t)
	m.ProjectRoot = t.TempDir()
	ctx := t.Context()
	input := inboundFixture(binding, "attachment-size", "alice")
	input.Media = []manager.MediaReference{{Kind: "file", Name: "report.bin", Locator: "sealed"}}
	box, err := store.ReceiveChannel(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimMedia(ctx, m)
	if err != nil || job == nil || job.InboxID != box.ID {
		t.Fatalf("claim: %+v %v", job, err)
	}

	const limit = 100 << 20
	file, err := os.Create(filepath.Join(t.TempDir(), "payload"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := file.Truncate(limit); err != nil {
		t.Fatal(err)
	}
	const token = "test-inbound-channel-media-size-0000"
	mux := http.NewServeMux()
	(&managercontroller.ChannelServer{Store: store, Manager: m, Token: token}).Routes(mux)
	endpoint := fmt.Sprintf("/api/internal/v1/channel-media/%s/%d", job.InboxID, job.Ordinal)
	over := httptest.NewRequest(http.MethodPut, endpoint, strings.NewReader(""))
	over.ContentLength = limit + 1
	over.Header.Set("Authorization", "Bearer "+token)
	over.Header.Set("X-Kiko-Media-Lease", job.Lease)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, over)
	if out.Code != 413 || !strings.Contains(out.Body.String(), "file_too_large") {
		t.Fatalf("oversized upload: %d %s", out.Code, out.Body)
	}

	// Use an unknown length to also exercise the bounded request reader.
	atLimit := httptest.NewRequest(http.MethodPut, endpoint, file)
	atLimit.Header.Set("Authorization", "Bearer "+token)
	atLimit.Header.Set("X-Kiko-Media-Lease", job.Lease)
	out = httptest.NewRecorder()
	mux.ServeHTTP(out, atLimit)
	if out.Code != 200 {
		t.Fatalf("exact-limit upload: %d %s", out.Code, out.Body)
	}
	var storedPath string
	if err := db.QueryRowContext(ctx, "SELECT path FROM manager_channel_media WHERE inbox_id=$1 AND ordinal=0", job.InboxID).Scan(&storedPath); err != nil || storedPath != "uploads/report.bin" {
		t.Fatalf("stored path: %q %v", storedPath, err)
	}
	paths, err := filepath.Glob(filepath.Join(m.ProjectRoot, "workspaces", "*", "projects", "conv-*", storedPath))
	if err != nil || len(paths) != 1 {
		t.Fatalf("project file missing: %v %v", paths, err)
	}
	info, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != limit {
		t.Fatalf("project file size = %d, want %d", info.Size(), limit)
	}
}
