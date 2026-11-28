package videogen

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	mp "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const Key = "video-generation"

var pluginID = service.CatalogID("plugin", Key)

type Service struct {
	repo.Store
	Secrets mp.Secrets
	HTTP    *http.Client
	Root    string
	Notify  func(context.Context, *sql.Tx, string, string) error
}
type Settings struct {
	Version    int64  `json:"version"`
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"credential_configured"`
	BaseURL    string `json:"base_url"`
	Model      string `json:"upstream_model_id"`
	Price      string `json:"credits_per_second"`
	Daily      int    `json:"workspace_daily_seconds"`
	Concurrent int    `json:"max_concurrent_generations"`
	config     int64
	cipher     []byte
}
type Patch struct {
	Version    *int64  `json:"version"`
	Enabled    *bool   `json:"enabled"`
	BaseURL    *string `json:"base_url"`
	Model      *string `json:"upstream_model_id"`
	Price      *string `json:"credits_per_second"`
	Daily      *int    `json:"workspace_daily_seconds"`
	Concurrent *int    `json:"max_concurrent_generations"`
	Key        *string `json:"api_key"`
	Receipt    string  `json:"receipt"`
}

func config(ctx context.Context, db repo.RowReader, lock bool) (v Settings, err error) {
	if lock {
		var ver int64
		if err = db.QueryRowContext(ctx, "SELECT version FROM platform_default_plugin_settings WHERE plugin_id=$1 FOR UPDATE", pluginID).Scan(&ver); err != nil {
			return
		}
	}
	var price int64
	err = db.QueryRowContext(ctx, `SELECT s.version,s.enabled AND p.listed,coalesce(c.version,0),coalesce(c.base_url,'https://api.nbility.ai'),coalesce(c.upstream_model_id,'MiniMax-H3'),coalesce(c.unit_price_units,1000000),coalesce(c.workspace_daily_seconds,300),coalesce(c.max_concurrent_generations,4),c.credential_ciphertext
 FROM platform_default_plugin_settings s JOIN platform_plugins p ON p.id=s.plugin_id LEFT JOIN platform_video_generation_configs c ON c.plugin_id=s.plugin_id AND c.version=s.active_config_version WHERE s.plugin_id=$1 AND p.enabled AND p.archived_at IS NULL`, pluginID).Scan(&v.Version, &v.Enabled, &v.config, &v.BaseURL, &v.Model, &price, &v.Daily, &v.Concurrent, &v.cipher)
	v.Configured = len(v.cipher) > 0
	v.Price = proxy.Amount(price)
	return
}
func (s *Service) Settings(ctx context.Context, actor string) (Settings, error) {
	if err := repo.Admin(ctx, s.DB, actor, false); err != nil {
		return Settings{}, err
	}
	return config(ctx, s.DB, false)
}
func (s *Service) candidate(v Settings, p Patch) (Settings, string, error) {
	old := v.BaseURL
	if p.BaseURL != nil {
		v.BaseURL = strings.TrimRight(strings.TrimSpace(*p.BaseURL), "/")
	}
	if p.Model != nil {
		v.Model = strings.TrimSpace(*p.Model)
	}
	if p.Price != nil {
		v.Price = *p.Price
	}
	if p.Daily != nil {
		v.Daily = *p.Daily
	}
	if p.Concurrent != nil {
		v.Concurrent = *p.Concurrent
	}
	if p.Enabled != nil {
		v.Enabled = *p.Enabled
	}
	u, e := url.Parse(v.BaseURL)
	price, pe := proxy.ParseAmount(v.Price)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		v.Model == "" || utf8.RuneCountInString(v.Model) > 200 || strings.IndexFunc(v.Model, unicode.IsControl) >= 0 ||
		pe != nil || price < 1 || price > math.MaxInt64/15 || v.Daily < 1 || v.Daily > 1000000 || v.Concurrent < 1 || v.Concurrent > 1000 {
		return v, "", service.Fail("invalid_video_configuration", 400, "请检查 HTTPS 服务根地址、型号、积分和限额")
	}
	v.Price = proxy.Amount(price)
	if p.Key != nil {
		key := strings.TrimSpace(*p.Key)
		if key != "" && len(key) <= 8192 && !strings.ContainsAny(key, "\r\n") {
			return v, key, nil
		}
	}
	if p.Key != nil || old != v.BaseURL || !v.Configured {
		return v, "", service.Fail("credential_required", 400, "请填写当前地址的 API Key")
	}
	key, e := s.Secrets.Open("video-provider", pluginID, v.cipher)
	return v, key, e
}
func (s *Service) receipt(actor string, v Settings, key, stamp string) string {
	mac := hmac.New(sha256.New, s.Secrets.Root)
	for _, field := range []string{"video-test-v1", actor, strconv.FormatInt(v.Version, 10), v.BaseURL, v.Model, key, stamp} {
		mac.Write([]byte(field))
		mac.Write([]byte{0})
	}
	return stamp + "." + hex.EncodeToString(mac.Sum(nil))
}
func (s *Service) Test(ctx context.Context, actor string, p Patch) (any, error) {
	old, err := s.Settings(ctx, actor)
	if err != nil {
		return nil, err
	}
	if p.Version != nil && *p.Version != old.Version {
		return nil, service.Fail("version_conflict", 409, "配置已变化，请刷新后测试")
	}
	v, key, err := s.candidate(old, p)
	if err != nil {
		return nil, err
	}
	args := Arguments{Prompt: "A white ceramic cup on a wooden table, stationary camera.", Ratio: "landscape", Seconds: 1}
	id, err := s.submit(ctx, v, key, args)
	if err != nil {
		return nil, err
	}
	tick := time.NewTicker(6 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, service.Fail("video_test_timeout", 504, "测试等待超时，上游可能已受理")
		case <-tick.C:
			u, e := s.poll(ctx, v.BaseURL, key, id)
			if e != nil {
				continue
			}
			switch u.Status {
			case "failed":
				return nil, rejected
			case "completed":
				b, e := s.download(ctx, u)
				if e != nil {
					return nil, e
				}
				if e = inspect(ctx, b, args.Ratio, args.Seconds); e != nil {
					return nil, e
				}
				expiry := time.Now().Add(10 * time.Minute)
				return map[string]any{
					"receipt":    s.receipt(actor, v, key, strconv.FormatInt(expiry.Unix(), 10)),
					"expires_at": expiry, "size_bytes": len(b), "preview_mp4": b,
				}, nil
			}
		}
	}
}
func (s *Service) Save(ctx context.Context, actor string, p Patch) (out Settings, err error) {
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		if e := repo.Admin(ctx, tx, actor, true); e != nil {
			return e
		}
		old, e := config(ctx, tx, true)
		if e != nil {
			return e
		}
		if p.Version == nil || *p.Version != old.Version {
			return service.Fail("version_conflict", 409, "配置已变化，请刷新")
		}
		if p.Enabled != nil && !*p.Enabled && p.Key == nil && p.Model == nil && p.BaseURL == nil && p.Price == nil && p.Concurrent == nil && p.Daily == nil {
			_, e = tx.ExecContext(ctx, "UPDATE platform_default_plugin_settings SET enabled=false,version=version+1,updated_by=$2,updated_at=now() WHERE plugin_id=$1", pluginID, actor)
		} else {
			v, key, e := s.candidate(old, p)
			if e != nil {
				return e
			}
			if !old.Configured || p.Key != nil || v.BaseURL != old.BaseURL || v.Model != old.Model {
				stamp, _, _ := strings.Cut(p.Receipt, ".")
				ts, err := strconv.ParseInt(stamp, 10, 64)
				if err != nil || ts < time.Now().Unix() || ts > time.Now().Add(10*time.Minute).Unix() || !hmac.Equal([]byte(p.Receipt), []byte(s.receipt(actor, v, key, stamp))) {
					return service.Fail("video_test_required", 409, "请先测试当前视频配置")
				}
			}
			secret, e := s.Secrets.Seal("video-provider", pluginID, key)
			if e != nil {
				return e
			}
			price, e := proxy.ParseAmount(v.Price)
			if e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, `INSERT INTO platform_video_generation_configs(plugin_id,version,base_url,upstream_model_id,credential_ciphertext,unit_price_units,max_concurrent_generations,workspace_daily_seconds,created_by)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, pluginID, v.Version+1, v.BaseURL, v.Model, secret, price, v.Concurrent, v.Daily, actor)
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE platform_default_plugin_settings SET enabled=$2,version=version+1,active_config_version=version+1,updated_by=$3,updated_at=now() WHERE plugin_id=$1`, pluginID, v.Enabled, actor); e != nil {
				return e
			}
		}
		if e != nil {
			return e
		}
		out, e = config(ctx, tx, false)
		return e
	})
	return
}

func missing(err error) bool { return errors.Is(err, sql.ErrNoRows) }
