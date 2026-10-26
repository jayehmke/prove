package record_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/testutil"
)

func TestRecordIsInTotoStatement(t *testing.T) {
	t.Run("AC-807-1 a record is an in-toto Statement v1 in a DSSE envelope about the git tree", func(t *testing.T) {
		testutil.Isolate(t)
		key := testutil.NewKey(t, "tester@example.com")
		const tree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
		const commit = "0123456789abcdef0123456789abcdef01234567"
		now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
		data, err := record.Seal(record.New(commit, tree, record.Predicate{
			Check: "unit", Command: "go test ./...", Inputs: []string{"**"},
			Fingerprint: "sha256:abc", Result: record.Pass, StartedAt: now, FinishedAt: now,
			Signer: "tester@example.com",
		}), key.Signer)
		if err != nil {
			t.Fatal(err)
		}

		// Read it as plain JSON, the way any DSSE / in-toto tool would.
		var env struct {
			PayloadType string `json:"payloadType"`
			Payload     string `json:"payload"`
			Signatures  []struct {
				KeyID string `json:"keyid"`
				Sig   string `json:"sig"`
			} `json:"signatures"`
		}
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatalf("envelope is not JSON: %v", err)
		}
		if env.PayloadType != "application/vnd.in-toto+json" {
			t.Fatalf("payloadType = %q", env.PayloadType)
		}
		if len(env.Signatures) != 1 || env.Signatures[0].Sig == "" || env.Signatures[0].KeyID == "" {
			t.Fatalf("want one signature with a keyid, got %+v", env.Signatures)
		}
		payload, err := base64.StdEncoding.DecodeString(env.Payload)
		if err != nil {
			t.Fatalf("payload is not base64: %v", err)
		}
		var st map[string]any
		if err := json.Unmarshal(payload, &st); err != nil {
			t.Fatalf("payload is not JSON: %v", err)
		}
		if st["_type"] != "https://in-toto.io/Statement/v1" {
			t.Fatalf("_type = %v", st["_type"])
		}
		if st["predicateType"] != "https://github.com/jayehmke/prove/check/v0.1" {
			t.Fatalf("predicateType = %v", st["predicateType"])
		}
		subjects, _ := st["subject"].([]any)
		if len(subjects) == 0 {
			t.Fatal("no subject")
		}
		first, _ := subjects[0].(map[string]any)
		digest, _ := first["digest"].(map[string]any)
		if digest["gitTree"] != tree {
			t.Fatalf("first subject digest = %v, want gitTree %s", digest, tree)
		}
		var sawCommit bool
		for _, s := range subjects {
			d, _ := s.(map[string]any)["digest"].(map[string]any)
			sawCommit = sawCommit || d["gitCommit"] == commit
		}
		if !sawCommit {
			t.Fatalf("subjects %v should include the commit", subjects)
		}
		if _, ok := st["predicate"].(map[string]any); !ok {
			t.Fatal("no predicate")
		}

		// The signature is over the DSSE pre-authentication encoding.
		sig, _ := base64.StdEncoding.DecodeString(env.Signatures[0].Sig)
		trust := testutil.Trust(t, map[string]testutil.Key{"tester": key})
		id, err := trust.Verify(context.Background(), env.Signatures[0].KeyID, record.PAE(env.PayloadType, payload), sig)
		if err != nil || id != "tester" {
			t.Fatalf("signature over PAE does not verify: %q %v", id, err)
		}

		// Open gives back the same statement.
		_, opened, err := record.Open(data)
		if err != nil {
			t.Fatal(err)
		}
		if opened.Tree() != tree || opened.Commit() != commit || opened.Predicate.Command != "go test ./..." {
			t.Fatalf("opened statement differs: %+v", opened)
		}
	})
}

func TestPAE(t *testing.T) {
	// The example from the DSSE spec.
	got := string(record.PAE("http://example.com/HelloWorld", []byte("hello world")))
	want := "DSSEv1 29 http://example.com/HelloWorld 11 hello world"
	if got != want {
		t.Fatalf("PAE = %q, want %q", got, want)
	}
}
