package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	c, err := Parse([]byte(`
version: 1
store: local
signers:
  alice: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeFakeFakeFakeFakeFakeFakeFakeFakeFake
  ci: {type: kms, key: "arn:aws:kms:key/1"}
checks:
  - name: unit
    run: go test ./...
    lockfiles: [go.sum]
    signers: [ci]
sample: 10
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Store.Type != "local" || c.Sample != 10 {
		t.Fatalf("store %+v sample %v", c.Store, c.Sample)
	}
	if c.Signers["alice"].Type != "ssh" || c.Signers["ci"] != (SignerKey{Type: "kms", Key: "arn:aws:kms:key/1"}) {
		t.Fatalf("signers %+v", c.Signers)
	}
	unit, ok := c.Check("unit")
	if !ok || !reflect.DeepEqual(unit.Inputs, DefaultInputs) {
		t.Fatalf("unit %+v: inputs should default to the whole repo", unit)
	}

	empty, err := Parse(nil)
	if err != nil || len(empty.Checks) != 0 {
		t.Fatalf("an empty file is an empty policy: %v", err)
	}

	for yml, want := range map[string]string{
		"checks:\n  - name: a\n":                                  "no run command",
		"checks:\n  - {name: a, run: x}\n  - {name: a, run: y}\n": "defined twice",
		"checks:\n  - {name: 'a b', run: x}\n":                    "name",
		"checks:\n  - {name: a, run: x, signers: [bob]}\n":        "not in signers",
		"checks:\n  - {name: a, run: x, junit: ../out.xml}\n":     "inside the repository",
		"checks:\n  - {name: a, run: x, inputs: ['src/[']}\n":     "bad pattern",
		"requirements: {required: [X-1]}\n":                       "pattern",
		"sample: 200\n":                                           "sample",
		"chekcs: []\n":                                            "chekcs",
		"version: 2\n":                                            "version",
	} {
		if _, err := Parse([]byte(yml)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want an error containing %q", yml, err, want)
		}
	}
}
