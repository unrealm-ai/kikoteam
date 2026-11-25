package pi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPlanOnlyNativePiDoesNotOfferTools(t *testing.T) {
	calls := make(chan int, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
			return
		}
		calls <- len(body.Tools)
		w.Header().Set("Content-Type", "text/event-stream")
		if _, e := io.WriteString(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"draft\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"); e != nil {
			t.Error(e)
		}
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg := upstreamConfig(t, provider.URL+"/v1")
	cfg.PlanOnly = true
	cfg.MCP = map[string]any{"unsafe": map[string]any{"url": "http://127.0.0.1:1"}}
	if _, e := cfg.prepare(); e == nil {
		t.Fatal("plan-only accepted a configured tool")
	}
	cfg.MCP = nil
	c, e := Start(ctx, cfg, "")
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := c.Close(); e != nil {
			t.Error(e)
		}
	}()
	if _, e = c.Prompt(ctx, "Prepare a corrected draft", nil, func(Update) {}); e != nil {
		t.Fatal(e)
	}
	select {
	case n := <-calls:
		if n != 0 {
			t.Fatalf("model saw %d tools", n)
		}
	default:
		t.Fatal("model endpoint was not reached")
	}
}
