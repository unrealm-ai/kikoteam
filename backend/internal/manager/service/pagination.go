package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type cursor struct {
	Scope string    `json:"scope"`
	Time  time.Time `json:"time"`
	ID    string    `json:"id"`
	Seq   int64     `json:"seq,omitempty"`
	High  int64     `json:"high,omitempty"`
	Kind  string    `json:"kind,omitempty"`
	Inner string    `json:"inner,omitempty"`
}

func (s *Service) encodeCursor(v cursor) (string, error) {
	if len(s.CursorSecret) < 32 {
		return "", Fail("configuration_unusable", 503, "游标签名配置无效")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, s.CursorSecret)
	if _, err = m.Write(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}
func (s *Service) decodeCursor(raw, scope string) (cursor, error) {
	var v cursor
	bad := Fail("invalid_cursor", 400, "分页游标无效或范围不匹配")
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || len(raw) > 4096 {
		return v, bad
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return v, bad
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return v, bad
	}
	m := hmac.New(sha256.New, s.CursorSecret)
	if _, err = m.Write(b); err != nil {
		return v, err
	}
	if len(s.CursorSecret) < 32 || !hmac.Equal(sig, m.Sum(nil)) || json.Unmarshal(b, &v) != nil || v.Scope != scope {
		return v, bad
	}
	return v, nil
}
