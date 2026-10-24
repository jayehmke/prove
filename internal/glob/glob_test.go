package glob

import "testing"

func TestSelected(t *testing.T) {
	for _, tc := range []struct {
		patterns []string
		name     string
		want     bool
	}{
		{[]string{"**"}, "a/b/c.go", true},
		{[]string{"*.go"}, "main.go", true},
		{[]string{"*.go"}, "cmd/main.go", false},
		{[]string{"**/*.go"}, "main.go", true},
		{[]string{"**/*.go"}, "cmd/prove/main.go", true},
		{[]string{"src/**"}, "src/a/b.txt", true},
		{[]string{"src/**"}, "srcx/a.txt", false},
		{[]string{"src"}, "src/a/b.txt", true}, // a directory selects what's under it
		{[]string{"src/"}, "src/a.txt", true},
		{[]string{"internal/**/testdata/*"}, "internal/x/y/testdata/f", true},
		{[]string{"**", "!docs/**"}, "docs/a.md", false},
		{[]string{"**", "!docs/**"}, "main.go", true},
		{[]string{"**", "!docs/**", "docs/api.md"}, "docs/api.md", true}, // last match wins
		{nil, "main.go", false},
	} {
		if got := Selected(tc.patterns, tc.name); got != tc.want {
			t.Errorf("Selected(%q, %q) = %v, want %v", tc.patterns, tc.name, got, tc.want)
		}
	}
	if err := Validate("src/[a"); err == nil {
		t.Error("Validate accepted a bad pattern")
	}
}
