package channel

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestInboundProviderMediaAndBoundLocator(t *testing.T) {
	tests := []struct {
		provider string
		refs     []MediaReference
	}{
		{"feishu", feishuMedia("post", `{"zh_cn":{"content":[[{"tag":"img","image_key":"img"},{"tag":"file","file_key":"file","file_name":"r.csv"}]]}}`)},
		{"dingtalk", append(dingMedia("picture", json.RawMessage(`{"downloadCode":"img"}`)), dingMedia("file", json.RawMessage(`{"downloadCode":"file","fileName":"r.csv"}`))...)},
		{"wecom", wecomMedia("mixed", json.RawMessage(`{"mixed":{"msg_item":[{"msgtype":"image","image":{"url":"img","aeskey":"sensitive-key"}},{"msgtype":"file","file":{"url":"file","name":"r.csv"}}]}}`))},
	}
	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			if len(tc.refs) != 2 || tc.refs[0].Kind != "image" || tc.refs[1].Name != "r.csv" {
				t.Fatal(tc.refs)
			}
			input := Inbound{BindingID: "binding", Generation: 3, MessageID: "message", Media: tc.refs}
			sealed, err := sealMedia(input, "hub-token")
			if err != nil {
				t.Fatal(err)
			}
			a, err := mediaCipher("hub-token")
			if err != nil {
				t.Fatal(err)
			}
			for i, r := range sealed.Media {
				ct, err := base64.RawStdEncoding.DecodeString(r.Locator)
				if err != nil {
					t.Fatal(err)
				}
				plain, err := a.Open(nil, nil, ct, mediaAAD("binding", 3, "message", i))
				if err != nil || string(plain) != tc.refs[i].Locator {
					t.Fatal("locator round trip", err)
				}
				for _, other := range [][]byte{mediaAAD("other", 3, "message", i), mediaAAD("binding", 4, "message", i), mediaAAD("binding", 3, "other", i), mediaAAD("binding", 3, "message", i+1)} {
					if _, err = a.Open(nil, nil, ct, other); err == nil {
						t.Fatal("accepted locator for another source")
					}
				}
			}
		})
	}
}
func TestWecomDecryptMedia(t *testing.T) {
	key := bytes.Repeat([]byte("k"), 32)
	plain := []byte("wecom attachment")
	pad := 32 - len(plain)%32
	encrypted := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	cipher.NewCBCEncrypter(block, key[:16]).CryptBlocks(encrypted, encrypted)
	encoded := base64.RawStdEncoding.EncodeToString(key)
	valid := append([]byte(nil), encrypted...)
	data, err := decryptWecomFile(valid, encoded)
	if err != nil || !bytes.Equal(data, plain) {
		t.Fatal("decryption", err)
	}
	encrypted[len(encrypted)-17] ^= 255
	if _, err = decryptWecomFile(encrypted, encoded); err == nil {
		t.Fatal("accepted corrupt padding")
	}
}
func assertUpload(t *testing.T, r *http.Request, field string) {
	t.Helper()
	part, _, err := r.FormFile(field)
	if err != nil {
		t.Error(err)
		return
	}
	defer func() {
		if err := part.Close(); err != nil {
			t.Error(err)
		}
	}()
	data, err := io.ReadAll(part)
	if err != nil || string(data) != "frozen bytes" {
		t.Error("unexpected uploaded version", err)
	}
}
func writeProviderJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Error(err)
	}
}
func TestFeishuNativeMediaReply(t *testing.T) {
	for _, format := range []string{"image", "file"} {
		t.Run(format, func(t *testing.T) {
			sent, uploaded := 0, 0
			key := format + "_key"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(r.URL.Path, "tenant_access_token"):
					writeProviderJSON(t, w, map[string]any{"code": 0, "tenant_access_token": "fixture-token", "expire": 7200})
					return
				case r.URL.Path == "/open-apis/im/v1/"+format+"s":
					assertUpload(t, r, format)
					uploaded++
					writeProviderJSON(t, w, map[string]any{"code": 0, "data": map[string]string{key: "native-id"}})
				case r.URL.Path == "/open-apis/im/v1/messages/original/reply":
					var body struct {
						Type    string `json:"msg_type"`
						Content string `json:"content"`
					}
					var payload map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || json.Unmarshal([]byte(body.Content), &payload) != nil || payload[key] != "native-id" || body.Type != format || uploaded != 1 {
						t.Error("wrong media reply", body)
					}
					sent++
					writeProviderJSON(t, w, map[string]any{"code": 0, "data": map[string]string{"message_id": "sent", "chat_id": "chat"}})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			api := lark.NewClient("app", "secret", lark.WithOpenBaseUrl(srv.URL), lark.WithEnableTokenCache(false), lark.WithLogger(quietLogger{}))
			got := sendFeishuWithClient(context.Background(), api, Delivery{Format: format, ArtifactID: "result", Media: &deliveryMedia{Name: "report.csv", Data: []byte("frozen bytes")}, ChatID: "chat", ReplyToMessageID: "original", IdempotencyKey: "stable-operation"})
			if got.Status != "sent" || sent != 1 || uploaded != 1 {
				t.Fatal(got, sent, uploaded)
			}
		})
	}
}

type localDingUpload struct {
	endpoint  *url.URL
	transport http.RoundTripper
}

func (h localDingUpload) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "oapi.dingtalk.com" {
		r = r.Clone(r.Context())
		r.URL.Host = h.endpoint.Host
		r.URL.Scheme = h.endpoint.Scheme
	}
	return h.transport.RoundTrip(r)
}
func TestDingTalkNativeMediaTarget(t *testing.T) {
	for _, kind := range []string{"direct", "group"} {
		for _, format := range []string{"image", "file"} {
			t.Run(kind+"/"+format, func(t *testing.T) {
				sent, uploaded := 0, 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/v1.0/oauth2/accessToken":
						writeProviderJSON(t, w, map[string]any{"accessToken": "token", "expireIn": 7200})
					case "/media/upload":
						assertUpload(t, r, "media")
						if r.FormValue("type") != format || r.URL.Query().Get("access_token") != "token" {
							t.Error("wrong upload kind")
						}
						uploaded++
						writeProviderJSON(t, w, map[string]any{"errcode": 0, "type": format, "media_id": "version-id"})
					case "/v1.0/robot/groupMessages/send", "/v1.0/robot/oToMessages/batchSend":
						var body struct {
							Key   string   `json:"msgKey"`
							Param string   `json:"msgParam"`
							Users []string `json:"userIds"`
							Chat  string   `json:"openConversationId"`
						}
						var fields map[string]string
						if json.NewDecoder(r.Body).Decode(&body) != nil || json.Unmarshal([]byte(body.Param), &fields) != nil {
							t.Error("invalid send")
							return
						}
						if kind == "direct" && (len(body.Users) != 1 || body.Users[0] != "staff" || body.Chat != "") || kind == "group" && (body.Chat != "group" || len(body.Users) != 0) {
							t.Error("wrong target", body)
						}
						if format == "image" && (body.Key != "sampleImageMsg" || fields["photoURL"] != "version-id") || format == "file" && (body.Key != "sampleFile" || fields["mediaId"] != "version-id" || fields["fileName"] != "report.csv" || fields["fileType"] != "csv") {
							t.Error("wrong content", body)
						}
						sent++
						writeProviderJSON(t, w, map[string]string{"processQueryKey": "receipt"})
					default:
						t.Error("unexpected endpoint", r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				defer srv.Close()
				endpoint, err := url.Parse(srv.URL)
				if err != nil {
					t.Fatal(err)
				}
				client := srv.Client()
				client.Transport = localDingUpload{endpoint, client.Transport}
				b, _ := dingFixture()
				b.BotOpenID = "robot"
				b.TenantID = "corp"
				d := Delivery{Format: format, ArtifactID: "result", ChatType: kind, ChatID: "group", Media: &deliveryMedia{Name: "report.csv", Data: []byte("frozen bytes")}}
				if kind == "direct" {
					d.RecipientID = "staff"
				}
				got := sendDingTalkWithHTTP(context.Background(), b, d, dingHTTP{client: client, origin: srv.URL})
				if got.Status != "accepted" || sent != 1 || uploaded != 1 {
					t.Fatal(got, sent, uploaded)
				}
			})
		}
	}
}
func TestWecomNativeMediaAcknowledgements(t *testing.T) {
	for _, format := range []string{"file", "image"} {
		t.Run(format, func(t *testing.T) {
			f := setupWecom(t, true, false)
			f.push(t, wecomIncoming("message", "callback", "hi"))
			<-f.inbound
			d := Delivery{BindingID: f.b.ID, Generation: 1, ChatID: "user", Format: format, ArtifactID: "result", Media: &deliveryMedia{Name: "report.csv", Data: []byte("frozen bytes")}}
			cmd := "aibot_send_msg"
			if format == "image" {
				d.ReplyToMessageID = "message"
				cmd = "aibot_respond_msg"
			}
			result := make(chan deliveryReceipt, 1)
			go func() { result <- sendWeComDelivery(f.ctx, f.b, d) }()
			ack := func(frame wecomFrame, body any) {
				f.push(t, map[string]any{"headers": frame.Headers, "errcode": 0, "body": body})
			}
			init := nextWecomFrame(t, f, "aibot_upload_media_init")
			var meta struct {
				Type     string
				Filename string
				Chunks   int `json:"total_chunks"`
			}
			if json.Unmarshal(init.Body, &meta) != nil || meta.Type != format || meta.Filename != "report.csv" || meta.Chunks != 1 {
				t.Fatal("wrong upload metadata", string(init.Body))
			}
			ack(init, map[string]string{"upload_id": "upload"})
			chunk := nextWecomFrame(t, f, "aibot_upload_media_chunk")
			var part struct {
				Data   []byte `json:"base64_data"`
				Upload string `json:"upload_id"`
			}
			if json.Unmarshal(chunk.Body, &part) != nil || string(part.Data) != "frozen bytes" || part.Upload != "upload" {
				t.Fatal("wrong uploaded bytes")
			}
			ack(chunk, nil)
			ack(nextWecomFrame(t, f, "aibot_upload_media_finish"), map[string]string{"media_id": "remote-media", "type": format})
			frame := nextWecomFrame(t, f, cmd)
			var body map[string]json.RawMessage
			var media struct {
				ID string `json:"media_id"`
			}
			if json.Unmarshal(frame.Body, &body) != nil || json.Unmarshal(body[format], &media) != nil || media.ID != "remote-media" || jsonString(body["msgtype"]) != format || cmd == "aibot_send_msg" && jsonString(body["chatid"]) != "user" || cmd == "aibot_respond_msg" && frame.Headers.ReqID != "callback" {
				t.Fatal("wrong destination", string(frame.Body))
			}
			select {
			case <-result:
				t.Fatal("upload receipt treated as message acknowledgement")
			default:
			}
			ack(frame, nil)
			if r := <-result; r.Status != "sent" || r.EvidenceType != "business_ack" {
				t.Fatal(r)
			}
		})
	}
}
