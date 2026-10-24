// Package config reads .prove.yaml, the policy that names a repository's
// checks, who may sign them, and where records are stored.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/jayehmke/prove/internal/glob"
)

// FileName is the policy file at the repository root.
const FileName = ".prove.yaml"

// DefaultRequirementPattern finds requirement IDs such as AC-807-1 in test
// names when the policy doesn't set one.
const DefaultRequirementPattern = `AC-[0-9]+(?:-[0-9]+)*`

// Config is a parsed .prove.yaml.
type Config struct {
	Version      int          `yaml:"version"`
	Store        Store        `yaml:"store"`
	Checks       []Check      `yaml:"checks"`
	Signers      []Signer     `yaml:"signers"`
	Requirements Requirements `yaml:"requirements"`
	// Sample is the percent of checks verify reruns locally (0-100).
	Sample float64 `yaml:"sample"`

	// Found is false for the zero config used when there is no file.
	Found bool `yaml:"-"`
}

// Store selects where records live: "git" (the default), "local", or the
// name of an external adapter `prove-store-<name>`. Other keys are passed
// to the adapter.
type Store struct {
	Type    string         `yaml:"type"`
	Path    string         `yaml:"path"`
	Options map[string]any `yaml:",inline"`
}

// Check is a named command and the files it depends on.
type Check struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
	// Inputs are globs of the files the check reads. Empty means the whole
	// repository.
	Inputs []string `yaml:"inputs"`
	// Lockfiles are always part of the fingerprint, inside inputs or not.
	Lockfiles []string `yaml:"lockfiles"`
	// JUnit is the path of the JUnit XML the command writes, optional.
	// $PROVE_OUT expands to a scratch directory prove provides.
	JUnit string `yaml:"junit"`
	// Signers lists the identities allowed to sign this check. Empty means
	// any trusted signer.
	Signers []string `yaml:"signers"`
}

// Signer is a trusted identity and its public key.
type Signer struct {
	Identity string `yaml:"identity"`
	// Key is an SSH public key ("ssh-ed25519 AAAA..."), or for an external
	// signer the key ID the adapter reports.
	Key string `yaml:"key"`
	// Adapter names an external signer `prove-signer-<name>`. Empty means
	// the built-in ssh signer.
	Adapter string `yaml:"adapter"`
}

// Requirements sets how requirement IDs are found and which must pass.
type Requirements struct {
	Pattern  string   `yaml:"pattern"`
	Required []string `yaml:"required"`
}

// Zero is the configuration used when a repository has no .prove.yaml.
func Zero() *Config {
	return &Config{Version: 1, Store: Store{Type: "git"}}
}

// Parse parses and validates a .prove.yaml.
func Parse(data []byte) (*Config, error) {
	c := Zero()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %v", FileName, err)
	}
	c.Found = true
	if c.Store.Type == "" {
		c.Store.Type = "git"
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %v", FileName, err)
	}
	return c, nil
}

// Load reads a .prove.yaml from disk. A missing file gives the zero config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Zero(), nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func (c *Config) validate() error {
	if c.Version != 0 && c.Version != 1 {
		return fmt.Errorf("version %d is not supported (want 1)", c.Version)
	}
	if c.Sample < 0 || c.Sample > 100 {
		return fmt.Errorf("sample must be between 0 and 100, got %v", c.Sample)
	}
	if _, err := c.RequirementPattern(); err != nil {
		return err
	}
	identities := map[string]bool{}
	for i, s := range c.Signers {
		if s.Identity == "" || s.Key == "" {
			return fmt.Errorf("signers[%d]: identity and key are required", i)
		}
		if identities[s.Identity] {
			return fmt.Errorf("signers: identity %q is listed twice", s.Identity)
		}
		identities[s.Identity] = true
	}
	names := map[string]bool{}
	for i, ch := range c.Checks {
		if ch.Name == "" || ch.Run == "" {
			return fmt.Errorf("checks[%d]: name and run are required", i)
		}
		if names[ch.Name] {
			return fmt.Errorf("checks: %q is listed twice", ch.Name)
		}
		names[ch.Name] = true
		for _, p := range append(append([]string{}, ch.Inputs...), ch.Lockfiles...) {
			if err := glob.Validate(p); err != nil {
				return fmt.Errorf("check %q: %v", ch.Name, err)
			}
		}
		for _, id := range ch.Signers {
			if !identities[id] {
				return fmt.Errorf("check %q: signer %q is not in signers", ch.Name, id)
			}
		}
	}
	return nil
}

// RequirementPattern compiles the requirement ID pattern.
func (c *Config) RequirementPattern() (*regexp.Regexp, error) {
	p := c.Requirements.Pattern
	if p == "" {
		p = DefaultRequirementPattern
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, fmt.Errorf("requirements.pattern: %v", err)
	}
	return re, nil
}

// Check returns the named check.
func (c *Config) Check(name string) (Check, bool) {
	for _, ch := range c.Checks {
		if ch.Name == name {
			return ch, true
		}
	}
	return Check{}, false
}

// InputsOrDefault returns the check's inputs, or the whole repository.
func (ch Check) InputsOrDefault() []string {
	if len(ch.Inputs) == 0 {
		return []string{"**"}
	}
	return ch.Inputs
}
