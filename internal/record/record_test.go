package record_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jayehmke/prove/internal/record"
)

type edSigner struct{ key ed25519.PrivateKey }

func (s edSigner) Sign(msg []byte) (string, []byte, error) {
	return "test-key", ed25519.Sign(s.key, msg), nil
}

func TestRecordIsInTotoStatement(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const commit = "0123456789abcdef0123456789abcdef01234567"
	const tree = "89abcdef0123456789abcdef0123456789abcdef"
	st := record.New(commit, tree, record.Predicate{
		Check:       "unit",
		Command:     "go test ./...",
		Inputs:      []string{"**"},
		Fingerprint: "sha256:00",
		Result:      record.ResultPass,
		StartedAt:   time.Unix(1, 0).UTC(),
		FinishedAt:  time.Unix(2, 0).UTC(),
	})
	env, err := record.Seal(st, edSigner{priv})
	if err != nil {
		t.Fatal(err)
	}

	// DSSE envelope with an in-toto payload.
	if env.PayloadType != "application/vnd.in-toto+json" {
		t.Errorf("payloadType = %q", env.PayloadType)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	if len(env.Signatures) != 1 || env.Signatures[0].KeyID != "test-key" {
		t.Fatalf("signatures = %+v", env.Signatures)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signatures[0].Sig)
	if err != nil {
		t.Fatalf("sig is not base64: %v", err)
	}
	// The signature covers the DSSE v1 pre-authentication encoding.
	pae := fmt.Sprintf("DSSEv1 %d %s %d %s", len(env.PayloadType), env.PayloadType, len(payload), payload)
	if !ed25519.Verify(pub, []byte(pae), sig) {
		t.Error("signature does not verify over the DSSE PAE")
	}
	if got := string(record.PAE("http://example.com/HelloWorld", []byte("hello world"))); got != "DSSEv1 29 http://example.com/HelloWorld 11 hello world" {
		t.Errorf("PAE = %q", got)
	}

	// The payload is an in-toto Statement v1 about the tree.
	var raw struct {
		Type          string `json:"_type"`
		PredicateType string `json:"predicateType"`
		Subject       []struct {
			Name   string            `json:"name"`
			Digest map[string]string `json:"digest"`
		} `json:"subject"`
		Predicate map[string]any `json:"predicate"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Type != "https://in-toto.io/Statement/v1" {
		t.Errorf("_type = %q", raw.Type)
	}
	if raw.PredicateType != "https://github.com/jayehmke/prove/check/v0.1" {
		t.Errorf("predicateType = %q", raw.PredicateType)
	}
	digests := map[string]string{}
	for _, s := range raw.Subject {
		if s.Name == "" {
			t.Error("subject without a name")
		}
		for k, v := range s.Digest {
			digests[k] = v
		}
	}
	if digests["gitTree"] != tree || digests["gitCommit"] != commit {
		t.Errorf("subject digests = %v", digests)
	}
	if raw.Predicate["check"] != "unit" || raw.Predicate["command"] != "go test ./..." {
		t.Errorf("predicate = %v", raw.Predicate)
	}

	// Decode round-trips and rejects another predicate type.
	got, _, err := record.Decode(env)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tree() != tree || got.Commit() != commit || got.Predicate.Check != "unit" {
		t.Errorf("decoded = %+v", got)
	}
	other := st
	other.PredicateType = "https://example.com/other"
	otherEnv, err := record.Seal(other, edSigner{priv})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := record.Decode(otherEnv); err == nil {
		t.Error("Decode accepted a foreign predicate type")
	}
}
