package signer_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/signer"
	"github.com/jayehmke/prove/internal/testutil"
)

// fakeSigner "signs" with a keyed sha256, enough to exercise the protocol.
const fakeSigner = `#!/bin/sh
req=$(cat)
field() { printf '%s' "$req" | sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }
mac() { { field payload | base64 -d; printf secret; } | sha256sum | cut -d' ' -f1 | tr -d '\n' | base64 -w0; }
case $(field op) in
  key) echo '{"keyid":"fake-key-1"}' ;;
  sign) printf '{"keyid":"fake-key-1","sig":"%s"}\n' "$(mac)" ;;
  verify) if [ "$(field sig)" = "$(mac)" ]; then echo '{"ok":true}'; else echo '{"ok":false,"reason":"mac mismatch"}'; fi ;;
esac
`

func TestExternalSigner(t *testing.T) {
	testutil.Isolate(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "prove-signer-fake"), []byte(fakeSigner), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx := context.Background()

	s := signer.NewExternal(ctx, "fake", "ci@example.com")
	keyID, sig, err := s.Sign([]byte("hello"))
	if err != nil || keyID != "fake-key-1" {
		t.Fatalf("sign: %q %v", keyID, err)
	}
	key, err := s.Key()
	if err != nil || key != (config.SignerKey{Type: "fake", Key: "fake-key-1"}) {
		t.Fatalf("key: %+v %v", key, err)
	}
	trust, err := signer.NewTrustSet(map[string]config.SignerKey{"ci": key})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := trust.Verify(ctx, keyID, []byte("hello"), sig); err != nil || id != "ci" {
		t.Fatalf("verify: %q %v", id, err)
	}
	if _, err := trust.Verify(ctx, keyID, []byte("hellO"), sig); err == nil || !strings.Contains(err.Error(), "mac mismatch") {
		t.Fatalf("verify of a changed message: %v", err)
	}
	if _, err := trust.Verify(ctx, "other-key", []byte("hello"), sig); err != signer.ErrUntrusted {
		t.Fatalf("unknown key id: %v", err)
	}
}

func TestHomeKeyFallback(t *testing.T) {
	// A key in ~/.ssh, and no PROVE_SSH_KEY or git signing key.
	setup := func(t *testing.T) (home string, key testutil.Key) {
		home = testutil.Isolate(t)
		key = testutil.NewKey(t, "dev@example.com")
		data, err := os.ReadFile(key.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		return home, key
	}
	ctx := context.Background()

	t.Run("AC-938-3 off CI it signs with the ~/.ssh key and says which", func(t *testing.T) {
		home, key := setup(t)
		var stderr bytes.Buffer
		s, err := signer.FromEnv(ctx, nil, &stderr)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Key(); got.Key != key.Public {
			t.Fatalf("signed with %q, want the ~/.ssh key %q", got.Key, key.Public)
		}
		if want := filepath.Join(home, ".ssh", "id_ed25519"); !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr should name %s: %q", want, stderr.String())
		}
	})

	t.Run("AC-938-3 with CI=true it refuses the ~/.ssh key", func(t *testing.T) {
		setup(t)
		t.Setenv("CI", "true")
		var stderr bytes.Buffer
		if s, err := signer.FromEnv(ctx, nil, &stderr); err == nil || !strings.Contains(err.Error(), "PROVE_SSH_KEY") {
			t.Fatalf("CI=true signed with the ~/.ssh key (%v, %v)", s, err)
		}
	})

	t.Run("AC-938-3 with CI=true and PROVE_SSH_KEY set it signs with that key", func(t *testing.T) {
		setup(t)
		t.Setenv("CI", "true")
		job := testutil.NewKey(t, "ci@example.com")
		t.Setenv("PROVE_SSH_KEY", job.Path)
		var stderr bytes.Buffer
		s, err := signer.FromEnv(ctx, nil, &stderr)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Key(); got.Key != job.Public {
			t.Fatalf("signed with %q, want PROVE_SSH_KEY's %q", got.Key, job.Public)
		}
	})
}

func TestSSHSignatureIsBoundToItsKey(t *testing.T) {
	testutil.Isolate(t)
	alice := testutil.NewKey(t, "alice")
	bob := testutil.NewKey(t, "bob")
	trust := testutil.Trust(t, map[string]testutil.Key{"alice": alice, "bob": bob})
	ctx := context.Background()
	keyID, sig, err := alice.Signer.Sign([]byte("msg"))
	if err != nil {
		t.Fatal(err)
	}
	if id, err := trust.Verify(ctx, keyID, []byte("msg"), sig); err != nil || id != "alice" {
		t.Fatalf("alice: %q %v", id, err)
	}
	bobID, _, _ := bob.Signer.Sign([]byte("msg"))
	if _, err := trust.Verify(ctx, bobID, []byte("msg"), sig); err == nil {
		t.Fatal("alice's signature verified as bob's")
	}
}

// TestSSHSIGInteropWithSSHKeygen checks prove's signatures against OpenSSH
// in both directions.
func TestSSHSIGInteropWithSSHKeygen(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen is not installed")
	}
	testutil.Isolate(t)
	key := testutil.NewKey(t, "alice")
	dir := t.TempDir()
	msg := []byte("DSSEv1 28 application/vnd.in-toto+json 2 {}")
	msgPath := filepath.Join(dir, "msg")
	if err := os.WriteFile(msgPath, msg, 0o600); err != nil {
		t.Fatal(err)
	}

	// prove signs, ssh-keygen verifies.
	keyID, sig, err := key.Signer.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	sigPath := filepath.Join(dir, "prove.sig")
	armored := "-----BEGIN SSH SIGNATURE-----\n"
	b64 := base64.StdEncoding.EncodeToString(sig)
	for len(b64) > 70 {
		armored += b64[:70] + "\n"
		b64 = b64[70:]
	}
	armored += b64 + "\n-----END SSH SIGNATURE-----\n"
	if err := os.WriteFile(sigPath, []byte(armored), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(keygen, "-Y", "check-novalidate", "-n", "prove", "-s", sigPath)
	cmd.Stdin = bytes.NewReader(msg)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen rejected prove's signature: %v\n%s", err, out)
	}

	// ssh-keygen signs, prove verifies.
	cmd = exec.Command(keygen, "-Y", "sign", "-n", "prove", "-f", key.Path, msgPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen sign: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(msgPath + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.NewReplacer("-----BEGIN SSH SIGNATURE-----", "", "-----END SSH SIGNATURE-----", "", "\n", "").Replace(string(raw))
	theirs, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatal(err)
	}
	trust := testutil.Trust(t, map[string]testutil.Key{"alice": key})
	if id, err := trust.Verify(context.Background(), keyID, msg, theirs); err != nil || id != "alice" {
		t.Fatalf("prove rejected ssh-keygen's signature: %q %v", id, err)
	}
}
