// Package imagegen owns platform-managed image generation and its per-image charges.
package imagegen

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/defaultplugins"
	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	mp "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const Key = "image-generation"
const base64Provider = "openai_images_b64_json"

var pluginID = service.CatalogID("plugin", Key)

type Service struct {
	Logger *slog.Logger
	repo.Store
	Secrets mp.Secrets
	HTTP    *http.Client
	Root    string
}
type Settings struct {
	sizes          map[string]string
	listed         bool
	Key            string `json:"key"`
	Name           string `json:"name"`
	ReleaseVersion int64  `json:"release_version"`
	Version        int64  `json:"version"`
	Enabled        bool   `json:"enabled"`
	Provider       string `json:"provider"`
	BaseURL        string `json:"base_url"`
	Model          string `json:"upstream_model_id"`
	Price          string `json:"credits_per_image"`
	Limit          int    `json:"workspace_daily_image_limit"`
	Concurrent     int    `json:"max_concurrent_generations"`
	Timeout        int    `json:"timeout_seconds"`
	Configured     bool   `json:"credential_configured"`
	ConfigVersion  int64  `json:"active_config_version"`
	cipher         []byte
}
type Patch struct {
	Version    *int64  `json:"version"`
	Enabled    *bool   `json:"enabled"`
	Provider   *string `json:"provider"`
	BaseURL    *string `json:"base_url"`
	Model      *string `json:"upstream_model_id"`
	Price      *string `json:"credits_per_image"`
	Limit      *int    `json:"workspace_daily_image_limit"`
	Concurrent *int    `json:"max_concurrent_generations"`
	Timeout    *int    `json:"timeout_seconds"`
	APIKey     *string `json:"api_key"`
	Receipt    string  `json:"receipt"`
}

func config(ctx context.Context, db repo.RowReader, lock bool) (x Settings, err error) {
	query := `SELECT p.builtin_key,p.name,v.version,s.version,s.enabled,COALESCE(c.version,0),COALESCE(c.provider,'openai_images'),
 COALESCE(c.base_url,'https://api.openai.com/v1'),COALESCE(c.upstream_model_id,''),COALESCE(c.unit_price_units,5000000),
 COALESCE(c.workspace_daily_image_limit,100),COALESCE(c.max_concurrent_generations,8),COALESCE(c.timeout_seconds,120),c.credential_ciphertext,p.listed,c.supported_sizes
 FROM platform_default_plugin_settings s JOIN platform_plugins p ON p.id=s.plugin_id JOIN platform_plugin_versions v ON v.id=p.current_version_id
 LEFT JOIN platform_image_generation_config_versions c ON c.plugin_id=s.plugin_id AND c.version=s.active_config_version
 WHERE s.plugin_id=$1 AND p.workspace_id IS NULL AND p.enabled AND p.archived_at IS NULL AND v.distribution='all_agents' AND v.handler_key='image_generation'`
	if lock {
		var locked int64
		// A separate read after acquiring the lock sees the matching immutable config
		// even if a concurrent publisher changed the pointer while we were waiting.
		if e := db.QueryRowContext(ctx, "SELECT version FROM platform_default_plugin_settings WHERE plugin_id=$1 FOR UPDATE", pluginID).Scan(&locked); e != nil {
			return x, e
		}
	}
	var price int64
	var sizes []byte
	err = db.QueryRowContext(ctx, query, pluginID).Scan(&x.Key, &x.Name, &x.ReleaseVersion, &x.Version, &x.Enabled, &x.ConfigVersion, &x.Provider, &x.BaseURL, &x.Model, &price, &x.Limit, &x.Concurrent, &x.Timeout, &x.cipher, &x.listed, &sizes)
	if errors.Is(err, sql.ErrNoRows) {
		err = service.Fail("catalog_unavailable", 503, "官方文生图目录尚未就绪")
	}
	x.sizes = imageSizes
	if err == nil && len(sizes) > 0 {
		var saved map[string]string
		if err = json.Unmarshal(sizes, &saved); err != nil {
			return x, err
		}
		x.sizes = saved
	}
	x.Configured = len(x.cipher) > 0
	x.Price = proxy.Amount(price)
	return
}
func (s *Service) Settings(ctx context.Context, actor string) (Settings, error) {
	if err := repo.Admin(ctx, s.DB, actor, false); err != nil {
		return Settings{}, err
	}
	return config(ctx, s.DB, false)
}
func (s *Service) Directory(ctx context.Context, actor string) ([]defaultplugins.Summary, error) {
	if err := repo.Admin(ctx, s.DB, actor, false); err != nil {
		return nil, err
	}
	return defaultplugins.List(ctx, s.DB, "", "")
}

func invalid() error {
	return service.Fail("invalid_image_configuration", 400, "文生图配置无效，请检查 HTTPS 地址、模型、单价及限额")
}
func (s *Service) candidate(old Settings, p Patch) (Settings, string, error) {
	x := old
	if p.Enabled != nil {
		x.Enabled = *p.Enabled
	}
	if p.Provider != nil {
		x.Provider = *p.Provider
	}
	if p.BaseURL != nil {
		x.BaseURL = strings.TrimRight(strings.TrimSpace(*p.BaseURL), "/")
	}
	if p.Model != nil {
		x.Model = strings.TrimSpace(*p.Model)
	}
	if p.Price != nil {
		x.Price = *p.Price
	}
	if p.Limit != nil {
		x.Limit = *p.Limit
	}
	if p.Concurrent != nil {
		x.Concurrent = *p.Concurrent
	}
	if p.Timeout != nil {
		x.Timeout = *p.Timeout
	}
	u, e := url.Parse(x.BaseURL)
	price, pe := proxy.ParseAmount(x.Price)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(x.BaseURL) > 2048 || (x.Provider != "openai_images" && x.Provider != base64Provider) || x.Model == "" || len(x.Model) > 200 || pe != nil || price <= 0 || x.Concurrent < 1 || x.Limit < 1 || x.Limit > 1000000 || x.Concurrent > 1000 || x.Timeout < 1 || x.Timeout > 300 {
		return x, "", invalid()
	}
	x.Price = proxy.Amount(price)
	if p.APIKey != nil {
		key := strings.TrimSpace(*p.APIKey)
		if key == "" || len(key) > 8192 || strings.ContainsAny(key, "\r\n") {
			return x, "", invalid()
		}
		return x, key, nil
	}
	if x.BaseURL != old.BaseURL || !old.Configured {
		return x, "", service.Fail("credential_required", 400, "请为当前服务地址填写 API Key")
	}
	key, e := s.Secrets.Open("image-provider", pluginID, old.cipher)
	return x, key, e
}
func (s *Service) receipt(actor string, x Settings, key, stamp string) string {
	mac := hmac.New(sha256.New, s.Secrets.Root)
	fields := []string{"image-test-v1", actor, strconv.FormatInt(x.Version, 10), x.BaseURL, x.Model, x.Provider, key, stamp}
	for _, v := range fields {
		mac.Write([]byte(v))
		mac.Write([]byte{0})
	}
	return stamp + "." + hex.EncodeToString(mac.Sum(nil))
}
func (s *Service) verified(actor string, x Settings, key, receipt string) bool {
	stamp, _, ok := strings.Cut(receipt, ".")
	t, e := strconv.ParseInt(stamp, 10, 64)
	return ok && e == nil && t >= time.Now().Unix() && t <= time.Now().Add(10*time.Minute).Unix() && hmac.Equal([]byte(receipt), []byte(s.receipt(actor, x, key, stamp)))
}
func (s *Service) Test(ctx context.Context, actor string, p Patch) (any, error) {
	old, e := s.Settings(ctx, actor)
	if e != nil {
		return nil, e
	}
	if p.Version != nil && *p.Version != old.Version {
		return nil, service.Fail("version_conflict", 409, "配置已变化，请刷新后测试")
	}
	x, key, e := s.candidate(old, p)
	if e != nil {
		return nil, e
	}
	img, _, e := s.generate(ctx, x, key, "A single green leaf on a plain background", "square", service.NewID())
	if e != nil {
		return nil, e
	}
	preview, e := makePreview(img)
	if e != nil {
		return nil, e
	}
	expiry := time.Now().Add(10 * time.Minute)
	return map[string]any{"preview_png": preview, "receipt": s.receipt(actor, x, key, strconv.FormatInt(expiry.Unix(), 10)), "expires_at": expiry}, nil
}
func (s *Service) Save(ctx context.Context, actor string, p Patch) (Settings, error) {
	var out Settings
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := repo.Admin(ctx, tx, actor, true); err != nil {
			return err
		}
		old, err := config(ctx, tx, true)
		if err != nil {
			return err
		}
		if p.Version == nil || *p.Version != old.Version {
			return service.Fail("version_conflict", 409, "配置已更新，请刷新后重试")
		}
		if p.Enabled != nil && !*p.Enabled && p.APIKey == nil && p.BaseURL == nil && p.Model == nil && p.Price == nil && p.Concurrent == nil && p.Limit == nil && p.Timeout == nil && p.Provider == nil {
			_, err = tx.ExecContext(ctx, `UPDATE platform_default_plugin_settings SET enabled=false,version=version+1,updated_by=$2,updated_at=now() WHERE plugin_id=$1`, pluginID, actor)
		} else {
			x, key, e := s.candidate(old, p)
			if e != nil {
				return e
			}
			changed := !old.Configured || x.Provider != old.Provider || x.BaseURL != old.BaseURL || x.Model != old.Model || p.APIKey != nil
			if changed && !s.verified(actor, x, key, p.Receipt) {
				return service.Fail("image_test_required", 409, "请先测试当前模型和凭据")
			}
			raw, e := s.Secrets.Seal("image-provider", pluginID, key)
			if e != nil {
				return e
			}
			price, e := proxy.ParseAmount(x.Price)
			if e != nil {
				return e
			}
			sizes, e := json.Marshal(x.sizes)
			if e != nil {
				return e
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO platform_image_generation_config_versions(plugin_id,version,provider,base_url,upstream_model_id,credential_ciphertext,unit_price_units,supported_sizes,max_concurrent_generations,workspace_daily_image_limit,timeout_seconds,validated_at,created_by)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,CASE WHEN $13 THEN now() ELSE (SELECT validated_at FROM platform_image_generation_config_versions WHERE plugin_id=$1 AND version=$14) END,$12)`, pluginID, old.Version+1, x.Provider, x.BaseURL, x.Model, raw, price, sizes, x.Concurrent, x.Limit, x.Timeout, actor, changed, old.ConfigVersion)
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE platform_default_plugin_settings SET enabled=$2,version=version+1,active_config_version=version+1,updated_by=$3,updated_at=now() WHERE plugin_id=$1`, pluginID, x.Enabled, actor)
			}
		}
		if err != nil {
			return err
		}
		out, err = config(ctx, tx, false)
		return err
	})
	return out, err
}
