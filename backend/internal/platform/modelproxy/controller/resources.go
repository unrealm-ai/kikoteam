package controller

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	mp "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Operation func(*http.Request, string) (any, int, error)

type ResourceAPI struct {
	Official *mp.Models
	Wrap     func(string, Operation) http.HandlerFunc
	Decode   func(*http.Request, any) error
	Paginate func(map[string][]string, int) (int, int, error)
}

func (s ResourceAPI) Register(m *http.ServeMux) {
	for route, handler := range map[string]Operation{
		"GET /api/v1/platform-models":                      s.officialList,
		"POST /api/v1/platform-models/test":                s.officialTest,
		"POST /api/v1/platform-models":                     s.officialSave,
		"PATCH /api/v1/platform-models/{model_id}":         s.officialSave,
		"GET /api/v1/accounts/{account_id}/credit-wallet":  s.creditWallet,
		"GET /api/v1/accounts/{account_id}/credit-ledger":  s.creditList,
		"POST /api/v1/accounts/{account_id}/credit-grants": s.creditGrant,
		"GET /api/v1/credit-settings":                      s.creditSettings,
		"PATCH /api/v1/credit-settings":                    s.creditSettings,
		"GET /api/v1/model-usage":                          s.creditList,
		"POST /api/v1/model-usage/{usage_id}/resolve":      s.usageResolve,
	} {
		m.HandleFunc(route, s.Wrap(route, handler))
	}
}

func invalidCreditRequest() error {
	return service.Fail("invalid_request", 400, "模型或积分参数无效")
}

func (s ResourceAPI) officialList(r *http.Request, actor string) (any, int, error) {
	p, n, e := s.Paginate(r.URL.Query(), 20)
	if e != nil {
		return nil, 0, e
	}
	if p > 1000000 || n > 100 {
		return nil, 0, invalidCreditRequest()
	}
	v, e := s.Official.Store.Models(r.Context(), actor, p, n)
	return v, 200, e
}

func (s ResourceAPI) officialTest(r *http.Request, actor string) (any, int, error) {
	var p struct {
		proxy.ModelPatch
		ID string `json:"id,omitempty"`
	}
	if e := s.Decode(r, &p); e != nil {
		return nil, 0, e
	}
	if p.ID != "" && !repo.UUID(p.ID) {
		return nil, 0, invalidCreditRequest()
	}
	v, e := s.Official.Test(r.Context(), actor, p.ID, p.ModelPatch)
	return v, 200, e
}

func (s ResourceAPI) officialSave(r *http.Request, actor string) (any, int, error) {
	var p proxy.ModelPatch
	if e := s.Decode(r, &p); e != nil {
		return nil, 0, e
	}
	id := r.PathValue("model_id")
	if (id == "") != (p.Version == 0) || p.Version < 0 {
		return nil, 0, invalidCreditRequest()
	}
	m, e := s.Official.Save(r.Context(), actor, id, p)
	if id == "" {
		return m, 201, e
	}
	return m, 200, e
}

func (s ResourceAPI) creditWallet(r *http.Request, actor string) (any, int, error) {
	v, e := s.Official.Store.Wallet(r.Context(), actor, r.PathValue("account_id"))
	return v, 200, e
}

func (s ResourceAPI) creditSettings(r *http.Request, actor string) (any, int, error) {
	if r.Method == "GET" {
		v, e := s.Official.Store.Settings(r.Context(), actor)
		return v, 200, e
	}
	var p repo.Settings
	if e := s.Decode(r, &p); e != nil {
		return nil, 0, e
	}
	n, e := proxy.ParseAmount(p.Bonus)
	if e != nil || p.Version < 1 {
		return nil, 0, invalidCreditRequest()
	}
	v, e := s.Official.Store.UpdateSettings(r.Context(), actor, p.Version, n)
	return v, 200, e
}

func (s ResourceAPI) creditGrant(r *http.Request, actor string) (any, int, error) {
	var p struct {
		Amount string `json:"amount"`
		Reason string `json:"reason"`
	}
	if e := s.Decode(r, &p); e != nil {
		return nil, 0, e
	}
	keys := r.Header.Values("Idempotency-Key")
	n, e := proxy.ParseAmount(p.Amount)
	if len(keys) != 1 || strings.TrimSpace(keys[0]) == "" || len(keys[0]) > 128 || e != nil || n <= 0 || !mp.ValidReason(p.Reason) {
		return nil, 0, invalidCreditRequest()
	}
	v, replay, e := s.Official.Store.Grant(r.Context(), actor, r.PathValue("account_id"), keys[0], strings.TrimSpace(p.Reason), n)
	if replay {
		return v, 200, e
	}
	return v, 201, e
}

func (s ResourceAPI) usageResolve(r *http.Request, actor string) (any, int, error) {
	var p struct {
		Action string          `json:"action"`
		Reason string          `json:"reason"`
		Usage  json.RawMessage `json:"usage,omitempty"`
	}
	if e := s.Decode(r, &p); e != nil {
		return nil, 0, e
	}
	if !mp.ValidReason(p.Reason) {
		return nil, 0, invalidCreditRequest()
	}
	var u *proxy.Usage
	switch p.Action {
	case "settle":
		var e error
		u, e = mp.ValidUsage(p.Usage)
		if e != nil {
			return nil, 0, invalidCreditRequest()
		}
	case "no_charge":
		if len(p.Usage) > 0 {
			return nil, 0, invalidCreditRequest()
		}
	default:
		return nil, 0, invalidCreditRequest()
	}
	e := s.Official.Store.Resolve(r.Context(), actor, r.PathValue("usage_id"), strings.TrimSpace(p.Reason), u)
	return nil, 204, e
}

func (s ResourceAPI) creditList(r *http.Request, actor string) (any, int, error) {
	q := r.URL.Query()
	ledger := r.PathValue("account_id") != ""
	f, e := creditFilters(q, ledger)
	if e != nil {
		return nil, 0, e
	}
	if ledger {
		f.Account = r.PathValue("account_id")
	} else if f.Account == "" && q.Get("all") != "true" {
		f.Account = actor
	}
	limit := 20
	if raw := q.Get("limit"); raw != "" {
		limit, e = strconv.Atoi(raw)
		if e != nil || limit < 1 || limit > 100 {
			return nil, 0, invalidCreditRequest()
		}
	}
	v, e := s.Official.Store.ListCredits(r.Context(), actor, f, q.Get("cursor"), limit, ledger)
	return v, 200, e
}

func creditFilters(q url.Values, ledger bool) (f repo.Filter, e error) {
	for name, values := range q {
		if len(values) != 1 {
			return f, invalidCreditRequest()
		}
		v := values[0]
		switch name {
		case "account_id", "agent_id", "model_id":
			if !repo.UUID(v) || ledger && name == "account_id" {
				return f, invalidCreditRequest()
			}
			switch name {
			case "account_id":
				f.Account = strings.ToLower(v)
			case "agent_id":
				f.Agent = v
			default:
				f.Model = v
			}
		case "from", "to":
			at, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return f, invalidCreditRequest()
			}
			at = at.UTC()
			if name == "from" {
				f.From = &at
			} else {
				f.To = &at
			}
		case "kind":
			if !ledger || v != "signup_bonus" && v != "admin_grant" && v != "model_charge" {
				return f, invalidCreditRequest()
			}
			f.Kind = v
		case "status":
			if ledger || !validUsageStatus(v) {
				return f, invalidCreditRequest()
			}
			f.Status = v
		case "all":
			if ledger || v != "true" {
				return f, invalidCreditRequest()
			}
		case "cursor", "limit":
			if len(v) > 512 {
				return f, invalidCreditRequest()
			}
		default:
			return f, invalidCreditRequest()
		}
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) || q.Get("all") != "" && f.Account != "" {
		return f, invalidCreditRequest()
	}
	return f, nil
}

func validUsageStatus(v string) bool {
	switch v {
	case "running", "usage_recorded", "settled", "pending_review", "no_charge":
		return true
	}
	return false
}
