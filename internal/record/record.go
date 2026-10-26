// Package record builds, signs and opens prove records: in-toto Statement v1
// documents in a DSSE envelope.
package record

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

// Seal signs a statement and returns the envelope bytes to store.
func Seal(st Statement, s Signer) ([]byte, error) {
	payload, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	keyID, sig, err := s.Sign(PAE(PayloadType, payload))
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{
		PayloadType: PayloadType,
		Payload:     base64.StdEncoding.EncodeToString(payload),
		Signatures:  []Signature{{KeyID: keyID, Sig: base64.StdEncoding.EncodeToString(sig)}},
	})
}

// Open parses stored envelope bytes without checking signatures. The bytes
// must be exactly what Seal wrote: an envelope that was re-encoded or edited
// outside the signed payload is rejected, so any changed byte is caught.
func Open(data []byte) (*Envelope, *Statement, error) {
	var env Envelope
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return nil, nil, fmt.Errorf("not a DSSE envelope: %v", err)
	}
	canonical, err := json.Marshal(env)
	if err != nil || !bytes.Equal(canonical, data) {
		return nil, nil, errors.New("record bytes are not the envelope prove signed (edited after signing)")
	}
	if env.PayloadType != PayloadType {
		return nil, nil, fmt.Errorf("payloadType is %q, not %q", env.PayloadType, PayloadType)
	}
	if len(env.Signatures) == 0 {
		return nil, nil, errors.New("envelope has no signatures")
	}
	payload, err := env.PayloadBytes()
	if err != nil {
		return nil, nil, err
	}
	var st Statement
	if err := json.Unmarshal(payload, &st); err != nil {
		return nil, nil, fmt.Errorf("payload is not an in-toto statement: %v", err)
	}
	if st.Type != StatementType {
		return nil, nil, fmt.Errorf("statement _type is %q, not %q", st.Type, StatementType)
	}
	if st.PredicateType != PredicateType {
		return nil, nil, fmt.Errorf("predicateType is %q, not %q", st.PredicateType, PredicateType)
	}
	if st.Tree() == "" {
		return nil, nil, errors.New("statement has no git tree subject")
	}
	return &env, &st, nil
}

// PayloadBytes decodes the payload.
func (e *Envelope) PayloadBytes() ([]byte, error) {
	p, err := base64.StdEncoding.Strict().DecodeString(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("payload is not base64: %v", err)
	}
	return p, nil
}

// PAE is the DSSE pre-authentication encoding.
func PAE(payloadType string, payload []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "DSSEv1 %d %s %d ", len(payloadType), payloadType, len(payload))
	b.Write(payload)
	return b.Bytes()
}

// ID is the content address of stored envelope bytes.
func ID(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
