// Package testutil builds throwaway git repositories and keys for tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jayehmke/prove/internal/gitx"
	"github.com/jayehmke/prove/internal/sign"
)

// Repo creates an empty repository with a committer identity.
func Repo(t testing.TB) *gitx.Repo {
	t.Helper()
	dir := t.TempDir()
	repo := &gitx.Repo{Dir: dir}
	Git(t, repo, "init", "-q", "-b", "main")
	Git(t, repo, "config", "user.name", "Test")
	Git(t, repo, "config", "user.email", "test@example.com")
	Git(t, repo, "config", "commit.gpgsign", "false")
	return repo
}

// Git runs git in repo and fails the test on error.
func Git(t testing.TB, repo *gitx.Repo, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Write writes a file relative to the repository root.
func Write(t testing.TB, repo *gitx.Repo, rel, content string) {
	t.Helper()
	p := filepath.Join(repo.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Commit commits everything and returns the new HEAD.
func Commit(t testing.TB, repo *gitx.Repo, msg string) string {
	t.Helper()
	Git(t, repo, "add", "-A")
	Git(t, repo, "commit", "-q", "--allow-empty", "-m", msg)
	return Git(t, repo, "rev-parse", "HEAD")
}

// Key is a generated signing key.
type Key struct {
	Path   string // private key file
	Public string // authorized_keys line
	Signer *sign.SSH
}

// NewKey generates an ed25519 signing key.
func NewKey(t testing.TB) Key {
	t.Helper()
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := sign.GenerateKey(path); err != nil {
		t.Fatal(err)
	}
	s, err := sign.LoadSSH(path)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(path + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	return Key{Path: path, Public: strings.TrimSpace(string(pub)), Signer: s}
}
