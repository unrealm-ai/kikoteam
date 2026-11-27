// Package config loads immutable process configuration before serving requests.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Reference resolves a value from exactly one explicit source.
type Reference struct {
	Value string `json:"value"`
	Env   string `json:"env"`
	File  string `json:"file"`
}

type Config struct {
	Server struct {
		Origin         string `json:"public_origin"`
		Address        string `json:"address"`
		WebDir         string `json:"web_dir"`
		TrustedProxies string `json:"trusted_proxy_cidrs"`
	} `json:"server"`
	Database struct {
		URL Reference `json:"url"`
	} `json:"database"`
	Auth struct {
		Secret Reference `json:"secret"`
	} `json:"auth"`
	MCPPrivateCIDRs   []netip.Prefix `json:"-"`
	ModelProxyOrigin  string         `json:"-"`
	AllowHTTPIPOrigin bool           `json:"-"`
	DSN               string         `json:"-"`
	AuthSecret        string         `json:"-"`
}

// Load reads process configuration; explicit deployment environment values override file values.
func Load() (Config, error) {
	var c Config
	c.Server.Address = "127.0.0.1:8080"
	path, explicit := os.LookupEnv("KIKO_CONFIG")
	if !explicit {
		path = "backend.local.json"
	}
	if path == "" {
		return c, fmt.Errorf("KIKO_CONFIG must not be empty")
	}
	raw, err := os.ReadFile(path)
	filePresent := err == nil
	if err != nil && (explicit || !os.IsNotExist(err)) {
		return c, fmt.Errorf("cannot read configuration file")
	}
	if filePresent {
		if err = validateJSON(raw); err != nil {
			return c, err
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&c); err != nil {
			return c, fmt.Errorf("invalid configuration JSON or unknown field")
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			return c, fmt.Errorf("configuration must contain one JSON object")
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return c, fmt.Errorf("configuration must be an object")
		}
	}
	if filePresent && (c.Database.URL.Value != "" || c.Auth.Secret.Value != "") {
		st, statErr := os.Stat(path)
		if statErr != nil || st.Mode().Perm()&0077 != 0 {
			return c, fmt.Errorf("configuration with inline secrets must be private (0600)")
		}
	}
	override(&c.Server.Origin, "KIKO_PUBLIC_ORIGIN")
	if c.AllowHTTPIPOrigin, err = envBool("KIKO_ALLOW_HTTP_IP_ORIGIN"); err != nil {
		return c, err
	}
	override(&c.Server.Address, "KIKO_HTTP_ADDRESS")
	override(&c.Server.WebDir, "KIKO_WEB_DIR")
	override(&c.Server.TrustedProxies, "KIKO_TRUSTED_PROXY_CIDRS")
	c.ModelProxyOrigin = os.Getenv("KIKO_MODEL_PROXY_ORIGIN")
	if v, ok := os.LookupEnv("DATABASE_URL"); ok {
		c.Database.URL = Reference{Value: v}
	}
	if v, ok := os.LookupEnv("KIKO_AUTH_SECRET"); ok {
		c.Auth.Secret = Reference{Value: v}
	}

	base := filepath.Dir(path)
	if c.DSN, err = resolve(c.Database.URL, base, "database.url"); err != nil {
		return c, err
	}
	if c.AuthSecret, err = resolve(c.Auth.Secret, base, "auth.secret"); err != nil {
		return c, err
	}

	for _, raw := range strings.Split(os.Getenv("KIKO_MCP_PRIVATE_CIDRS"), ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		prefix, parseErr := netip.ParsePrefix(strings.TrimSpace(raw))
		if parseErr != nil {
			return c, fmt.Errorf("KIKO_MCP_PRIVATE_CIDRS contains an invalid CIDR")
		}
		c.MCPPrivateCIDRs = append(c.MCPPrivateCIDRs, prefix.Masked())
	}
	return c, nil
}

func override(dst *string, key string) {
	if v, ok := os.LookupEnv(key); ok {
		*dst = v
	}
}
func envBool(key string) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return b, nil
}
func resolve(r Reference, base, label string) (string, error) {
	n := 0
	for _, v := range []string{r.Value, r.Env, r.File} {
		if v != "" {
			n++
		}
	}
	if n > 1 {
		return "", fmt.Errorf("%s must select only one value/env/file source", label)
	}
	if r.Env != "" {
		v, ok := os.LookupEnv(r.Env)
		if !ok || v == "" {
			return "", fmt.Errorf("%s environment reference is missing or empty", label)
		}
		return v, nil
	}
	if r.File != "" {
		path := r.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
			return "", fmt.Errorf("%s secret file must be readable, regular and private (0600)", label)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%s secret file cannot be read", label)
		}
		v := strings.TrimRight(string(b), "\r\n")
		if v == "" {
			return "", fmt.Errorf("%s secret file is empty", label)
		}
		return v, nil
	}
	return r.Value, nil
}

// validateJSON rejects ambiguous policy input before decoding typed fields.
func validateJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var visit func() error
	visit = func() error {
		token, err := d.Token()
		if err != nil {
			return fmt.Errorf("invalid configuration JSON")
		}
		if token == nil {
			return fmt.Errorf("null configuration values are not allowed")
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return fmt.Errorf("invalid configuration JSON")
				}
				name, valid := key.(string)
				name = strings.ToLower(name)
				if !valid || seen[name] {
					return fmt.Errorf("duplicate configuration key")
				}
				seen[name] = true
				if e = visit(); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e := visit(); e != nil {
					return e
				}
			}
		}
		if _, e := d.Token(); e != nil {
			return fmt.Errorf("invalid configuration JSON")
		}
		return nil
	}
	return visit()
}
