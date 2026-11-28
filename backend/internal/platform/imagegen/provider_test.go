package imagegen

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerationPayload(t *testing.T) {
	for _, tc := range []struct {
		base string
		ark  bool
	}{
		{"https://ark.cn-beijing.volces.com/api/v3", true},
		{"https://api.openai.com/v1", false},
		{"https://ark.cn-beijing.volces.com.example.org/api/v3", false},
	} {
		t.Run(tc.base, func(t *testing.T) {
			p := generationPayload(Settings{BaseURL: tc.base, Model: "model", sizes: imageSizes}, "a leaf", "square")
			if p["output_format"] != "png" || p["model"] != "model" || p["prompt"] != "a leaf" || p["size"] != "1024x1024" {
				t.Fatal(p)
			}
			if tc.ark && (p["response_format"] != "b64_json" || p["n"] != nil) ||
				!tc.ark && (p["response_format"] != nil || p["n"] != 1) {
				t.Fatal(p)
			}
		})
	}
}

func TestProviderDownloadAndImageValidation(t *testing.T) {
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewGray(image.Rect(0, 0, 1536, 1024))); err != nil {
		t.Fatal(err)
	}
	location := ""
	var host string
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/generations":
			var in struct {
				Size string
				N    int
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Size != "1536x1024" || in.N != 1 {
				t.Error("provider payload", in, err)
			}
			if r.Header.Get("Idempotency-Key") != "fixed" || r.Header.Get("Authorization") != "Bearer fixture" {
				t.Error("missing provider headers")
			}
			w.Header().Set("X-Request-ID", "provider-id")
			if e := json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"url": location}}}); e != nil {
				t.Error(e)
			}
		case "/temporary":
			if r.Header.Get("Authorization") != "" || r.URL.RawQuery != "signature=test" {
				t.Error("download credential scope")
			}
			if _, e := w.Write(raw.Bytes()); e != nil {
				t.Error(e)
			}
		default:
			t.Errorf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer provider.Close()
	host = provider.URL
	location = host + "/temporary?signature=test"
	s := Service{HTTP: provider.Client()}
	c := Settings{sizes: imageSizes, BaseURL: host, Model: "fixture", Timeout: 2}
	data, id, e := s.generate(context.Background(), c, "fixture", "example", "landscape", "fixed")
	if e != nil || id != "provider-id" || !bytes.Equal(data, raw.Bytes()) {
		t.Fatal("download", id, e)
	}
	location = "https://untrusted.invalid/file"
	if _, _, e = s.generate(t.Context(), c, "fixture", "example", "landscape", "fixed"); e == nil {
		t.Fatal("foreign origin accepted")
	}
	if _, e = validatePNG(raw.Bytes(), imageSizes["portrait"]); e == nil {
		t.Fatal("wrong size accepted")
	}
	if _, e = validatePNG(raw.Bytes()[:raw.Len()/2], imageSizes["landscape"]); e == nil {
		t.Fatal("incomplete PNG accepted")
	}
}
