package pi

import "testing"

func TestNativeBaseURLMatchesPlatformAnthropicProbe(t *testing.T) {
	for _, base := range []string{"https://example.test", "https://example.test/v1", "https://example.test/v1/"} {
		if got := nativeBaseURL(Model{Protocol: "anthropic_messages", BaseURL: base}); got != "https://example.test" {
			t.Fatal(got)
		}
	}
	if got := nativeBaseURL(Model{Protocol: "anthropic_messages", BaseURL: "https://example.test/prefix/v1"}); got != "https://example.test/prefix" {
		t.Fatal(got)
	}
	if got := nativeBaseURL(Model{Protocol: "anthropic_messages", BaseURL: "https://example.test/proxy/v1?route=tenant%2Fone"}); got != "https://example.test/proxy?route=tenant%2Fone" {
		t.Fatal(got)
	}
	if got := nativeBaseURL(Model{Protocol: "anthropic_messages", BaseURL: "https://example.test/proxy%2Ftenant/v1"}); got != "https://example.test/proxy%2Ftenant" {
		t.Fatal(got)
	}
	if got := nativeBaseURL(Model{Protocol: "chat_completions", BaseURL: "https://example.test/v1"}); got != "https://example.test/v1" {
		t.Fatal(got)
	}
}
