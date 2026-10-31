// Package config reads .prove.yaml, the policy a repository's checks are
// run and verified against.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/jayehmke/prove/internal/glob"
	"gopkg.in/yaml.v3"
)

// FileName is the policy file at the repository root.
const FileName = ".prove.yaml"

// DefaultInputs is the input set of a check that declares none: the whole
// repository, which is always safe.
var DefaultInputs = []string{"**"}

// Config is a parsed .prove.yaml.
type Config struct {
	Version int `yaml:"version"`
	// Checks every commit must carry a passing record for.
	Checks []Check `yaml:"checks"`
	// Signers maps a signer identity to its public key.
	Signers map[string]SignerKey `yaml:"signers"`
	// Requirements found in test names.
	Requirements Requirements `yaml:"requirements"`
	// Store is where records are kept.
	Store StoreConfig `yaml:"store"`
	// Share is a second store that prove push --share copies records
	// into, and that verify, plan and show read along with Store. Empty
	// means none.
	Share StoreConfig `yaml:"share"`
	// Sample is the percent of checks verify reruns by default.
	Sample float64 `yaml:"sample"`
}

// Check is a named command plus the files it depends on.
type Check struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
	// Inputs are globs of the files the command reads. Default: everything.
	Inputs []string `yaml:"inputs"`
	// Lockfiles are globs of dependency lockfiles, hashed with the inputs.
	Lockfiles []string `yaml:"lockfiles"`
	// JUnit is the path of the JUnit XML report the command writes.
	JUnit string `yaml:"junit"`
	// Signers may sign this check. Empty means any trusted signer.
	Signers []string `yaml:"signers"`
}

// SignerKey is a trusted public key. In YAML it is either a string (an SSH
// public key in authorized_keys form) or a map with a type naming an
// external signer adapter.
type SignerKey struct {
	// Type is "ssh" or the name of a prove-signer-<type> adapter.
	Type string `yaml:"type"`
	// Key is the public key, in the form the signer type understands.
	Key string `yaml:"key"`
}

// UnmarshalYAML accepts a plain string as an SSH key.
func (k *SignerKey) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		k.Type, k.Key = "ssh", n.Value
		return nil
	}
	type plain SignerKey
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*k = SignerKey(p)
	if k.Type == "" {
		k.Type = "ssh"
	}
	return nil
}

// Requirements configures requirement IDs read from test names.
type Requirements struct {
	// Pattern is a regular expression matching one requirement ID.
	Pattern string `yaml:"pattern"`
	// Required IDs must each have at least one passing test.
	Required []string `yaml:"required"`
}

// StoreConfig names the record store. In YAML it is either a string (the
// type) or a map.
type StoreConfig struct {
	// Type is "local", "git" or the name of a prove-store-<type> adapter.
	Type string `yaml:"type"`
	// Path is the folder of the local store.
	Path string `yaml:"path"`
	// Options are passed to an external store adapter as its config.
	Options map[string]any `yaml:"options"`
}

// UnmarshalYAML accepts a plain string as the store type.
func (s *StoreConfig) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		s.Type = n.Value
		return nil
	}
	type plain StoreConfig
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*s = StoreConfig(p)
	return nil
}

// Load reads FileName from the repository root. It returns nil and no error
// when the file does not exist.
func Load(root string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(root, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return c, nil
}

// Parse parses and validates a policy.
func Parse(data []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	for i := range c.Checks {
		if len(c.Checks[i].Inputs) == 0 {
			c.Checks[i].Inputs = DefaultInputs
		}
	}
	return &c, nil
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func (c *Config) validate() error {
	if c.Version != 0 && c.Version != 1 {
		return fmt.Errorf("unsupported version %d (this prove reads version 1)", c.Version)
	}
	seen := map[string]bool{}
	for i, ch := range c.Checks {
		if !namePattern.MatchString(ch.Name) {
			return fmt.Errorf("checks[%d]: name %q must be letters, digits, '.', '_' or '-'", i, ch.Name)
		}
		if seen[ch.Name] {
			return fmt.Errorf("check %q is defined twice", ch.Name)
		}
		seen[ch.Name] = true
		if ch.Run == "" {
			return fmt.Errorf("check %q has no run command", ch.Name)
		}
		for _, p := range append(append([]string{}, ch.Inputs...), ch.Lockfiles...) {
			if err := glob.Validate(p); err != nil {
				return fmt.Errorf("check %q: %w", ch.Name, err)
			}
		}
		if ch.JUnit != "" && (filepath.IsAbs(ch.JUnit) || !filepath.IsLocal(filepath.FromSlash(ch.JUnit))) {
			return fmt.Errorf("check %q: junit path %q must be inside the repository", ch.Name, ch.JUnit)
		}
		for _, s := range ch.Signers {
			if _, ok := c.Signers[s]; !ok {
				return fmt.Errorf("check %q allows signer %q, which is not in signers", ch.Name, s)
			}
		}
	}
	for id, k := range c.Signers {
		if k.Key == "" {
			return fmt.Errorf("signer %q has no key", id)
		}
		if !ValidName(k.Type) {
			return fmt.Errorf("signer %q type %q must be ssh or an adapter name (letters, digits, '.', '_' or '-')", id, k.Type)
		}
	}
	if c.Store.Type != "" && !ValidName(c.Store.Type) {
		return fmt.Errorf("store type %q must be git, local or an adapter name (letters, digits, '.', '_' or '-')", c.Store.Type)
	}
	switch {
	case c.Share.Type == "" && (c.Share.Path != "" || len(c.Share.Options) > 0):
		return fmt.Errorf("share needs a type: local or an adapter name such as s3")
	case c.Share.Type == "git":
		return fmt.Errorf("share type git: records in the git store are shared with prove push <remote>; name another store")
	case c.Share.Type != "" && !ValidName(c.Share.Type):
		return fmt.Errorf("share type %q must be local or an adapter name (letters, digits, '.', '_' or '-')", c.Share.Type)
	}
	if c.Requirements.Pattern != "" {
		if _, err := regexp.Compile(c.Requirements.Pattern); err != nil {
			return fmt.Errorf("requirements.pattern: %w", err)
		}
	} else if len(c.Requirements.Required) > 0 {
		return fmt.Errorf("requirements.required needs requirements.pattern")
	}
	if c.Sample < 0 || c.Sample > 100 {
		return fmt.Errorf("sample must be between 0 and 100, got %v", c.Sample)
	}
	return nil
}

// Check returns the check with the given name.
func (c *Config) Check(name string) (Check, bool) {
	if c == nil {
		return Check{}, false
	}
	for _, ch := range c.Checks {
		if ch.Name == name {
			return ch, true
		}
	}
	return Check{}, false
}

// ValidName reports whether name can name a check.
func ValidName(name string) bool { return namePattern.MatchString(name) }
