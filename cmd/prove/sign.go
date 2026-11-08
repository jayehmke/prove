package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/runner"
)

// runUnsigned runs checks with no signing key loaded and writes each
// result to dir/<check>.json for prove sign.
func runUnsigned(ctx context.Context, e *env, checks []config.Check, req *regexp.Regexp, dir string, stdout, stderr io.Writer) (int, error) {
	code := exitOK
	var results []*runner.Result
	for _, c := range checks {
		fmt.Fprintf(stderr, "prove: running %s: %s\n", c.Name, c.Run)
		res, err := runner.Execute(ctx, e.repo, c, req, stdout, stderr)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", c.Name, err)
		}
		fmt.Fprintf(stderr, "prove: %s exited %d, not signed\n", c.Name, res.ExitCode)
		if res.ExitCode != 0 {
			code = exitFail
		}
		results = append(results, res)
	}
	// Written once every check has run, so a results folder inside the
	// working tree can't fail a later check's clean-tree test.
	for _, res := range results {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return 0, err
		}
		if err := os.WriteFile(filepath.Join(dir, res.Check+".json"), append(data, '\n'), 0o644); err != nil {
			return 0, err
		}
	}
	fmt.Fprintf(stderr, "prove: wrote %d unsigned result(s) to %s; prove sign signs them\n", len(results), dir)
	return code, nil
}
