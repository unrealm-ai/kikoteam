package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

type peoplePageCursor struct {
	Scope  string `json:"scope"`
	Native string `json:"native"`
}

func peoplePageScope(w, actor, a string, q PeopleQuery) string {
	q.Cursor = ""
	q.SnapshotVersion = 0
	raw := []byte(strings.Join([]string{w, actor, a, q.Provider, q.Purpose, q.Kind, q.Q, q.ParentID, q.DepartmentID, strconv.Itoa(q.Limit), strconv.FormatInt(q.SnapshotVersion, 10)}, "\x00"))
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *PeopleSelectorService) openPageCursor(scope, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	bad := Fail("invalid_selector_cursor", 400, "分页游标或查询范围无效")
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || len(s.Secret) < 32 {
		return "", bad
	}
	data, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return "", bad
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return "", bad
	}
	h := hmac.New(sha256.New, s.Secret)
	_, _ = h.Write(data)
	var c peoplePageCursor
	if !hmac.Equal(sig, h.Sum(nil)) || json.Unmarshal(data, &c) != nil || c.Scope != scope {
		return "", bad
	}
	return c.Native, nil
}
func (s *PeopleSelectorService) sealPageCursor(scope string, native *string) (*string, error) {
	if native == nil {
		return nil, nil
	}
	if len(s.Secret) < 32 {
		return nil, Fail("selector_unavailable", 503, "选择器暂不可用")
	}
	raw, e := json.Marshal(peoplePageCursor{Scope: scope, Native: *native})
	if e != nil {
		return nil, e
	}
	h := hmac.New(sha256.New, s.Secret)
	_, _ = h.Write(raw)
	out := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	return &out, nil
}
