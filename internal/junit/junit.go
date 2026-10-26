// Package junit reads JUnit XML reports, which go (gotestsum), jest, pytest,
// Playwright, golangci-lint, ESLint, ruff and swift can all write.
package junit

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/jayehmke/prove/internal/record"
)

// Parse reads the test cases of a JUnit XML report, in document order, and
// tags each with the requirement IDs that req (which may be nil) finds in
// its name. A case with a <failure> or <error> fails; one with <skipped>
// is skipped.
func Parse(data []byte, req *regexp.Regexp) ([]record.Test, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var tests []record.Test
	sawRoot := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("junit: %v", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !sawRoot {
			if start.Name.Local != "testsuites" && start.Name.Local != "testsuite" {
				return nil, fmt.Errorf("junit: root element is <%s>, want <testsuites> or <testsuite>", start.Name.Local)
			}
			sawRoot = true
			continue
		}
		if start.Name.Local != "testcase" {
			continue
		}
		var c struct {
			Name      string    `xml:"name,attr"`
			Classname string    `xml:"classname,attr"`
			Failure   *struct{} `xml:"failure"`
			Error     *struct{} `xml:"error"`
			Skipped   *struct{} `xml:"skipped"`
		}
		if err := dec.DecodeElement(&c, &start); err != nil {
			return nil, fmt.Errorf("junit: %v", err)
		}
		t := record.Test{Name: c.Name, Classname: c.Classname, Result: "pass"}
		switch {
		case c.Failure != nil || c.Error != nil:
			t.Result = "fail"
		case c.Skipped != nil:
			t.Result = "skip"
		}
		if req != nil {
			t.Requirements = ids(req, c.Name)
		}
		tests = append(tests, t)
	}
	if !sawRoot {
		return nil, errors.New("junit: no <testsuites> or <testsuite> element")
	}
	return tests, nil
}

func ids(req *regexp.Regexp, name string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range req.FindAllString(name, -1) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
