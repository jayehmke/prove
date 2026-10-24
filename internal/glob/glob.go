// Package glob matches repository paths against input patterns.
//
// Patterns are slash-separated and matched against the whole path from the
// repository root. `*`, `?` and `[...]` match within one path segment (as in
// path.Match), `**` matches any number of segments, a pattern that names a
// directory matches everything under it, and a leading `!` excludes. The last
// pattern that matches a path decides, as in .gitignore.
package glob

import (
	"fmt"
	"path"
	"strings"
)

// Validate reports a malformed pattern.
func Validate(pattern string) error {
	p := strings.TrimPrefix(pattern, "!")
	if p == "" {
		return fmt.Errorf("empty pattern %q", pattern)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, ""); err != nil {
			return fmt.Errorf("bad pattern %q: %v", pattern, err)
		}
	}
	return nil
}

// Match reports whether name, or a directory that contains it, matches
// pattern. A leading `!` is ignored here; Select applies it.
func Match(pattern, name string) bool {
	pat := strings.Split(strings.Trim(strings.TrimPrefix(pattern, "!"), "/"), "/")
	segs := strings.Split(name, "/")
	for n := len(segs); n >= 1; n-- {
		if matchSegs(pat, segs[:n]) {
			return true
		}
	}
	return false
}

func matchSegs(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegs(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], segs[0])
	return err == nil && ok && matchSegs(pat[1:], segs[1:])
}

// Selected reports whether name is selected by patterns: the last pattern
// that matches decides, and `!` patterns exclude.
func Selected(patterns []string, name string) bool {
	selected := false
	for _, p := range patterns {
		if Match(p, name) {
			selected = !strings.HasPrefix(p, "!")
		}
	}
	return selected
}
