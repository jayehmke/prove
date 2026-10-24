package config

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	c, err := Parse([]byte(`
version: 1
store:
  type: s3
  bucket: evidence
checks:
  - name: unit
    run: go test ./...
    inputs: ["**/*.go", go.mod]
    lockfiles: [go.sum]
    signers: [alice]
signers:
  - identity: alice
    key: ssh-ed25519 AAAA
sample: 10
`))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Found || c.Store.Type != "s3" || c.Store.Options["bucket"] != "evidence" || c.Sample != 10 {
		t.Errorf("config = %+v", c)
	}
	if ch, ok := c.Check("unit"); !ok || len(ch.Inputs) != 2 || ch.Lockfiles[0] != "go.sum" {
		t.Errorf("check = %+v", ch)
	}

	for yaml, want := range map[string]string{
		"checks:\n  - name: a\n":                                "name and run are required",
		"checks:\n  - {name: a, run: x}\n  - {name: a, run: y}": "listed twice",
		"checks:\n  - {name: a, run: x, signers: [bob]}":        `signer "bob" is not in signers`,
		"chekcs: []":                   "chekcs",
		"sample: 101":                  "between 0 and 100",
		"requirements: {pattern: '('}": "requirements.pattern",
		"checks:\n  - {name: a, run: x, inputs: ['[']}": "bad pattern",
	} {
		if _, err := Parse([]byte(yaml)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want an error with %q", yaml, err, want)
		}
	}
}
