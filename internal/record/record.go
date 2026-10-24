// Package record builds, signs and opens prove records: in-toto Statement v1
// documents in a DSSE envelope.
package record

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

const (
	// PayloadType is the DSSE payload type of an in-toto statement.
	PayloadType = "application/vnd.in-toto+json"
	// StatementType is the in-toto Statement v1 type.
	StatementType = "https://in-toto.io/Statement/v1"
	// PredicateType identifies a prove check result.
	PredicateType = "https://github.com/jayehmke/prove/check/v0.1"

	// Pass and Fail are the two check results.
	Pass = "pass"
	Fail = "fail"
)

// Statement is an in-toto Statement v1.
type Statement struct {
	Type          string    `json:"_type"`
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     Predicate `json:"predicate"`
}

// Subject is a resource the statement is about.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// Predicate is the result of one run of a check.
type Predicate struct {
	Check       string    `json:"check"`
	Command     string    `json:"command"`
	Inputs      []string  `json:"inputs"`
	Lockfiles   []string  `json:"lockfiles,omitempty"`
	Fingerprint string    `json:"fingerprint"`
	ExitCode    int       `json:"exitCode"`
	Result      string    `json:"result"`
	StartedAt   time.Time `json:"startedAt"`
	FinishedAt  time.Time `json:"finishedAt"`
	// Signer is the identity the signer claimed. Verify trusts the key, not
	// this field.
	Signer string `json:"signer"`
	// Tests are per-test results read from JUnit XML, when configured.
	Tests []Test `json:"tests,omitempty"`
	// JUnitError says why configured JUnit output could not be read.
	JUnitError string `json:"junitError,omitempty"`
	Runner     Runner `json:"runner"`
}

// Test is one test case from a JUnit report.
type Test struct {
	Name         string   `json:"name"`
	Classname    string   `json:"classname,omitempty"`
	Result       string   `json:"result"` // pass, fail or skip
	Requirements []string `json:"requirements,omitempty"`
}

// Runner describes where the check ran.
type Runner struct {
	Prove string `json:"prove"`
	OS    string `json:"os"`
	Arch  string `json:"arch"`
}

// Subject names and in-toto digest algorithms for git objects.
const (
	TreeSubject   = "git-tree"
	CommitSubject = "git-commit"
	TreeDigest    = "gitTree"
	CommitDigest  = "gitCommit"
)

// New returns a statement about a commit and its tree.
func New(commit, tree string, p Predicate) Statement {
	return Statement{
		Type: StatementType,
		Subject: []Subject{
			{Name: TreeSubject, Digest: map[string]string{TreeDigest: tree}},
			{Name: CommitSubject, Digest: map[string]string{CommitDigest: commit}},
		},
		PredicateType: PredicateType,
		Predicate:     p,
	}
}

// Tree returns the git tree the statement is about.
func (s *Statement) Tree() string { return s.digest(TreeDigest) }

// Commit returns the git commit the statement is about.
func (s *Statement) Commit() string { return s.digest(CommitDigest) }

func (s *Statement) digest(alg string) string {
	for _, sub := range s.Subject {
		if d, ok := sub.Digest[alg]; ok {
			return d
		}
	}
	return ""
}

// Envelope is a DSSE envelope.
type Envelope struct {
	PayloadType string      `json:"payloadType"`
	Payload     string      `json:"payload"` // standard base64
	Signatures  []Signature `json:"signatures"`
}

// Signature is one DSSE signature.
type Signature struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"` // standard base64
}

// Signer signs the DSSE pre-authentication encoding of a payload.
type Signer interface {
	Sign(msg []byte) (keyID string, sig []byte, err error)
}

// ErrNotImplemented marks unfinished code.
var ErrNotImplemented = errors.New("not implemented")

// Seal signs a statement and returns the envelope bytes to store.
func Seal(st Statement, s Signer) ([]byte, error) {
	return nil, ErrNotImplemented
}

// Open parses stored envelope bytes without checking signatures.
func Open(data []byte) (*Envelope, *Statement, error) {
	return nil, nil, ErrNotImplemented
}

// PAE is the DSSE pre-authentication encoding.
func PAE(payloadType string, payload []byte) []byte {
	return nil
}

// ID is the content address of stored envelope bytes.
func ID(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
