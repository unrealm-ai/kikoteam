package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTriggerCanonicalSignatureAndCommandBoundaries(t *testing.T) {
	now := time.UnixMilli(1800000000000)
	id := NewID()
	stamp := strconv.FormatInt(now.UnixMilli(), 10)
	body := []byte(`{"version":1}`)
	hash := sha256.Sum256(body)
	canonical := strings.Join([]string{"v1", "POST", "/api/triggers/webhook/" + id, stamp, hex.EncodeToString(hash[:])}, "\n")
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(canonical))
	sig := hex.EncodeToString(mac.Sum(nil))
	if _, e := VerifyTriggerSignature(id, "secret", stamp, sig, body, now); e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct {
		id, key, stamp, sig string
		body                []byte
		now                 time.Time
	}{{NewID(), "secret", stamp, sig, body, now}, {id, "other", stamp, sig, body, now}, {id, "secret", stamp, sig, []byte(`{"version":2}`), now}, {id, "secret", stamp, sig, body, now.Add(301 * time.Second)}, {id, "secret", "0" + stamp, sig, body, now}, {id, "secret", stamp, strings.ToUpper(sig), body, now}} {
		if _, e := VerifyTriggerSignature(v.id, v.key, v.stamp, v.sig, v.body, v.now); e == nil {
			t.Fatal("invalid signature accepted", v)
		}
	}
	for _, q := range []TriggerCommand{{Version: 1, EventID: "bad\t", Action: "create_task", Description: "x"}, {Version: 1, EventID: "ok", Action: "create_task", Description: strings.Repeat("a", 8001)}, {Version: 1, EventID: "ok", Action: "supplement_task", Description: "x"}, {Version: 1, EventID: "ok", Action: "send_message", Description: "x"}} {
		if ValidateTriggerCommand(q) == nil {
			t.Fatal("invalid command", q.Action)
		}
	}
}
