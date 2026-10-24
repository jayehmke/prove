package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/jayehmke/prove/internal/gitx"
)

// RefPrefix is where the git store keeps records. Nothing else is written:
// no branch, no index, no working tree file.
const RefPrefix = "refs/prove/records/"

// Git keeps each record as a commit under refs/prove/records/<id> whose
// tree holds record.json and meta.json. Share records with
// `git push origin 'refs/prove/*:refs/prove/*'` and the matching fetch.
type Git struct {
	Repo *gitx.Repo
}

// NewGit returns the store that keeps records under refs/prove/.
func NewGit(repo *gitx.Repo) Store { return &Git{Repo: repo} }

// Name describes the store.
func (g *Git) Name() string { return "git store (" + RefPrefix + ")" }

// Put writes the record commit and its ref.
func (g *Git) Put(ctx context.Context, meta Meta, record []byte) error {
	if err := checkID(meta.ID); err != nil {
		return err
	}
	ref := RefPrefix + meta.ID
	if _, err := g.Repo.Git(ctx, "rev-parse", "--verify", "--quiet", ref); err == nil {
		return nil // content-addressed: already stored
	}
	m, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	recBlob, err := g.blob(ctx, record)
	if err != nil {
		return err
	}
	metaBlob, err := g.blob(ctx, m)
	if err != nil {
		return err
	}
	tree, err := g.Repo.GitInput(ctx, []byte(fmt.Sprintf("100644 blob %s\tmeta.json\n100644 blob %s\trecord.json\n", metaBlob, recBlob)), "mktree")
	if err != nil {
		return err
	}
	commit, err := g.commitTree(ctx, strings.TrimSpace(string(tree)), "prove record "+meta.Check+" "+meta.ID)
	if err != nil {
		return err
	}
	_, err = g.Repo.Git(ctx, "update-ref", ref, commit, "")
	return err
}

func (g *Git) blob(ctx context.Context, data []byte) (string, error) {
	out, err := g.Repo.GitInput(ctx, data, "hash-object", "-w", "--stdin")
	return strings.TrimSpace(string(out)), err
}

// commitTree makes a parentless commit with a fixed identity, so it needs
// no user config and never touches HEAD.
func (g *Git) commitTree(ctx context.Context, tree, msg string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "commit-tree", "--no-gpg-sign", tree, "-m", msg)
	cmd.Dir = g.Repo.Dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=prove", "GIT_AUTHOR_EMAIL=prove@localhost",
		"GIT_COMMITTER_NAME=prove", "GIT_COMMITTER_EMAIL=prove@localhost")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git commit-tree: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Get reads a record.
func (g *Git) Get(ctx context.Context, id string) ([]byte, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	data, err := g.Repo.GitInput(ctx, nil, "cat-file", "blob", RefPrefix+id+":record.json")
	if err != nil {
		return nil, fmt.Errorf("record %s is not in the %s", id, g.Name())
	}
	return data, nil
}

// List reads the metadata of every record ref.
func (g *Git) List(ctx context.Context, q Query) ([]Meta, error) {
	out, err := g.Repo.Git(ctx, "for-each-ref", "--format=%(refname)", RefPrefix)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, r := range strings.Split(out, "\n") {
		if r != "" {
			refs = append(refs, r+":meta.json")
		}
	}
	var metas []Meta
	err = g.Repo.ReadBlobs(ctx, refs, func(i int, data []byte) error {
		var m Meta
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("%s: %v", refs[i], err)
		}
		if q.Match(m) {
			metas = append(metas, m)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].ID < metas[j].ID })
	return metas, nil
}
