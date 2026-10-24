// Package gitx runs the git commands prove needs. Everything goes through the
// git binary so prove reads exactly what git sees, with no second
// implementation of the object format.
package gitx

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// Repo is a git working tree.
type Repo struct {
	// Dir is the top level of the working tree.
	Dir string
}

// Open finds the repository that contains dir.
func Open(dir string) (*Repo, error) {
	out, err := run(dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not inside a git repository: %w", err)
	}
	return &Repo{Dir: strings.TrimSpace(string(out))}, nil
}

func run(dir string, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

// Run runs git in the repository and returns stdout with surrounding
// whitespace trimmed.
func (r *Repo) Run(args ...string) (string, error) {
	out, err := run(r.Dir, nil, args...)
	return strings.TrimSpace(string(out)), err
}

// RunInput runs git with stdin and returns raw stdout.
func (r *Repo) RunInput(stdin []byte, args ...string) ([]byte, error) {
	return run(r.Dir, stdin, args...)
}

// ResolveCommit turns a revision into a full commit hash.
func (r *Repo) ResolveCommit(rev string) (string, error) {
	if rev == "" {
		rev = "HEAD"
	}
	return r.Run("rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

// TreeOf returns the tree hash of a revision.
func (r *Repo) TreeOf(rev string) (string, error) {
	return r.Run("rev-parse", "--verify", "--quiet", rev+"^{tree}")
}

// GitDir returns the absolute path of the .git directory.
func (r *Repo) GitDir() (string, error) {
	return r.Run("rev-parse", "--absolute-git-dir")
}

// Dirty lists uncommitted changes, untracked files included, in
// `git status --porcelain` form. Ignored files don't count.
func (r *Repo) Dirty() ([]string, error) {
	out, err := run(r.Dir, nil, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// Entry is one file in a tree listing.
type Entry struct {
	Mode string
	Type string // blob or commit (a submodule)
	Hash string
	Path string
}

// ListTree lists every file in the tree of rev, recursively.
func (r *Repo) ListTree(rev string) ([]Entry, error) {
	out, err := run(r.Dir, nil, "ls-tree", "-r", "-z", "--full-tree", rev)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, path, ok := bytes.Cut(rec, []byte{'\t'})
		if !ok {
			return nil, fmt.Errorf("git ls-tree: unexpected line %q", rec)
		}
		f := strings.Fields(string(meta))
		if len(f) != 3 {
			return nil, fmt.Errorf("git ls-tree: unexpected line %q", rec)
		}
		entries = append(entries, Entry{Mode: f[0], Type: f[1], Hash: f[2], Path: string(path)})
	}
	return entries, nil
}

// ErrMissing is returned by ReadObjects when an object doesn't exist.
var ErrMissing = errors.New("object missing")

// ReadObjects reads the contents of each object (a hash or any revision
// expression such as <commit>:<path>) with one `git cat-file --batch`, and
// calls fn for each in order. A missing object is passed to fn as ErrMissing.
func (r *Repo) ReadObjects(revs []string, fn func(rev string, content []byte, err error) error) error {
	if len(revs) == 0 {
		return nil
	}
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = r.Dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		w := bufio.NewWriter(stdin)
		for _, rev := range revs {
			fmt.Fprintln(w, rev)
		}
		w.Flush()
		stdin.Close()
	}()
	br := bufio.NewReader(stdout)
	var fnErr error
	for _, rev := range revs {
		header, err := br.ReadString('\n')
		if err != nil {
			fnErr = fmt.Errorf("git cat-file: %v: %s", err, stderr.String())
			break
		}
		header = strings.TrimSuffix(header, "\n")
		if strings.HasSuffix(header, " missing") || strings.HasSuffix(header, " ambiguous") {
			if fnErr == nil {
				fnErr = fn(rev, nil, ErrMissing)
			}
			continue
		}
		f := strings.Fields(header)
		if len(f) != 3 {
			fnErr = fmt.Errorf("git cat-file: unexpected header %q", header)
			break
		}
		size, err := strconv.Atoi(f[2])
		if err != nil {
			fnErr = fmt.Errorf("git cat-file: unexpected header %q", header)
			break
		}
		content := make([]byte, size+1) // trailing newline
		if _, err := io.ReadFull(br, content); err != nil {
			fnErr = fmt.Errorf("git cat-file: %v", err)
			break
		}
		if fnErr == nil {
			fnErr = fn(rev, content[:size], nil)
		}
	}
	_, _ = io.Copy(io.Discard, br)
	waitErr := cmd.Wait()
	if fnErr != nil {
		return fnErr
	}
	if waitErr != nil {
		return fmt.Errorf("git cat-file: %v: %s", waitErr, stderr.String())
	}
	return nil
}

// ReadFile reads path at rev. ok is false when the file doesn't exist there.
func (r *Repo) ReadFile(rev, path string) (content []byte, ok bool, err error) {
	err = r.ReadObjects([]string{rev + ":" + path}, func(_ string, c []byte, e error) error {
		if errors.Is(e, ErrMissing) {
			return nil
		}
		content, ok = c, true
		return e
	})
	return content, ok, err
}

// Ref is a ref name and the object it points at.
type Ref struct {
	Name string
	Hash string
}

// ForEachRef lists refs under prefix (for example refs/prove/).
func (r *Repo) ForEachRef(prefix string) ([]Ref, error) {
	out, err := r.Run("for-each-ref", "--format=%(objectname) %(refname)", prefix)
	if err != nil {
		return nil, err
	}
	var refs []Ref
	for _, l := range strings.Split(out, "\n") {
		hash, name, ok := strings.Cut(l, " ")
		if ok {
			refs = append(refs, Ref{Name: name, Hash: hash})
		}
	}
	return refs, nil
}
