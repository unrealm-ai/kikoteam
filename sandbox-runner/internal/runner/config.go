// Package runner owns the bare Sandbox and one builtin process per task Session.
package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	BackendURL      string `json:"backend_url"`
	ConfigToken     string `json:"config_token"`
	ManagementToken string `json:"management_token"`
	SandboxID       string `json:"sandbox_id"`
	EngineID        string `json:"engine_id"`
	Root            string `json:"root"`
	Builtin         string `json:"builtin"`
}
type scope struct {
	ID             string         `json:"sandbox_id"`
	WorkspaceID    string         `json:"workspace_id"`
	AgentID        string         `json:"agent_id"`
	ConversationID string         `json:"conversation_id,omitempty"`
	TaskSpaceID    string         `json:"task_space_id,omitempty"`
	TaskID         string         `json:"task_id,omitempty"`
	EngineID       string         `json:"engine_id"`
	Lifecycle      string         `json:"lifecycle"`
	ReclaimAllowed bool           `json:"reclaim_allowed"`
	Sessions       []sessionScope `json:"sessions"`
}
type sessionScope struct {
	SessionID      string  `json:"session_id"`
	TaskID         string  `json:"task_id,omitempty"`
	TaskSpaceID    string  `json:"task_space_id,omitempty"`
	ConversationID string  `json:"conversation_id,omitempty"`
	AssignmentID   *string `json:"assignment_id"`
	Generation     int64   `json:"generation,string"`
	Lifecycle      string  `json:"lifecycle"`
	EngineID       *string `json:"engine_id"`
}

func uuid() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func token() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func validID(v string) bool {
	if len(v) != 36 {
		return false
	}
	for i, c := range v {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if (c < 'a' || c > 'f') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
func load(path string) (Config, error) {
	var c Config
	info, err := os.Stat(path)
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
		return c, errors.New("configuration must be private")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	u, err := url.Parse(c.BackendURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || !validID(c.SandboxID) || !validID(c.EngineID) || len(c.ConfigToken) < 32 || len(c.ManagementToken) < 32 || !filepath.IsAbs(c.Root) || !filepath.IsAbs(c.Builtin) {
		return c, errors.New("invalid Runner configuration")
	}
	return c, nil
}
func (s *server) fetch(ctx context.Context) (scope, error) {
	var v scope
	r, err := http.NewRequestWithContext(ctx, "GET", s.config.BackendURL+"/api/internal/v1/sandbox-instances/"+s.config.SandboxID+"/config?engine_id="+s.config.EngineID, nil)
	if err != nil {
		return v, err
	}
	r.Header.Set("Authorization", "Bearer "+s.config.ConfigToken)
	res, err := s.backend.Do(r)
	if err != nil {
		return v, err
	}
	defer func() { _ = res.Body.Close() }() //nolint:errcheck // Decode reports read errors; body is bounded.
	if res.StatusCode != 200 {
		return v, fmt.Errorf("configuration HTTP %d", res.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&v); err != nil {
		return v, err
	}
	if v.ID != s.config.SandboxID || v.EngineID != s.config.EngineID || !validID(v.WorkspaceID) || !validID(v.AgentID) || !v.validOwner() {
		return v, errors.New("configuration scope mismatch")
	}
	if s.scope.ID != "" && !s.scope.sameOwner(v) {
		return v, errors.New("configuration owner changed")
	}
	if err := v.validateSessions(); err != nil {
		return v, err
	}
	return v, nil
}
func writePrivate(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = os.WriteFile(path+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
func runtimeEnvironment() []string {
	out := []string{}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_PATH"} {
		if v, ok := os.LookupEnv(key); ok {
			out = append(out, key+"="+v)
		}
	}
	return out
}
func deadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
}
