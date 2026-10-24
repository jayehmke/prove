// Package junit reads JUnit XML and finds requirement IDs in test names.
// go (gotestsum), jest, pytest, Playwright, golangci-lint, ESLint, ruff and
// swift can all write it.
package junit

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"

	"github.com/jayehmke/prove/internal/record"
)

type suite struct {
	Suites []suite    `xml:"testsuite"`
	Cases  []testcase `xml:"testcase"`
}

type testcase struct {
	Name      string     `xml:"name,attr"`
	Classname string     `xml:"classname,attr"`
	Time      float64    `xml:"time,attr"`
	Failures  []struct{} `xml:"failure"`
	Errors    []struct{} `xml:"error"`
	Skipped   *struct{}  `xml:"skipped"`
}

// Parse reads JUnit XML (a <testsuites> or <testsuite> root, suites nested
// to any depth).
func Parse(r io.Reader) ([]record.Test, error) {
	var root suite
	if err := xml.NewDecoder(r).Decode(&root); err != nil {
		return nil, fmt.Errorf("junit: %v", err)
	}
	var tests []record.Test
	var walk func(s suite)
	walk = func(s suite) {
		for _, c := range s.Cases {
			result := record.ResultPass
			switch {
			case len(c.Failures) > 0 || len(c.Errors) > 0:
				result = record.ResultFail
			case c.Skipped != nil:
				result = record.ResultSkip
			}
			tests = append(tests, record.Test{Name: c.Name, Classname: c.Classname, Result: result, DurationMs: c.Time * 1000})
		}
		for _, sub := range s.Suites {
			walk(sub)
		}
	}
	walk(root)
	return tests, nil
}

// ParseFiles reads and concatenates several JUnit files.
func ParseFiles(paths []string) ([]record.Test, error) {
	var all []record.Test
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		tests, err := Parse(f)
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %v", p, err)
		}
		all = append(all, tests...)
	}
	return all, nil
}

// Requirements groups tests by the requirement IDs re finds in their names,
// sorted by ID. A requirement passes when it has a passing test and no
// failing one, fails when any test fails, and is skipped otherwise.
func Requirements(tests []record.Test, re *regexp.Regexp) []record.Requirement {
	byID := map[string]*record.Requirement{}
	var ids []string
	for _, t := range tests {
		seen := map[string]bool{}
		for _, id := range re.FindAllString(t.Name, -1) {
			if seen[id] {
				continue
			}
			seen[id] = true
			r, ok := byID[id]
			if !ok {
				r = &record.Requirement{ID: id, Result: record.ResultSkip}
				byID[id] = r
				ids = append(ids, id)
			}
			r.Tests = append(r.Tests, t.Name)
			switch {
			case t.Result == record.ResultFail:
				r.Result = record.ResultFail
			case t.Result == record.ResultPass && r.Result == record.ResultSkip:
				r.Result = record.ResultPass
			}
		}
	}
	slices.Sort(ids)
	out := make([]record.Requirement, 0, len(ids))
	for _, id := range ids {
		out = append(out, *byID[id])
	}
	return out
}
