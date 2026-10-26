package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jayehmke/prove/internal/verify"
)

// report runs verify for the commit named in pos (default HEAD).
func report(ctx context.Context, pos []string, sample float64, stdout, stderr io.Writer) (*verify.Report, error) {
	if len(pos) > 1 {
		return nil, usageError{"give at most one commit"}
	}
	e, err := load(ctx)
	if err != nil {
		return nil, err
	}
	trust, err := e.trust(ctx, stderr)
	if err != nil {
		return nil, err
	}
	commit := "HEAD"
	if len(pos) == 1 {
		commit = pos[0]
	}
	if sample < 0 && e.policy != nil {
		sample = e.policy.Sample
	}
	return verify.Verify(ctx, verify.Options{
		Repo: e.repo, Store: e.store, Trust: trust, Policy: e.policy, Commit: commit,
		Sample: sample, Stdout: stdout, Stderr: stderr,
	})
}

func cmdVerify(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	sample := fs.String("sample", "", "")
	format := fs.String("format", "text", "")
	pos, rest, err := parse(fs, args)
	if err != nil {
		return 0, err
	}
	if rest != nil {
		return 0, usageError{"verify takes no command"}
	}
	if *format != "text" && *format != "json" {
		return 0, usageError{fmt.Sprintf("unknown format %q: use text or json", *format)}
	}
	pct := -1.0 // the policy's default
	if *sample != "" {
		pct, err = strconv.ParseFloat(strings.TrimSuffix(*sample, "%"), 64)
		if err != nil || pct < 0 || pct > 100 {
			return 0, usageError{fmt.Sprintf("--sample %q: want a percent from 0 to 100", *sample)}
		}
	}
	rep, err := report(ctx, pos, pct, stderr, stderr)
	if err != nil {
		return 0, err
	}
	if *format == "json" {
		if err := writeJSON(stdout, rep); err != nil {
			return 0, err
		}
	} else {
		writeText(stdout, rep)
	}
	if !rep.OK {
		return exitFail, nil
	}
	return exitOK, nil
}

func writeJSON(w io.Writer, rep *verify.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

func writeText(w io.Writer, rep *verify.Report) {
	fmt.Fprintf(w, "prove verify %s (tree %s)\n", short(rep.Commit), short(rep.Tree))
	width := 0
	for _, c := range rep.Checks {
		width = max(width, len(c.Name))
	}
	for _, c := range rep.Checks {
		mark := "ok  "
		if !c.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "  %s %-*s  %s\n", mark, width, c.Name, detail(c))
	}
	if n, covered := coverage(rep); n > 0 {
		fmt.Fprintf(w, "requirements: %d/%d covered\n", covered, n)
	}
	if rep.OK {
		fmt.Fprintln(w, "PASS")
		return
	}
	fmt.Fprintf(w, "FAIL: %d problem(s)\n", len(rep.Failures))
	for _, f := range rep.Failures {
		fmt.Fprintf(w, "  - %s\n", f)
	}
}

// detail describes where a check's verdict came from.
func detail(c verify.CheckResult) string {
	if !c.OK && c.Signer == "" {
		return c.Reason
	}
	s := "signed by " + c.Signer + ", " + origin(c)
	if c.Sampled {
		s += ", rerun by sampling"
	}
	if !c.OK {
		s += ": " + c.Reason
	}
	return s
}

func origin(c verify.CheckResult) string {
	if c.Reused {
		return "reused from " + short(c.RecordCommit)
	}
	return "new"
}

// coverage counts required requirement IDs and how many are covered.
func coverage(rep *verify.Report) (n, covered int) {
	for _, r := range rep.Requirements {
		if r.Required {
			n++
			if r.Covered {
				covered++
			}
		}
	}
	return n, covered
}

func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
