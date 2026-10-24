package store

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jayehmke/prove/internal/gitx"
)

// The git store keeps each record as a parentless commit whose tree holds
// entry.json, at refs/prove/records/<commit>/<id>. Commits (not bare blobs)
// keep the refs pushable to any host. Records travel with
//
//	git push origin 'refs/prove/*:refs/prove/*'
//	git fetch origin 'refs/prove/*:refs/prove/*'
//
// and never touch a branch.
type gitStore struct{ repo *gitx.Repo }

const recordsPrefix = RefPrefix + "records/"

// NewGit returns a store that keeps records under refs/prove/ in repo.
func NewGit(repo *gitx.Repo) Store { return &gitStore{repo: repo} }

func (s *gitStore) Put(e Entry) error {
	if !validID(e.ID) {
		return fmt.Errorf("git store: bad record id %q", e.ID)
	}
	if !validHex(e.Commit) {
		return fmt.Errorf("git store: bad commit %q", e.Commit)
	}
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	blob, err := s.repo.RunInput(b, "hash-object", "-w", "--stdin")
	if err != nil {
		return err
	}
	tree, err := s.repo.RunInput([]byte(fmt.Sprintf("100644 blob %s\tentry.json\n", strings.TrimSpace(string(blob)))), "mktree")
	if err != nil {
		return err
	}
	// A fixed identity and date: the same entry always makes the same commit.
	cmd := exec.Command("git", "commit-tree", "-m", "prove record "+e.Check+" "+e.ID, strings.TrimSpace(string(tree)))
	cmd.Dir = s.repo.Dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=prove", "GIT_AUTHOR_EMAIL=prove@localhost", "GIT_AUTHOR_DATE=@0 +0000",
		"GIT_COMMITTER_NAME=prove", "GIT_COMMITTER_EMAIL=prove@localhost", "GIT_COMMITTER_DATE=@0 +0000",
	)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git commit-tree: %v", err)
	}
	_, err = s.repo.Run("update-ref", recordsPrefix+e.Commit+"/"+e.ID, strings.TrimSpace(string(out)))
	return err
}

func validHex(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *gitStore) read(refs []gitx.Ref) ([]Entry, error) {
	revs := make([]string, len(refs))
	for i, r := range refs {
		revs[i] = r.Hash + ":entry.json"
	}
	var out []Entry
	err := s.repo.ReadObjects(revs, func(rev string, content []byte, err error) error {
		if err != nil {
			return fmt.Errorf("git store: %s: %v", rev, err)
		}
		var e Entry
		if err := json.Unmarshal(content, &e); err != nil {
			return fmt.Errorf("git store: %s is not a record: %v", rev, err)
		}
		out = append(out, e)
		return nil
	})
	return out, err
}

func (s *gitStore) Get(id string) (Entry, error) {
	if !validID(id) {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	refs, err := s.repo.ForEachRef(recordsPrefix)
	if err != nil {
		return Entry{}, err
	}
	for _, r := range refs {
		if strings.HasSuffix(r.Name, "/"+id) {
			entries, err := s.read([]gitx.Ref{r})
			if err != nil {
				return Entry{}, err
			}
			return entries[0], nil
		}
	}
	return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

func (s *gitStore) List(f Filter) ([]Entry, error) {
	prefix := recordsPrefix
	if f.Commit != "" {
		if !validHex(f.Commit) {
			return nil, nil
		}
		prefix += f.Commit + "/"
	}
	refs, err := s.repo.ForEachRef(prefix)
	if err != nil {
		return nil, err
	}
	entries, err := s.read(refs)
	if err != nil {
		return nil, err
	}
	return filterEntries(entries, f), nil
}
