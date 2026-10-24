// Package verify checks a commit against the policy using stored records,
// recomputed fingerprints and, optionally, sampled reruns.
package verify

import (
	"context"
	"errors"
	"io"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/signer"
	"github.com/jayehmke/prove/internal/store"
)

// Options configure a verification.
type Options struct {
	Repo  *gitx.Repo
	Store store.Store
	Trust *signer.TrustSet
	// Policy is the parsed .prove.yaml. Nil means no policy file: every
	// check recorded for the commit's tree is verified against its own
	// command and inputs.
	Policy *config.Config
	// Commit is the revision to verify.
	Commit string
	// Sample is the percent of passing checks to rerun locally.
	Sample float64
	// Stdout and Stderr receive the output of sampled reruns.
	Stdout io.Writer
	Stderr io.Writer
}

// Status of a check.
const (
	StatusPass    = "pass"
	StatusFail    = "fail"
	StatusMissing = "missing"
)

// CheckResult is the verdict for one policy check.
type CheckResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Status string `json:"status"`
	// Reason says why the check failed.
	Reason string `json:"reason,omitempty"`
	// Signer is the trusted identity that signed the record used.
	Signer string `json:"signer,omitempty"`
	// Reused is true when the record was made on a different tree.
	Reused       bool   `json:"reused"`
	RecordID     string `json:"recordId,omitempty"`
	RecordCommit string `json:"recordCommit,omitempty"`
	Sampled      bool   `json:"sampled,omitempty"`
}

// Requirement is the coverage of one requirement ID.
type Requirement struct {
	ID       string   `json:"id"`
	Required bool     `json:"required"`
	Covered  bool     `json:"covered"`
	Passing  []string `json:"passing,omitempty"`
	Failing  []string `json:"failing,omitempty"`
}

// Report is the outcome of a verification.
type Report struct {
	Commit       string        `json:"commit"`
	Tree         string        `json:"tree"`
	OK           bool          `json:"ok"`
	Checks       []CheckResult `json:"checks"`
	Requirements []Requirement `json:"requirements,omitempty"`
	// Failures holds one readable reason per failure.
	Failures []string `json:"failures,omitempty"`
}

// Verify checks every policy check of the commit.
func Verify(ctx context.Context, o Options) (*Report, error) {
	return nil, errors.New("not implemented")
}
