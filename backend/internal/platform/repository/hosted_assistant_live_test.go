package repository

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// TestHostedAssistantLive uses an explicitly supplied local, validated model.
// No real connector is attached to the fresh workspace or its employees.
func TestHostedAssistantLive(t *testing.T) {
	dir := os.Getenv("KIKO_ASSISTANT_LIVE_MODEL_DIR")
	if dir == "" {
		t.Skip("provide an isolated local model fixture to run live all-scenario acceptance")
	}
	f := modelProxyFixture(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 12 * time.Second}
	owner, _ := f.register(t, client, "live-assistant@example.test")
	installAssistantTestModel(t, f, owner, dir)
	on := true
	if _, err := f.assistant.SaveAdmin(t.Context(), owner, service.AssistantSettingsPatch{ExpectedVersion: 1, Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	ws := f.api(t, client, "POST", "/workspaces", map[string]string{"name": "Assistant QA"}, "", 201)["id"].(string)
	if worked, err := f.assistant.ProvisionNext(t.Context()); err != nil || !worked {
		t.Fatal(worked, err)
	}
	entry, err := f.assistant.Entry(t.Context(), ws, owner)
	if err != nil || len(entry.Cards) != 3 {
		t.Fatal(entry, err)
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	stop := f.startEngine(t, ctx, root)
	defer stop()
	base := "/workspaces/" + ws
	c := f.api(t, client, "POST", base+"/conversations/web-direct", map[string]string{"agent_id": entry.AgentID}, "", 200)
	chat := ""
	for _, card := range entry.Cards {
		if filter := os.Getenv("KIKO_ASSISTANT_LIVE_CASES"); filter != "" && !strings.Contains(","+filter+",", ","+card.Key+",") {
			continue
		}
		var input string
		if chat == "" {
			out := f.api(t, client, "POST", base+"/sessions", map[string]any{"mode": "chat", "conversation_id": c["id"], "input": map[string]string{"text": card.Input}}, service.NewID(), 201)
			chat = out["id"].(string)
			if err = f.db.QueryRow(`SELECT id FROM manager_inputs WHERE session_id=$1 AND text=$2`, chat, card.Input).Scan(&input); err != nil {
				t.Fatal(err)
			}
		} else {
			out := f.api(t, client, "POST", base+"/sessions/"+chat+"/inputs", map[string]string{"text": card.Input, "delivery_mode": "queue"}, service.NewID(), 201)
			input, _ = out["id"].(string)
			if input == "" {
				if err = f.db.QueryRow(`SELECT id FROM manager_inputs WHERE session_id=$1 AND text=$2`, chat, card.Input).Scan(&input); err != nil {
					t.Fatal(err)
				}
			}
		}
		waitAssistantCase(t, ctx, f, client, base, card.Key, input)
	}
}

func installAssistantTestModel(t *testing.T, f *proxyFixture, owner, dir string) {
	t.Helper()
	var src struct {
		ID         string `json:"id"`
		Workspace  string `json:"workspace_id"`
		URL        string `json:"base_url"`
		Name       string `json:"model_id"`
		Protocol   string `json:"protocol"`
		Ciphertext string `json:"ciphertext"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "model.json"))
	if err != nil || json.Unmarshal(data, &src) != nil || !strings.HasPrefix(src.URL, "https://") {
		t.Fatal("validated HTTPS model fixture required")
	}
	root, err := os.ReadFile(filepath.Join(dir, "root"))
	if err != nil {
		t.Fatal("missing fixture encryption key")
	}
	key := sha256.Sum256(append([]byte("kiko:model-credential:v1:"), root...))
	block, _ := aes.NewCipher(key[:])
	aead, _ := cipher.NewGCM(block)
	encrypted, err := hex.DecodeString(src.Ciphertext)
	if err != nil || len(encrypted) < aead.NonceSize() {
		t.Fatal("invalid model fixture")
	}
	plain, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], []byte(src.Workspace+":"+src.ID))
	if err != nil {
		t.Fatal("could not decrypt model fixture")
	}
	id := service.NewID()
	sealed, err := f.secrets.Seal("provider", id, string(plain))
	if err != nil {
		t.Fatal(err)
	}
	reviewExec(t, f.db, `INSERT INTO platform_proxy_models(id,name,protocol,base_url,upstream_model_id,credential_ciphertext,
 input_price_units,output_price_units,enabled,is_default,validated,version,tested_at,updated_by)
 VALUES($1,'Local QA Model',$2,$3,$4,$5,1,1,true,true,true,1,now(),$6)`, id, src.Protocol, src.URL, src.Name, sealed, owner)
	reviewExec(t, f.db, "UPDATE platform_credit_wallets SET balance_units=10000000000 WHERE account_id=$1", owner)
}

func waitAssistantCase(t *testing.T, ctx context.Context, f *proxyFixture, client *http.Client, base, scenario, source string) {
	t.Helper()
	limit := time.Now().Add(220 * time.Second)
	want := 1
	if scenario == "complaints" {
		want = 2
	}
	answered := false
	for ctx.Err() == nil && time.Now().Before(limit) {
		if scenario == "meeting" && !answered {
			inbox := f.api(t, client, "GET", base+"/human-requests", nil, "", 200)
			for _, item := range inbox["items"].([]any) {
				request := item.(map[string]any)
				if request["state"] != "pending" {
					continue
				}
				question := request["question"].(map[string]any)
				if question["answer_type"] != "information" {
					t.Fatal("meeting did not use information confirmation")
				}
				body := map[string]any{"decision": "information", "version": request["version"], "text": "由林静负责上线后漏斗监控"}
				if qs, ok := question["questions"].([]any); ok && len(qs) > 0 {
					if len(qs) != 1 {
						t.Fatal("meeting should ask one unresolved item, not several")
					}
					field := qs[0].(map[string]any)
					questionText, _ := field["text"].(string)
					if !strings.Contains(questionText, "漏斗") && !strings.Contains(questionText, "监控") {
						t.Fatal("meeting asked about a resolved item instead of the unassigned post-release monitoring")
					}
					if field["answerType"] != "text" {
						t.Fatalf("meeting form expected text input, got %v", field["answerType"])
					}
					body["text"] = ""
					body["answers"] = []map[string]any{{"questionId": field["id"], "value": "林静"}}
				}
				f.api(t, client, "POST", base+"/human-requests/"+request["id"].(string)+"/answer", body, "", 200)
				answered = true
			}
		}
		var tasks, returned int
		err := f.db.QueryRow(`SELECT count(DISTINCT k.id),count(DISTINCT k.id) FILTER(WHERE r.id IS NOT NULL)
 FROM manager_tasks k LEFT JOIN manager_sessions s ON s.task_id=k.id AND s.mode='task'
 LEFT JOIN manager_turns turn ON turn.session_id=s.id AND turn.status='completed'
 LEFT JOIN manager_task_reports r ON r.source_turn_id=turn.id AND r.status='admitted'
 WHERE k.source_chat_input_id=$1`, source).Scan(&tasks, &returned)
		if err != nil {
			t.Fatal(err)
		}
		if returned >= want {
			if tasks != want || scenario == "meeting" && !answered {
				t.Fatalf("%s: tasks=%d answers=%t", scenario, tasks, answered)
			}
			assertAssistantToolTrace(t, f, source, scenario)
			t.Logf("%s: %d task(s) completed and reported", scenario, returned)
			return
		}
		time.Sleep(2 * time.Second)
	}
	var status, code string
	_ = f.db.QueryRow(`SELECT COALESCE(t.status,''),COALESCE(t.error->>'code','') FROM manager_inputs i
 LEFT JOIN manager_turns t ON t.id=i.turn_id WHERE i.id=$1`, source).Scan(&status, &code)
	t.Fatalf("%s timed out, source state=%s code=%s confirmation answered=%t", scenario, status, code, answered)
}

func assertAssistantToolTrace(t *testing.T, f *proxyFixture, source, scenario string) {
	t.Helper()
	var trace string
	err := f.db.QueryRow(`SELECT COALESCE(string_agg(e.event::text,' '),'') FROM manager_tasks k
 JOIN manager_sessions s ON s.task_id=k.id JOIN manager_session_events e ON e.session_id=s.id
 WHERE k.source_chat_input_id=$1`, source).Scan(&trace)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string][]string{
		"complaints": {"get_order", "get_logistics", "get_support_history", "ORD-1042", "ORD-1087"},
		"meeting":    {"get_meeting_notes", "get_release_plan", "list_project_chat", "林静"},
		"incident":   {"get_incident_logs", "get_release_changes", "get_oncall_runbook", "rollback_release", "93.2%", "02:15–02:20", "02:20–02:25", "99.4%", "99.6%"},
	}
	for _, term := range required[scenario] {
		if !strings.Contains(trace, term) {
			t.Errorf("%s missing evidence: %s", scenario, term)
		}
	}
	if scenario == "incident" {
		var confirmations int
		err := f.db.QueryRow(`SELECT count(*) FROM manager_human_requests h
 JOIN manager_sessions s ON s.id=h.session_id JOIN manager_tasks k ON k.id=s.task_id
 WHERE k.source_chat_input_id=$1`, source).Scan(&confirmations)
		if err != nil || confirmations != 0 {
			t.Errorf("incident must not need human confirmation: count=%d err=%v", confirmations, err)
		}
	}
	if scenario == "complaints" {
		var overlap bool
		err := f.db.QueryRow(`SELECT COALESCE(MAX(t.started_at)<MIN(t.finished_at),false) FROM manager_tasks k
 JOIN manager_sessions s ON s.task_id=k.id JOIN manager_turns t ON t.session_id=s.id AND t.status='completed'
 WHERE k.source_chat_input_id=$1`, source).Scan(&overlap)
		if err != nil || !overlap {
			t.Errorf("complaints did not overlap: %v", err)
		}
	}
}
