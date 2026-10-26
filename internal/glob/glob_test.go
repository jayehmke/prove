package glob

import "testing"

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"**", "a/b/c.go", true},
		{"**", "README.md", true},
		{"*.go", "main.go", true},
		{"*.go", "cmd/prove/main.go", true}, // no slash: any depth
		{"/*.go", "cmd/prove/main.go", false},
		{"/*.go", "main.go", true},
		{"src/**", "src/a.go", true},
		{"src/**", "src/x/y/a.go", true},
		{"src/**", "srcs/a.go", false},
		{"src/**", "lib/src/a.go", false},
		{"src/", "src/x/a.go", true},
		{"src/**/*_test.go", "src/a_test.go", true},
		{"src/**/*_test.go", "src/x/a_test.go", true},
		{"src/**/*_test.go", "src/x/a.go", false},
		{"./go.mod", "go.mod", true},
		{"internal/*/x.go", "internal/a/x.go", true},
		{"internal/*/x.go", "internal/a/b/x.go", false},
	} {
		if got := Match(tc.pattern, tc.name); got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
	if Validate("src/[") == nil {
		t.Error("Validate should reject a bad pattern")
	}
}
