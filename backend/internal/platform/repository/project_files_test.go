package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestPrivateProjectUploadAndMessage(t *testing.T) {
	_, svc, mux, w, actor, agent, cookie := managerFixture(t)
	svc.ProjectRoot = t.TempDir()
	ctx := context.Background()
	conv, err := svc.ResolveWebConversation(ctx, w, actor, agent)
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/workspaces/" + w + "/conversations/" + conv.ID + "/files"
	request := func(method, address, key, ct string, data []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, address, bytes.NewReader(data))
		r.Header.Set("Cookie", cookie)
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("X-CSRF-Protection", "1")
		r.Header.Set("Content-Type", ct)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		return out
	}
	upload := func(key, body string, want int) manager.UploadedFile {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		f, e := mw.CreateFormFile("file", "销售.csv")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write([]byte(body)); e != nil {
			t.Fatal(e)
		}
		if e = mw.Close(); e != nil {
			t.Fatal(e)
		}
		out := request("POST", base, key, mw.FormDataContentType(), buf.Bytes())
		if out.Code != want {
			t.Fatalf("upload %d: %s", out.Code, out.Body.String())
		}
		var v manager.UploadedFile
		if e = json.Unmarshal(out.Body.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	a := upload("upload", "sales", 201)
	if a.Path != "uploads/销售.csv" || upload("upload", "sales", 200) != a {
		t.Fatal("replay changed the file")
	}
	upload("upload", "other", 409)
	if upload("second", "other", 201).Path != "uploads/销售 (2).csv" {
		t.Fatal("collision")
	}
	if out := request("GET", base+"?path=uploads", "", "", nil); out.Code != 200 || !strings.Contains(out.Body.String(), a.Path) {
		t.Fatal(out.Body.String())
	}
	get := func(p string) *httptest.ResponseRecorder {
		return request("GET", base+"/content?"+url.Values{"path": {p}}.Encode(), "", "", nil)
	}
	if out := get(a.Path); out.Code != 200 || out.Body.String() != "sales" {
		t.Fatal(out.Code, out.Body.String())
	}
	dir := filepath.Join(svc.ProjectRoot, "state/sandboxes/workspaces", w, "employees", agent, "conversations", conv.ID, "project")
	if e := os.Symlink(t.TempDir(), filepath.Join(dir, "link")); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"../outside", "link/test", "/tmp", "uploads"} {
		if out := get(p); out.Code != 400 {
			t.Fatal(p, out.Code)
		}
	}
	sessionURL := "/api/v1/workspaces/" + w + "/sessions"
	body, err := json.Marshal(map[string]any{"conversation_id": conv.ID, "mode": "chat", "input": map[string]any{"attachment_paths": []string{a.Path}}})
	if err != nil {
		t.Fatal(err)
	}
	out := request("POST", sessionURL, "first", "application/json", body)
	if out.Code != 201 {
		t.Fatal(out.Code, out.Body.String())
	}
	var chat manager.Session
	if e := json.Unmarshal(out.Body.Bytes(), &chat); e != nil {
		t.Fatal(e)
	}
	v, e := svc.Store.Input(ctx, w, actor, chat.ID, *chat.InitialInputID)
	if e != nil || len(v.Attachments) != 1 || v.Attachments[0] != a.Attachment || v.Text != "" {
		t.Fatal(v, e)
	}
	if e = os.Remove(filepath.Join(dir, a.Path)); e != nil {
		t.Fatal(e)
	}
	if out = request("POST", sessionURL, "first", "application/json", body); out.Code != 200 {
		t.Fatal("lost-reply replay should survive removal", out.Body.String())
	}
	if out = get(a.Path); out.Code != 404 {
		t.Fatal(out.Code)
	}
	if out = request("POST", sessionURL+"/"+chat.ID+"/inputs", "missing", "application/json", []byte(`{"attachment_paths":["uploads/销售.csv"]}`)); out.Code != 404 || !strings.Contains(out.Body.String(), a.Path) {
		t.Fatal(out.Code, out.Body.String())
	}
}

// Use a real HTTP connection so both net/http socket deadlines are exercised.
func TestProjectUploadOutlastsOrdinaryHTTPDeadline(t *testing.T) {
	_, svc, mux, w, actor, agent, cookie := managerFixture(t)
	svc.ProjectRoot = t.TempDir()
	c, err := svc.ResolveWebConversation(t.Context(), w, actor, agent)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(mux)
	server.Config.ReadTimeout = 250 * time.Millisecond
	server.Config.WriteTimeout = 250 * time.Millisecond
	server.Start()
	defer server.Close()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	f, err := form.CreateFormFile("file", "slow.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(f, "data"); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	wire := body.Bytes()
	split := bytes.Index(wire, []byte("\r\n\r\n")) + 5 // inside the file, after its first byte
	rd, wr := io.Pipe()
	defer func() { _ = rd.Close() }()
	go func() {
		_, err := wr.Write(wire[:split])
		if err == nil {
			time.Sleep(500 * time.Millisecond)
			_, err = wr.Write(wire[split:])
		}
		_ = wr.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(t.Context(), "POST", server.URL+"/api/v1/workspaces/"+w+"/conversations/"+c.ID+"/files", rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("X-CSRF-Protection", "1")
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Idempotency-Key", "slow")
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var got manager.UploadedFile
	if err = json.NewDecoder(res.Body).Decode(&got); err != nil || res.StatusCode != 201 || got.Path != "uploads/slow.csv" || got.Size != 4 {
		t.Fatal(res.Status, got, err)
	}

	// Exhausting the upload's own read deadline is a 408, even mid-part.
	timedOut := req.Clone(t.Context())
	timedOut.Body = io.NopCloser(io.MultiReader(bytes.NewReader(wire[:split]), iotest.ErrReader(os.ErrDeadlineExceeded)))
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, timedOut)
	if out.Code != http.StatusRequestTimeout || !strings.Contains(out.Body.String(), "upload_timeout") {
		t.Fatal(out.Code, out.Body.String())
	}
}

type failAfterPublication struct{ manager.Store }

func (s failAfterPublication) WithProjectPublication(ctx context.Context, target manager.Target, publish func(manager.SessionState) error) error {
	return s.Store.WithProjectPublication(ctx, target, func(state manager.SessionState) error {
		if err := publish(state); err != nil {
			return err
		}
		return errors.New("transaction outcome lost after publication")
	})
}

func TestProjectUploadVisibleAfterUncertainCommit(t *testing.T) {
	_, svc, _, w, actor, agent, _ := managerFixture(t)
	svc.ProjectRoot = t.TempDir()
	ctx := t.Context()
	c, err := svc.ResolveWebConversation(ctx, w, actor, agent)
	if err != nil {
		t.Fatal(err)
	}
	store := svc.Store
	svc.Store = failAfterPublication{store}
	upload := func() (manager.UploadedFile, bool, error) {
		return svc.UploadProject(ctx, c, actor, "lost-commit", "uploads", "notes.txt", strings.NewReader("notes"))
	}
	if _, _, err = upload(); err == nil {
		t.Fatal("expected transaction error")
	}
	svc.Store = store
	entries, err := svc.ListProject(c, "uploads")
	if err != nil || len(entries.Entries) != 1 || entries.Entries[0].Path != "uploads/notes.txt" {
		t.Fatal(entries, err)
	}
	f, _, err := svc.OpenProjectFile(c, entries.Entries[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	if err = errors.Join(err, f.Close()); err != nil || string(data) != "notes" {
		t.Fatal(string(data), err)
	}
	file, replay, err := upload()
	if err != nil || !replay || file.Path != entries.Entries[0].Path {
		t.Fatal(file, replay, err)
	}
}

func TestProjectUploadLegacyReceiptAndCaseSensitiveKeys(t *testing.T) {
	_, svc, _, w, actor, agent, _ := managerFixture(t)
	root := t.TempDir()
	svc.ProjectRoot = root
	ctx := t.Context()
	c, err := svc.ResolveWebConversation(ctx, w, actor, agent)
	if err != nil {
		t.Fatal(err)
	}
	upload := func(key string, wantReplay bool) manager.UploadedFile {
		t.Helper()
		file, replay, err := svc.UploadProject(ctx, c, actor, key, "uploads", "data.csv", strings.NewReader(key))
		if err != nil || replay != wantReplay {
			t.Fatal(key, file, replay, err)
		}
		return file
	}
	original := upload("JobA", false)
	// Recreate a v1 crash after linking the candidate, before marking it published.
	legacyParent := filepath.Join(root, "state/project-uploads", w, c.ID, actor)
	if err = os.MkdirAll(legacyParent, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(legacyParent, "JobA")
	current := filepath.Join(root, "state/project-uploads-v2", w, c.ID, actor, fmt.Sprintf("%x", sha256.Sum256([]byte("JobA"))))
	if err = os.Rename(current, legacy); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "state/sandboxes/workspaces", w, "employees", agent, "conversations", c.ID, "project")
	if err = os.Link(filepath.Join(project, original.Path), filepath.Join(legacy, "payload")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = manager.UpgradeProjectUploadReceipts(root); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := svc.ListProject(c, "uploads")
	if err != nil || len(tree.Entries) != 1 || tree.Entries[0].Path != original.Path {
		t.Fatal(tree, err)
	}
	if got := upload("JobA", true); got != original {
		t.Fatal(got, original)
	}
	other := upload("joba", false)
	if other.Path != "uploads/data (2).csv" || upload("joba", true) != other {
		t.Fatal(other)
	}
	for key, file := range map[string]manager.UploadedFile{"JobA": original, "joba": other} {
		got, err := os.ReadFile(filepath.Join(project, file.Path))
		if err != nil || string(got) != key {
			t.Fatal(key, string(got), err)
		}
	}
}

func TestProjectUploadsConcurrentRetries(t *testing.T) {
	_, s, _, w, actor, agent, _ := managerFixture(t)
	s.ProjectRoot = t.TempDir()
	ctx := context.Background()
	c, err := s.ResolveWebConversation(ctx, w, actor, agent)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	names := make(chan string, 6)
	for _, key := range []string{"a", "b", "a", "b", "a", "b"} {
		wg.Go(func() {
			file, replay, e := s.UploadProject(ctx, c, actor, key, "", "汇总.csv", strings.NewReader(key))
			if e != nil {
				t.Error(e)
				return
			}
			if !replay {
				names <- file.Path
			}
		})
	}
	wg.Wait()
	close(names)
	var created []string
	for p := range names {
		created = append(created, p)
	}
	slices.Sort(created)
	if !slices.Equal(created, []string{"汇总 (2).csv", "汇总.csv"}) {
		t.Fatal(created)
	}
}

func TestProjectPathsThroughDeferredTask(t *testing.T) {
	db, s, delegation, scope := plannedFixture(t)
	s.ProjectRoot = t.TempDir()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `UPDATE manager_conversations SET web_user_id=created_by WHERE id=$1`, scope.ConversationID); err != nil {
		t.Fatal(err)
	}
	c, err := s.WebProject(ctx, scope.WorkspaceID, scope.ActorID, scope.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.csv", "b.csv"} {
		if _, _, err = s.UploadProject(ctx, c, scope.ActorID, name[:1], "", name, strings.NewReader("test")); err != nil {
			t.Fatal(err)
		}
	}
	args := manager.DelegationArgs{Instruction: "record work", RequestKey: "plan-files", AttachmentPaths: []string{"a.csv"}}
	_, err = delegation.Call(ctx, scope, "create_todo", args)
	var denied *manager.Error
	if !errors.As(err, &denied) || denied.Status != 403 {
		t.Fatalf("unreferenced file: %v", err)
	}
	if _, _, err = s.SubmitInputMode(ctx, scope.WorkspaceID, scope.ActorID, scope.Identity.SessionID, "reference-files", "", "queue", "a.csv", "b.csv"); err != nil {
		t.Fatal(err)
	}
	raw, err := delegation.Call(ctx, scope, "create_todo", args)
	if err != nil {
		t.Fatal(err)
	}
	plan := raw.(manager.PlannedTask)
	_, err = delegation.Call(ctx, scope, "supplement_todo", manager.DelegationArgs{SessionID: plan.SessionID, ExpectedVersion: 1, RequestKey: "supplement-files", Instruction: "also compare", AttachmentPaths: []string{"a.csv", "b.csv"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = delegation.Call(ctx, scope, "start_todo", manager.DelegationArgs{SessionID: plan.SessionID, ExpectedVersion: 2, RequestKey: "start-files"})
	if err != nil {
		t.Fatal(err)
	}
	started := raw.(manager.PlannedTask)
	if started.InitialInputID == nil {
		t.Fatal("missing input")
	}
	input, err := s.Store.Input(ctx, scope.WorkspaceID, scope.ActorID, plan.SessionID, *started.InitialInputID)
	if err != nil || len(input.Attachments) != 2 || input.Attachments[0].Path != "a.csv" || input.Attachments[1].Path != "b.csv" {
		t.Fatal(input.Attachments, err)
	}
}
