// Package glob matches slash-separated repository paths against patterns.
//
// A pattern is a path.Match pattern per segment, plus "**", which matches
// zero or more whole segments. "**" alone matches every path. A pattern with
// no slash, like "*.go", matches that name at any depth, the way .gitignore
// does. A trailing "/" (like "docs/") matches everything under the directory.
package glob

import (
	"fmt"
	"path"
	"strings"
)

// Validate reports a malformed pattern.
func Validate(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("empty pattern")
	}
	for _, seg := range strings.Split(normalize(pattern), "/") {
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, ""); err != nil {
			return fmt.Errorf("bad pattern %q: %w", pattern, err)
		}
	}
	return nil
}

// Match reports whether name matches pattern.
func Match(pattern, name string) bool {
	return match(strings.Split(normalize(pattern), "/"), strings.Split(name, "/"))
}

// MatchAny reports whether name matches any of the patterns.
func MatchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if Match(p, name) {
			return true
		}
	}
	return false
}

func normalize(p string) string {
	p = strings.TrimPrefix(p, "./")
	if strings.HasSuffix(p, "/") {
		p += "**"
	}
	if strings.HasPrefix(p, "/") {
		return strings.TrimPrefix(p, "/")
	}
	if !strings.Contains(p, "/") {
		p = "**/" + p
	}
	return p
}

func match(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for i := 0; i <= len(name); i++ {
				if match(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}
