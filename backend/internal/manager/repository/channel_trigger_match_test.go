package repository

import "testing"

func TestChannelTriggerMatchesUsesSharedKeywordSemantics(t *testing.T) {
	raw := []byte(`{"mention":false,"keywords":["HELP，日报"],"regexes":[]}`)
	for _, text := range []string{"help me", "写日报"} {
		got, err := channelTriggerMatches(raw, text, false)
		if err != nil || !got {
			t.Fatal(text, got, err)
		}
	}
	got, err := channelTriggerMatches(raw, "unrelated", false)
	if err != nil || got {
		t.Fatal(got, err)
	}
	if _, err = channelTriggerMatches([]byte(`{"regexes":["/foo/i"]}`), "foo", false); err == nil {
		t.Fatal("unsupported syntax accepted")
	}
}
