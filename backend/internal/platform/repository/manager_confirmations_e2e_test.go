package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise unmodified Pi's real RPC dialog with a fixture-only extension.
// A private launcher keeps test prompts out of the shipped extension list.
func confirmationRuntimeRoot(t *testing.T, repo, temp string) string {
	t.Helper()
	root := filepath.Join(temp, "fixture")
	engine := filepath.Join(root, "agent-engine")
	scripts := filepath.Join(engine, "scripts")
	if err := os.MkdirAll(scripts, 0700); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"sandbox-runner", "agent-engine/extensions", "agent-engine/node_modules"} {
		if err := os.Symlink(filepath.Join(repo, rel), filepath.Join(root, rel)); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile(filepath.Join(repo, "agent-engine/scripts/pi.sh"))
	if err != nil {
		t.Fatal(err)
	}
	launcher := append([]byte("#!/bin/sh\nset -- \"$@\" -e \"$(dirname \"$0\")/confirm.mjs\"\n"), script...)
	if err = os.WriteFile(filepath.Join(scripts, "pi.sh"), launcher, 0700); err != nil {
		t.Fatal(err)
	}
	const extension = `export default function(pi) {
  pi.on("before_agent_start", async (event,ctx)=>{
    // The ordinary chat root prompt can carry a platform time prefix. Task
    // prompts still deliver the original body without the prefix.
    const body = event.prompt.replace(/^<system-reminder>本轮平台时间：[^\n]+<\/system-reminder>\n\n/, "");
    if (body !== "confirmation-fixture") return;
    const confirmed=await ctx.ui.confirm("报告范围","包含海外订单吗？");
    return {message:{customType:"test-choice",content:"fixture decision="+confirmed,display:false}};
  });
}`
	if err = os.WriteFile(filepath.Join(scripts, "confirm.mjs"), []byte(extension), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func confirmationModelReply(t *testing.T, w http.ResponseWriter, body runtimeRequest) bool {
	t.Helper()
	last := fmt.Sprint(body.Messages[len(body.Messages)-1].Content)
	var reply string
	switch {
	case strings.Contains(last, "fixture decision=false"):
		reply = "warm-denied"
	case strings.Contains(last, "包含海外订单吗？") && strings.Contains(last, "用户回答：否"):
		reply = "cold-denied"
	case strings.Contains(last, "ordinary-queued"):
		reply = "ordinary-after-answer"
	default:
		return false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, err := fmt.Fprintf(w, "data: {\"id\":\"choice\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%q},\"finish_reason\":null}]}\n\ndata: {\"id\":\"choice\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", reply)
	if err != nil {
		t.Error(err)
	}
	return true
}

func testRuntimeConfirmations(t *testing.T, ctx context.Context, db *sql.DB, base, agent string, call func(string, string, any, string) map[string]any, wait func(func() bool)) {
	t.Helper()
	for _, mode := range []string{"chat", "task"} {
		c := call("POST", base+"/conversations", map[string]string{"agent_id": agent}, "")["id"].(string)
		s := call("POST", base+"/sessions", map[string]any{"conversation_id": c, "mode": mode, "input": map[string]string{"text": "confirmation-fixture"}}, "question-"+mode)["id"].(string)
		path := base + "/sessions/" + s
		var action map[string]any
		wait(func() bool {
			actions := call("GET", path, nil, "")["execution"].(map[string]any)["required_actions"].([]any)
			if len(actions) != 1 {
				return false
			}
			action = actions[0].(map[string]any)
			return true
		})
		if action["type"] != "confirmation" || action["message"] != "包含海外订单吗？" {
			t.Fatal(action)
		}
		events := call("GET", path+"/events?type=session.requires_action", nil, "")["items"].([]any)
		if len(events) != 1 || events[0].(map[string]any)["turn_id"] != nil {
			t.Fatal(events)
		}
		q := call("POST", path+"/inputs", map[string]string{"text": "ordinary-queued", "delivery_mode": "queue"}, "ordinary")["id"]
		if mode == "task" {
			wait(func() bool {
				var released bool
				err := db.QueryRowContext(ctx, `SELECT engine_id IS NULL AND active_turn_id IS NULL AND required_actions<>'[]' FROM manager_sessions WHERE id=$1`, s).Scan(&released)
				return err == nil && released
			})
		}
		if input := call("GET", path+"/inputs/"+q.(string), nil, ""); input["status"] != "queued" {
			t.Fatal(input)
		}
		body := map[string]any{"confirmation": map[string]any{"confirmed": false, "request_id": action["request_id"]}}
		answer := call("POST", path+"/inputs", body, "answer")
		retry := call("POST", path+"/inputs", body, "answer-again")
		if retry["id"] != answer["id"] || answer["confirmed"] != false {
			t.Fatal(answer, retry)
		}
		wait(func() bool { return call("GET", path+"/inputs/"+q.(string), nil, "")["status"] == "processed" })
		var warm, ordered bool
		err := db.QueryRowContext(ctx, `SELECT a.turn_id=$1,ta.turn_no<tq.turn_no AND ta.status='completed' AND a.text LIKE '%用户回答：否'
 FROM manager_inputs a JOIN manager_turns ta ON ta.id=a.turn_id JOIN manager_inputs q ON q.id=$3 JOIN manager_turns tq ON tq.id=q.turn_id WHERE a.id=$2`, action["turn_id"], answer["id"], q).Scan(&warm, &ordered)
		if err != nil || !ordered || warm != (mode == "chat") {
			t.Fatalf("confirmation %s: warm=%t ordered=%t err=%v", mode, warm, ordered, err)
		}
		want := "cold-denied"
		if warm {
			want = "warm-denied"
		}
		output := fmt.Sprint(call("GET", path+"/events?type=agent.message", nil, "")["items"])
		if !strings.Contains(output, want) || !strings.Contains(output, "ordinary-after-answer") {
			t.Fatal("model did not receive the decision and queued input in separate turns", output)
		}
		if actions := call("GET", path, nil, "")["execution"].(map[string]any)["required_actions"].([]any); len(actions) != 0 {
			t.Fatal(actions)
		}
	}
}
