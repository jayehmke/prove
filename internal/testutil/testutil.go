// Package testutil builds throwaway git repositories and signing keys for
// tests.
package testutil

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/signer"
	"golang.org/x/crypto/ssh"
)

// Isolate points HOME and git's global config at a temp folder and clears
// the PROVE_ variables, so tests never read the developer's setup.
func Isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	global := filepath.Join(home, ".gitconfig")
	cfg := "[user]\n\tname = Prove Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n[commit]\n\tgpgsign = false\n"
	if err := os.WriteFile(global, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"PROVE_SIGNER", "PROVE_SSH_KEY", "PROVE_IDENTITY", "SSH_AUTH_SOCK"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	return home
}

// NewRepo creates a repository with one commit containing README.md.
func NewRepo(t *testing.T) *gitx.Repo {
	t.Helper()
	Isolate(t)
	dir := t.TempDir()
	Git(t, dir, "init", "-q")
	r := &gitx.Repo{Dir: dir}
	WriteFile(t, r, "README.md", "hello\n")
	Commit(t, r, "initial")
	return r
}

// Git runs git in dir and fails the test on error.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// WriteFile writes a file in the working tree, creating folders.
func WriteFile(t *testing.T, r *gitx.Repo, path, content string) {
	t.Helper()
	full := filepath.Join(r.Dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Commit commits every change and returns the commit hash.
func Commit(t *testing.T, r *gitx.Repo, msg string) string {
	t.Helper()
	Git(t, r.Dir, "add", "-A")
	Git(t, r.Dir, "commit", "-q", "--allow-empty", "-m", msg)
	return Head(t, r)
}

// Head returns HEAD's commit hash.
func Head(t *testing.T, r *gitx.Repo) string {
	t.Helper()
	c, err := r.ResolveCommit(context.Background(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Tree returns the tree hash of a commit.
func Tree(t *testing.T, r *gitx.Repo, commit string) string {
	t.Helper()
	tree, err := r.TreeOf(context.Background(), commit)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// Key is a generated ed25519 signing key.
type Key struct {
	Signer *signer.SSH
	// Path is the private key file in OpenSSH form.
	Path string
	// Public is the public key in authorized_keys form.
	Public string
}

// TrustedKey is the policy entry that trusts the key.
func (k Key) TrustedKey() config.SignerKey {
	return config.SignerKey{Type: "ssh", Key: k.Public}
}

// NewKey generates a key that claims identity and writes it to a temp file.
func NewKey(t *testing.T, identity string) Key {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, identity)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := signer.LoadSSH(path, identity)
	if err != nil {
		t.Fatal(err)
	}
	return Key{Signer: s, Path: path, Public: s.PublicKey()}
}

// Trust builds a trust set from identity → key.
func Trust(t *testing.T, keys map[string]Key) *signer.TrustSet {
	t.Helper()
	m := map[string]config.SignerKey{}
	for id, k := range keys {
		m[id] = k.TrustedKey()
	}
	ts, err := signer.NewTrustSet(m)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
