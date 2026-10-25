// Package adapter calls external prove adapters: programs on PATH named
// prove-<kind>-<name> that read one JSON request on stdin and write one
// JSON response on stdout, like git credential helpers.
package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// programPattern is a bare program name: no path separator, so exec only
// ever finds it on PATH, never as a file in the working directory.
var programPattern = regexp.MustCompile(`^prove-(store|signer)-[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Program returns the executable name of an adapter.
func Program(kind, name string) string { return "prove-" + kind + "-" + name }

// Call runs the adapter with req as JSON on stdin and decodes its stdout
// into resp. A non-zero exit, an {"error": "..."} answer or a response that
// is not JSON is an error that names the adapter.
func Call(ctx context.Context, program string, req, resp any) error {
	if !programPattern.MatchString(program) {
		return fmt.Errorf("%q is not a valid adapter name: adapters are prove-store-<name> or prove-signer-<name> on PATH, and a name is letters, digits, '.', '_' or '-'", program)
	}
	path, err := exec.LookPath(program)
	if err != nil {
		return fmt.Errorf("adapter %s is not on PATH", program)
	}
	in, err := json.Marshal(req)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	var ack struct {
		Error string `json:"error"`
	}
	out := bytes.TrimSpace(stdout.Bytes())
	if runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if json.Unmarshal(out, &ack) == nil && ack.Error != "" {
			msg = ack.Error
		}
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			if msg == "" {
				msg = "no message"
			}
			return fmt.Errorf("adapter %s failed (exit %d): %s", program, exit.ExitCode(), msg)
		}
		return fmt.Errorf("adapter %s: %v", program, runErr)
	}
	if !json.Valid(out) {
		return fmt.Errorf("adapter %s returned invalid JSON: %q", program, truncate(string(out), 200))
	}
	if err := json.Unmarshal(out, &ack); err == nil && ack.Error != "" {
		return fmt.Errorf("adapter %s: %s", program, ack.Error)
	}
	if err := json.Unmarshal(out, resp); err != nil {
		return fmt.Errorf("adapter %s returned JSON of the wrong shape: %v", program, err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
