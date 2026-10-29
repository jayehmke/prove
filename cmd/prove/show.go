package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/jayehmke/prove/internal/verify"
)

func cmdShow(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	format := fs.String("format", "md", "")
	onto := fs.String("onto", "", "")
	pos, rest, err := parse(fs, args)
	if err != nil {
		return 0, err
	}
	if rest != nil {
		return 0, usageError{"show takes no command"}
	}
	if *format != "md" && *format != "json" {
		return 0, usageError{fmt.Sprintf("unknown format %q: use md or json", *format)}
	}
	rep, err := report(ctx, pos, *onto, 0, stderr, stderr)
	if err != nil {
		return 0, err
	}
	if *format == "json" {
		return exitOK, writeJSON(stdout, rep)
	}
	writeMarkdown(stdout, rep)
	return exitOK, nil
}

// writeMarkdown prints one table row per check, then requirement coverage.
func writeMarkdown(w io.Writer, rep *verify.Report) {
	fmt.Fprintf(w, "## prove: %s (tree %s)\n\n", short(rep.Commit), short(rep.Tree))
	if rep.OK {
		fmt.Fprintf(w, "**PASS**: every check (%d) verified\n\n", len(rep.Checks))
	} else {
		fmt.Fprintf(w, "**FAIL**: %d problem(s)\n\n", len(rep.Failures))
	}
	if len(rep.Checks) > 0 {
		fmt.Fprintln(w, "| Check | Result | Signer | Record |")
		fmt.Fprintln(w, "|---|---|---|---|")
		for _, c := range rep.Checks {
			rec := c.Reason
			if c.Signer != "" {
				rec = origin(c)
				if c.Sampled {
					rec += ", rerun by sampling"
				}
				if !c.OK {
					rec += ": " + c.Reason
				}
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s |\n", cell(c.Name), c.Status, cell(c.Signer), cell(rec))
		}
		fmt.Fprintln(w)
	}
	if len(rep.Requirements) > 0 {
		n, covered := coverage(rep)
		fmt.Fprintf(w, "**Requirements**: %d/%d required covered\n\n", covered, n)
		fmt.Fprintln(w, "| Requirement | Status | Tests |")
		fmt.Fprintln(w, "|---|---|---|")
		for _, r := range rep.Requirements {
			status := "not covered"
			if r.Covered {
				status = "covered"
			}
			if !r.Required {
				status += " (not required)"
			}
			tests := append(append([]string{}, r.Passing...), prefix("failed: ", r.Failing)...)
			tests = append(tests, prefix("skipped: ", r.Skipped)...)
			fmt.Fprintf(w, "| %s | %s | %s |\n", cell(r.ID), status, cell(strings.Join(tests, "; ")))
		}
		fmt.Fprintln(w)
	}
	var other []string
	for _, f := range rep.Failures {
		if !strings.HasPrefix(f, "requirement ") && !isCheckFailure(rep, f) {
			other = append(other, f)
		}
	}
	for _, f := range other {
		fmt.Fprintf(w, "- %s\n", f)
	}
}

func isCheckFailure(rep *verify.Report, f string) bool {
	for _, c := range rep.Checks {
		if strings.HasPrefix(f, c.Name+": ") {
			return true
		}
	}
	return false
}

func prefix(p string, list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = p + s
	}
	return out
}

// cell escapes a markdown table cell.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}
