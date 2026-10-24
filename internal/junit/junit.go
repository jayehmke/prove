// Package junit reads JUnit XML reports, which go (gotestsum), jest, pytest,
// Playwright, golangci-lint, ESLint, ruff and swift can all write.
package junit

import (
	"errors"
	"regexp"

	"github.com/jayehmke/prove/internal/record"
)

// Parse reads the test cases of a JUnit XML report and tags each with the
// requirement IDs that req (which may be nil) finds in its name.
func Parse(data []byte, req *regexp.Regexp) ([]record.Test, error) {
	return nil, errors.New("not implemented")
}
