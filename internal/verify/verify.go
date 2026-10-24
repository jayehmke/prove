// Package verify checks a commit's records against the policy.
package verify

import (
	"context"
	"errors"
	"io"
	"math/rand"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/sign"
	"github.com/jayehmke/prove/internal/store"
)

// Options configures a verification.
type Options struct {
	Repo   *gitx.Repo
	Commit string
	Policy *config.Config
	Store  store.Store
	Trust  *sign.Trust
	// Sample is the percent of checks to rerun locally.
	Sample float64
	Rand   *rand.Rand
	Stdout io.Writer
	Stderr io.Writer
}

// CheckResult is the verdict on one policy check.
type CheckResult struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Result   string `json:"result"`
	Reason   string `json:"reason,omitempty"`
	Signer   string `json:"signer,omitempty"`
	Reused   bool   `json:"reused"`
	RecordID string `json:"record,omitempty"`
	// RecordCommit is the commit the record was made on.
	RecordCommit string `json:"recordCommit,omitempty"`
	Sampled      bool   `json:"sampled,omitempty"`
}

// RequirementResult is the verdict on one requirement ID.
type RequirementResult struct {
	ID       string   `json:"id"`
	Met      bool     `json:"met"`
	Required bool     `json:"required"`
	Tests    []string `json:"tests,omitempty"`
}

// Report is the outcome of a verification.
type Report struct {
	Commit       string              `json:"commit"`
	Tree         string              `json:"tree"`
	OK           bool                `json:"ok"`
	Checks       []CheckResult       `json:"checks"`
	Requirements []RequirementResult `json:"requirements,omitempty"`
	Notes        []string            `json:"notes,omitempty"`
}

// Verify checks every policy check of the commit.
func Verify(ctx context.Context, o Options) (*Report, error) { return nil, errors.New("not implemented") }

// Failures gives one readable reason for each failure.
func (r *Report) Failures() []string { return nil }
