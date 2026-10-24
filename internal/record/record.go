// Package record defines prove's evidence record: an in-toto Statement v1
// about a git tree, wrapped in a signed DSSE envelope.
package record

import (
	"time"
)

const (
	// StatementType is the in-toto Statement v1 type.
	StatementType = "https://in-toto.io/Statement/v1"
	// PredicateType identifies a prove check result.
	PredicateType = "https://github.com/jayehmke/prove/check/v0.1"
	// PayloadType is the DSSE payload type for in-toto statements.
	PayloadType = "application/vnd.in-toto+json"

	// Subject digest algorithms, from the in-toto digest set registry.
	DigestGitTree   = "gitTree"
	DigestGitCommit = "gitCommit"

	ResultPass = "pass"
	ResultFail = "fail"
	ResultSkip = "skip"
)

// Statement is an in-toto Statement v1 with a prove check predicate.
type Statement struct {
	Type          string    `json:"_type"`
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     Predicate `json:"predicate"`
}

// Subject is an in-toto resource descriptor.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// Predicate is the result of one check on one tree.
type Predicate struct {
	Check       string   `json:"check"`
	Command     string   `json:"command"`
	Inputs      []string `json:"inputs"`
	Lockfiles   []string `json:"lockfiles,omitempty"`
	Fingerprint string   `json:"fingerprint"`
	ExitCode    int      `json:"exitCode"`
	Result      string   `json:"result"`

	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Runner     Runner    `json:"runner"`

	// Tests and Requirements are present when the check wrote JUnit XML.
	Tests        []Test        `json:"tests,omitempty"`
	Requirements []Requirement `json:"requirements,omitempty"`
}

// Runner describes where the check ran.
type Runner struct {
	ProveVersion string `json:"proveVersion"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
}

// Test is one test case from JUnit XML.
type Test struct {
	Name       string  `json:"name"`
	Classname  string  `json:"classname,omitempty"`
	Result     string  `json:"result"`
	DurationMs float64 `json:"durationMs,omitempty"`
}

// Requirement is a requirement ID found in test names, with its tests.
type Requirement struct {
	ID     string   `json:"id"`
	Result string   `json:"result"`
	Tests  []string `json:"tests"`
}

// Envelope is a DSSE envelope.
type Envelope struct {
	PayloadType string      `json:"payloadType"`
	Payload     string      `json:"payload"`
	Signatures  []Signature `json:"signatures"`
}

// Signature is one DSSE signature.
type Signature struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"`
}

// Signer signs DSSE pre-authentication encodings.
type Signer interface {
	// Sign signs msg and returns the key ID and the raw signature.
	Sign(msg []byte) (keyID string, sig []byte, err error)
}

// New builds a statement about a commit and its tree.
func New(commit, tree string, p Predicate) Statement {
	return Statement{
		Type: StatementType,
		Subject: []Subject{
			{Name: "git+tree", Digest: map[string]string{DigestGitTree: tree}},
			{Name: "git+commit", Digest: map[string]string{DigestGitCommit: commit}},
		},
		PredicateType: PredicateType,
		Predicate:     p,
	}
}

// Tree returns the tree hash the statement is about.
func (s Statement) Tree() string { return s.digest(DigestGitTree) }

// Commit returns the commit hash the statement is about.
func (s Statement) Commit() string { return s.digest(DigestGitCommit) }

func (s Statement) digest(alg string) string {
	for _, sub := range s.Subject {
		if d, ok := sub.Digest[alg]; ok {
			return d
		}
	}
	return ""
}
