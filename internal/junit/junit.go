// Package junit reads JUnit XML and finds requirement IDs in test names.
package junit

import (
	"errors"
	"io"
	"regexp"

	"github.com/jayehmke/prove/internal/record"
)

// Parse reads JUnit XML (a <testsuites> or <testsuite> root).
func Parse(r io.Reader) ([]record.Test, error) { return nil, errors.New("not implemented") }

// ParseFiles reads and concatenates several JUnit files.
func ParseFiles(paths []string) ([]record.Test, error) { return nil, errors.New("not implemented") }

// Requirements groups tests by the requirement IDs re finds in their names.
// A requirement passes when it has a passing test and no failing one.
func Requirements(tests []record.Test, re *regexp.Regexp) []record.Requirement { return nil }
