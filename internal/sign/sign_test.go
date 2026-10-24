package sign_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/sign"
	"github.com/jayehmke/prove/internal/testutil"
)

func sigOf(t *testing.T, s record.Signer, msg []byte) record.Signature {
	t.Helper()
	keyID, sig, err := s.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	return record.Signature{KeyID: keyID, Sig: base64.StdEncoding.EncodeToString(sig)}
}

func TestTrustIdentifies(t *testing.T) {
	alice, mallory := testutil.NewKey(t), testutil.NewKey(t)
	trust, err := sign.NewTrust([]config.Signer{{Identity: "alice", Key: alice.Public}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("hello")
	if id, err := trust.Identify(msg, sigOf(t, alice.Signer, msg)); err != nil || id != "alice" {
		t.Errorf("Identify(alice) = %q, %v", id, err)
	}
	var untrusted *sign.UntrustedError
	if _, err := trust.Identify(msg, sigOf(t, mallory.Signer, msg)); !errors.As(err, &untrusted) {
		t.Errorf("Identify(mallory) = %v, want UntrustedError", err)
	}
	var bad *sign.SignatureError
	if _, err := trust.Identify([]byte("other"), sigOf(t, alice.Signer, msg)); !errors.As(err, &bad) {
		t.Errorf("Identify(other message) = %v, want SignatureError", err)
	}
	if _, err := sign.NewTrust([]config.Signer{{Identity: "x", Key: "not a key"}}, nil); err == nil {
		t.Error("NewTrust accepted a bad key")
	}
}

// ssh-keygen -Y verify accepts prove's signatures: the format is SSHSIG.
func TestSSHSigInteropWithSSHKeygen(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	key := testutil.NewKey(t)
	msg := []byte("a message")
	_, sig, err := key.Signer.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	b64 := base64.StdEncoding.EncodeToString(sig)
	var armored strings.Builder
	armored.WriteString("-----BEGIN SSH SIGNATURE-----\n")
	for len(b64) > 70 {
		armored.WriteString(b64[:70] + "\n")
		b64 = b64[70:]
	}
	armored.WriteString(b64 + "\n-----END SSH SIGNATURE-----\n")
	sigPath := filepath.Join(dir, "msg.sig")
	allowed := filepath.Join(dir, "allowed")
	if err := os.WriteFile(sigPath, []byte(armored.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(allowed, []byte("alice "+key.Public+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed, "-I", "alice", "-n", sign.Namespace, "-s", sigPath)
	cmd.Stdin = strings.NewReader(string(msg))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen -Y verify: %v\n%s", err, out)
	}
}

func TestSignWithAgent(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "agent.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { _ = agent.ServeAgent(keyring, c) }()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sock)

	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	pubPath := filepath.Join(t.TempDir(), "id.pub")
	if err := os.WriteFile(pubPath, ssh.MarshalAuthorizedKey(pub), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []string{pubPath, "key::" + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))} {
		s, err := sign.LoadSSH(spec)
		if err != nil {
			t.Fatalf("LoadSSH(%s): %v", spec, err)
		}
		_, sig, err := s.Sign([]byte("m"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := sign.VerifySSH([]byte("m"), sig)
		if err != nil || sign.KeyID(got) != sign.KeyID(pub) {
			t.Errorf("agent signature: %v", err)
		}
	}
}

// A fake signer whose "signature" is the payload itself: enough to test the
// protocol, not the crypto.
const fakeSigner = `#!/bin/sh
req=$(cat)
op=$(printf '%s' "$req" | sed -n 's/.*"op":"\([a-z]*\)".*/\1/p')
payload=$(printf '%s' "$req" | sed -n 's/.*"payload":"\([^"]*\)".*/\1/p')
if [ "$op" = sign ]; then
  printf '{"keyid":"fake:1","sig":"%s"}' "$payload"
else
  sig=$(printf '%s' "$req" | sed -n 's/.*"sig":"\([^"]*\)".*/\1/p')
  if [ "$sig" = "$payload" ]; then echo '{"ok":true}'; else echo '{"ok":false}'; fi
fi
`

func TestExternalSignerProtocol(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prove-signer-fake"), []byte(fakeSigner), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := &sign.External{Name: "fake", Dir: dir}
	msg := []byte("DSSEv1 payload")
	sig := sigOf(t, s, msg)
	if sig.KeyID != "fake:1" {
		t.Fatalf("keyid = %q", sig.KeyID)
	}
	trust, err := sign.NewTrust([]config.Signer{{Identity: "ci", Key: "fake:1", Adapter: "fake"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	trust.WithDir(dir)
	if id, err := trust.Identify(msg, sig); err != nil || id != "ci" {
		t.Errorf("Identify = %q, %v", id, err)
	}
	var bad *sign.SignatureError
	if _, err := trust.Identify([]byte("tampered"), sig); !errors.As(err, &bad) {
		t.Errorf("Identify(tampered) = %v, want SignatureError", err)
	}
}
