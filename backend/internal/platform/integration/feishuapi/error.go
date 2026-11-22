// Package feishuapi formats Feishu errors for administrator-facing diagnostics.
package feishuapi

import (
	"fmt"
	"strings"
	"unicode"
)

// ErrorDetail exposes only the provider code and message, never the response body.
func ErrorDetail(code int, message string, secrets ...string) string {
	if code <= 0 {
		return ""
	}
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[已隐藏]")
		}
	}
	message = strings.Join(strings.FieldsFunc(message, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	}), " ")
	runes := []rune(message)
	if len(runes) > 512 {
		message = string(runes[:512]) + "…"
	}
	return strings.TrimSpace(fmt.Sprintf("%d %s", code, message))
}
