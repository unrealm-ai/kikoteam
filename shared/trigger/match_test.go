package trigger

import "testing"

func TestSharedMatching(t *testing.T) {
	for _, tc := range []struct {
		kind, p, text, match string
		want                 bool
	}{
		{"keyword", " Help，日报, ", "Please HELP", "Help", true},
		{"keyword", " Help，日报, ", "写日报", "日报", true},
		{"keyword", "one,two", "three", "", false},
		{"regex", "(?i)^help [0-9]+$", "HELP 42", "HELP 42", true},
		{"regex", "^$", "", "", true},
	} {
		m, e := Compile(tc.kind, tc.p)
		if e != nil {
			t.Fatal(e)
		}
		got, ok := m.Match(tc.text)
		if ok != tc.want || got != tc.match {
			t.Fatalf("%+v: %q %v", tc, got, ok)
		}
	}
	for _, p := range []string{"[", "/foo/i", "(?<=foo)bar", "(foo)\\1"} {
		if _, e := Compile("regex", p); e == nil {
			t.Fatal("unsupported accepted", p)
		}
	}
	if _, e := Compile("keyword", ", ， "); e == nil {
		t.Fatal("empty split rule accepted")
	}
}
