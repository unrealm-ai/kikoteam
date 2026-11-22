// Package modelproxy contains the shared model proxy and credit types.
package modelproxy

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

const Units int64 = 1000000

var ErrChargeRange = errors.New("credit charge exceeds storage range")

type Price struct {
	Input      string  `json:"input"`
	Output     string  `json:"output"`
	CacheRead  *string `json:"cache_read"`
	CacheWrite *string `json:"cache_write"`
}

type Rates struct {
	Name       string `json:"model_name"`
	Input      string `json:"input_units"`
	Output     string `json:"output_units"`
	CacheRead  string `json:"cache_read_units"`
	CacheWrite string `json:"cache_write_units"`
}

type Model struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Protocol   string     `json:"protocol"`
	BaseURL    string     `json:"base_url"`
	UpstreamID string     `json:"upstream_model_id"`
	Prices     Price      `json:"prices"`
	HasKey     bool       `json:"has_key"`
	Enabled    bool       `json:"enabled"`
	Default    bool       `json:"is_default"`
	Validated  bool       `json:"validated"`
	Version    int64      `json:"version"`
	TestedAt   *time.Time `json:"tested_at"`
	Credential []byte     `json:"-"`
}

type ModelPatch struct {
	Version    int64   `json:"version,omitempty"`
	Name       *string `json:"name,omitempty"`
	Protocol   *string `json:"protocol,omitempty"`
	BaseURL    *string `json:"base_url,omitempty"`
	UpstreamID *string `json:"upstream_model_id,omitempty"`
	APIKey     *string `json:"api_key,omitempty"`
	Prices     *Price  `json:"prices,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
	Default    *bool   `json:"is_default,omitempty"`
	Receipt    string  `json:"test_receipt,omitempty"`
}

type Usage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_tokens"`
	CacheWrite int64 `json:"cache_write_tokens"`
}

type Call struct {
	ID    string
	Model Model
}

func Amount(v int64) string {
	n := big.NewInt(v)
	n.Abs(n)
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(n, big.NewInt(Units), fraction)
	sign := ""
	if v < 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s%s.%06d", sign, whole.String(), fraction.Int64())
}

func ParseAmount(s string) (int64, error) {
	whole, fraction, point := strings.Cut(s, ".")
	if whole == "" || len(s) > 26 || len(fraction) > 6 || point && fraction == "" {
		return 0, errors.New("invalid credit amount")
	}
	for _, r := range whole + fraction {
		if r < '0' || r > '9' {
			return 0, errors.New("invalid credit amount")
		}
	}
	return strconv.ParseInt(whole+fraction+strings.Repeat("0", 6-len(fraction)), 10, 64)
}

func (p Price) Values() ([4]*int64, error) {
	var out [4]*int64
	for i, s := range []*string{&p.Input, &p.Output, p.CacheRead, p.CacheWrite} {
		if s == nil {
			continue
		}
		n, err := ParseAmount(*s)
		if err != nil {
			return out, err
		}
		out[i] = &n
	}
	return out, nil
}

func (m Model) Snapshot() (Rates, error) {
	p, e := m.Prices.Values()
	if e != nil {
		return Rates{}, e
	}
	for i := 2; i < 4; i++ {
		if p[i] == nil {
			p[i] = p[0]
		}
	}
	return Rates{m.Name, strconv.FormatInt(*p[0], 10), strconv.FormatInt(*p[1], 10), strconv.FormatInt(*p[2], 10), strconv.FormatInt(*p[3], 10)}, nil
}

func (u Usage) Cost(r Rates) (int64, error) {
	total := new(big.Int)
	for i, tokens := range []int64{u.Input, u.Output, u.CacheRead, u.CacheWrite} {
		price, e := strconv.ParseInt([]string{r.Input, r.Output, r.CacheRead, r.CacheWrite}[i], 10, 64)
		if e != nil || tokens < 0 || price < 0 {
			return 0, errors.New("invalid billable usage")
		}
		total.Add(total, new(big.Int).Mul(big.NewInt(tokens), big.NewInt(price)))
	}
	total.Add(total, big.NewInt(Units-1)).Quo(total, big.NewInt(Units))
	if !total.IsInt64() {
		return 0, ErrChargeRange
	}
	return total.Int64(), nil
}
