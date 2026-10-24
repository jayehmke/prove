// Package adapter calls external adapter programs: one JSON request on
// stdin, one JSON response on stdout, like git credential helpers.
package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Version is the protocol version sent with every request.
const Version = 1

// Call runs program with req (plus "op" and "version") on stdin and decodes
// its stdout into resp. A non-zero exit, a response that isn't JSON, or a
// response with a non-empty "error" field is an error that names program.
func Call(program, dir, op string, req map[string]any, resp any) error {
	path, err := exec.LookPath(program)
	if err != nil {
		return fmt.Errorf("%s %s: adapter not found on PATH", program, op)
	}
	body := map[string]any{"op": op, "version": Version}
	for k, v := range req {
		body[k] = v
	}
	in, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("%s %s: %v", program, op, err)
	}
	cmd := exec.Command(path)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(append(in, '\n'))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "no message on stderr"
		}
		return fmt.Errorf("%s %s: %v: %s", program, op, err, msg)
	}
	out := bytes.TrimSpace(stdout.Bytes())
	var head struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(out, &head); err != nil {
		return fmt.Errorf("%s %s: invalid JSON response: %v: %q", program, op, err, truncate(out))
	}
	if head.Error != "" {
		return fmt.Errorf("%s %s: %s", program, op, head.Error)
	}
	if resp == nil {
		return nil
	}
	if err := json.Unmarshal(out, resp); err != nil {
		return fmt.Errorf("%s %s: invalid JSON response: %v", program, op, err)
	}
	return nil
}

func truncate(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "..."
	}
	return string(b)
}
