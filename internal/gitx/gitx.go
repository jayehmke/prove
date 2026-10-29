// Package gitx runs the git commands prove needs. It shells out to git so
// prove works with every repository layout git itself supports.
package gitx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Repo is a git working tree.
type Repo struct {
	// Dir is the top level of the working tree.
	Dir string
}

// Open finds the working tree that contains dir.
func Open(ctx context.Context, dir string) (*Repo, error) {
	r := &Repo{Dir: dir}
	top, err := r.Git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not inside a git repository: %w", err)
	}
	return &Repo{Dir: top}, nil
}

// Git runs git with args in the repository and returns trimmed stdout.
func (r *Repo) Git(ctx context.Context, args ...string) (string, error) {
	out, err := r.GitInput(ctx, nil, args...)
	return strings.TrimSpace(string(out)), err
}

// GitInput runs git with stdin and returns raw stdout.
func (r *Repo) GitInput(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// ResolveCommit turns a revision into a full commit hash.
func (r *Repo) ResolveCommit(ctx context.Context, rev string) (string, error) {
	out, err := r.Git(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil || out == "" {
		return "", fmt.Errorf("%q is not a commit in this repository", rev)
	}
	return out, nil
}

// TreeOf returns the tree hash of a commit.
func (r *Repo) TreeOf(ctx context.Context, commit string) (string, error) {
	return r.Git(ctx, "rev-parse", "--verify", "--end-of-options", commit+"^{tree}")
}

// DirtyPaths lists changed, staged and untracked (not ignored) paths.
func (r *Repo) DirtyPaths(ctx context.Context) ([]string, error) {
	out, err := r.GitInput(ctx, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var paths []string
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		paths = append(paths, f[3:])
		// Renames and copies are followed by the original path.
		if f[0] == 'R' || f[0] == 'C' {
			i++
		}
	}
	return paths, nil
}

// CommonDir returns the absolute git directory shared by all worktrees.
func (r *Repo) CommonDir(ctx context.Context) (string, error) {
	dir, err := r.Git(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(r.Dir, dir)
	}
	return dir, nil
}

// Checkout returns a directory holding commit: the working tree itself
// when HEAD is commit and the tree is clean, otherwise a temporary detached
// worktree. cleanup removes the temporary worktree.
func (r *Repo) Checkout(ctx context.Context, commit string) (dir string, cleanup func(), err error) {
	head, err := r.ResolveCommit(ctx, "HEAD")
	if err == nil && head == commit {
		if dirty, err := r.DirtyPaths(ctx); err == nil && len(dirty) == 0 {
			return r.Dir, func() {}, nil
		}
	}
	tmp, err := os.MkdirTemp("", "prove-tree-")
	if err != nil {
		return "", nil, err
	}
	dir = filepath.Join(tmp, "tree")
	if _, err := r.Git(ctx, "worktree", "add", "--detach", dir, commit); err != nil {
		_ = os.RemoveAll(tmp)
		return "", nil, err
	}
	return dir, func() {
		_, _ = r.Git(context.Background(), "worktree", "remove", "--force", dir)
		_ = os.RemoveAll(tmp)
	}, nil
}

// MergeOnto returns the commit that merging commit into onto makes: commit
// itself when it already contains onto, otherwise a merge commit with
// parents onto and commit. The merge commit is made with a fixed identity
// and date, so the same pair always gives the same commit, and it touches
// no ref, HEAD, index or working tree. A conflict is an error.
func (r *Repo) MergeOnto(ctx context.Context, commit, onto string) (string, error) {
	commit, err := r.ResolveCommit(ctx, commit)
	if err != nil {
		return "", err
	}
	base, err := r.ResolveCommit(ctx, onto)
	if err != nil {
		return "", err
	}
	if _, err := r.Git(ctx, "merge-base", "--is-ancestor", base, commit); err == nil {
		return commit, nil
	}
	out, err := r.Git(ctx, "merge-tree", "--write-tree", "--no-messages", base, commit)
	if err != nil {
		return "", fmt.Errorf("%s does not merge cleanly onto %s: %w", short(commit), onto, err)
	}
	tree, _, _ := strings.Cut(out, "\n")
	cmd := exec.CommandContext(ctx, "git", "commit-tree", "--no-gpg-sign", tree, "-p", base, "-p", commit,
		"-m", "prove: "+commit+" merged onto "+base)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=prove", "GIT_AUTHOR_EMAIL=prove@localhost", "GIT_AUTHOR_DATE=@0 +0000",
		"GIT_COMMITTER_NAME=prove", "GIT_COMMITTER_EMAIL=prove@localhost", "GIT_COMMITTER_DATE=@0 +0000")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	merged, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git commit-tree: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(merged)), nil
}

func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// Entry is one file in a tree.
type Entry struct {
	Mode string
	Type string // blob or commit (a submodule)
	OID  string
	Path string
}

// ListTree lists every file in a tree, recursively, in git's path order.
func (r *Repo) ListTree(ctx context.Context, tree string) ([]Entry, error) {
	out, err := r.GitInput(ctx, nil, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, line := range strings.Split(string(out), "\x00") {
		if line == "" {
			continue
		}
		meta, path, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("unexpected ls-tree line %q", line)
		}
		f := strings.Fields(meta)
		if len(f) != 3 {
			return nil, fmt.Errorf("unexpected ls-tree line %q", line)
		}
		entries = append(entries, Entry{Mode: f[0], Type: f[1], OID: f[2], Path: path})
	}
	return entries, nil
}

// ReadBlobs streams the contents of objects through one git cat-file
// process and calls fn for each, in the order given. fn gets nil data for
// an object that does not exist.
func (r *Repo) ReadBlobs(ctx context.Context, objects []string, fn func(i int, data []byte) error) error {
	if len(objects) == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "cat-file", "--batch")
	cmd.Dir = r.Dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	writeErr := make(chan error, 1)
	go func() {
		w := bufio.NewWriter(stdin)
		for _, o := range objects {
			if _, err := w.WriteString(o + "\n"); err != nil {
				writeErr <- err
				return
			}
		}
		err := w.Flush()
		if cerr := stdin.Close(); err == nil {
			err = cerr
		}
		writeErr <- err
	}()
	br := bufio.NewReader(stdout)
	var readErr error
	for i := range objects {
		header, err := br.ReadString('\n')
		if err != nil {
			readErr = fmt.Errorf("git cat-file: %w", err)
			break
		}
		f := strings.Fields(header)
		if len(f) == 2 && f[1] == "missing" {
			if err := fn(i, nil); err != nil {
				readErr = err
				break
			}
			continue
		}
		if len(f) != 3 {
			readErr = fmt.Errorf("git cat-file: unexpected header %q", header)
			break
		}
		size, err := strconv.Atoi(f[2])
		if err != nil {
			readErr = fmt.Errorf("git cat-file: bad size in %q", header)
			break
		}
		data := make([]byte, size+1) // contents plus the trailing newline
		if _, err := io.ReadFull(br, data); err != nil {
			readErr = fmt.Errorf("git cat-file: %w", err)
			break
		}
		if err := fn(i, data[:size]); err != nil {
			readErr = err
			break
		}
	}
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return readErr
	}
	if err := <-writeErr; err != nil {
		_ = cmd.Wait()
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git cat-file: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ErrNoCommits is returned when HEAD has no commit yet.
var ErrNoCommits = errors.New("the repository has no commits yet")
