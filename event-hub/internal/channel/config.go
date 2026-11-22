// Package channel owns elected channel connections, never business authorization.
package channel

import (
	"errors"
	"github.com/unrealm-ai/kikoteam/shared/trigger"
)

type Credentials struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}
type Trigger struct {
	Mention  bool     `json:"mention"`
	Keywords []string `json:"keywords"`
	Regexes  []string `json:"regexes"`
}
type History struct {
	Enabled bool `json:"enabled"`
	Limit   int  `json:"limit"`
}
type Binding struct {
	ID           string      `json:"id"`
	WorkspaceID  string      `json:"workspace_id"`
	AgentID      string      `json:"agent_id"`
	Provider     string      `json:"provider"`
	Generation   int64       `json:"connection_generation"`
	RulesVersion int64       `json:"rules_version"`
	Revision     int64       `json:"config_revision"`
	Enabled      bool        `json:"enabled"`
	AppID        string      `json:"app_id"`
	TenantID     string      `json:"external_tenant_id"`
	BotOpenID    string      `json:"bot_open_id"`
	Credentials  Credentials `json:"credentials"`
	Trigger      Trigger     `json:"trigger"`
	History      History     `json:"history"`
}
type Snapshot struct {
	Revision int64     `json:"snapshot_revision"`
	Items    []Binding `json:"items"`
}
type Applied struct {
	BotOpenID    string `json:"observed_bot_open_id,omitempty"`
	TenantID     string `json:"observed_tenant_id,omitempty"`
	Generation   int64  `json:"connection_generation"`
	RulesVersion int64  `json:"rules_version"`
	Revision     int64  `json:"config_revision"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code,omitempty"`
}

func status(b Binding, state, code string) Applied {
	return Applied{Generation: b.Generation, RulesVersion: b.RulesVersion, Revision: b.Revision, Status: state, ErrorCode: code}
}

type Filter struct {
	trigger  Trigger
	matchers []*trigger.Matcher
}

func Compile(t Trigger) (*Filter, error) {
	if len(t.Keywords) > 32 || len(t.Regexes) > 16 {
		return nil, errors.New("too many trigger rules")
	}
	f := &Filter{trigger: t}
	for i, list := range [][]string{t.Keywords, t.Regexes} {
		for _, p := range list {
			m, err := trigger.Compile([]string{"keyword", "regex"}[i], p)
			if err != nil {
				return nil, err
			}
			f.matchers = append(f.matchers, m)
		}
	}
	return f, nil
}

// Match never uses display names or textual @ markers as identity.
func (f *Filter) Match(chatType, text string, mentioned bool) (bool, error) {
	if err := trigger.ValidateText(text); err != nil {
		return false, err
	}
	if chatType == "p2p" {
		return true, nil
	}
	if chatType != "group" {
		return false, errors.New("unsupported chat type")
	}
	if f.trigger.Mention && mentioned {
		return true, nil
	}
	for _, m := range f.matchers {
		if _, matched := m.Match(text); matched {
			return true, nil
		}
	}
	return false, nil
}
