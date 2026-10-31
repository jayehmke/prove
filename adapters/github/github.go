// Package github is prove-store-github, a prove store adapter that keeps
// each record as a comment on the GitHub commit it was made for, so the
// record shows on the commit and on any pull request that contains it, and
// a push needs no git ref. See docs/adapters.md.
//
// A comment holds the record's meta and its bytes (base64) in a fenced
// block under a marker line. The store is untrusted like every store:
// anyone who can comment can add a comment, but verify checks each
// record's signature, so a forged or altered record can make a check fail,
// never pass. Get returns a record only when its bytes hash to the id.
//
// Options (all optional):
//
//	repo:   owner/name (default: $GITHUB_REPOSITORY, else the origin remote)
//	api:    the API base URL (default: $GITHUB_API_URL, else https://api.github.com)
//	commit: where get looks first; prove fetch sets it to the commit it fetches
//
// The token is $GH_TOKEN or $GITHUB_TOKEN, else `gh auth token`.
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
)

// Program is the adapter's executable name.
const Program = "prove-store-github"

// Marker opens every record comment.
const Marker = "<!-- prove-record v1 -->"

var (
	idPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`) // a commit sha in practice; path-safe always
	remoteRepo  = regexp.MustCompile(`github\.com[:/]([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(\.git)?/?$`)
	fence       = regexp.MustCompile("(?s)```prove-record\n(.*?)\n```")
)

// Store keeps records as commit comments in one repository.
type Store struct {
	api, repo, token, commit string
	client                   *http.Client
}

// payload is what a comment carries.
type payload struct {
	Meta   store.Meta `json:"meta"`
	Record string     `json:"record"`
}

// New opens the store that store.options describe.
func New(ctx context.Context, options map[string]any) (*Store, error) {
	str := func(name string) (string, error) {
		v, ok := options[name]
		if !ok || v == nil {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("store.options.%s must be a string", name)
		}
		return s, nil
	}
	s := &Store{client: http.DefaultClient}
	var err error
	if s.repo, err = str("repo"); err != nil {
		return nil, err
	}
	if s.api, err = str("api"); err != nil {
		return nil, err
	}
	if s.commit, err = str("commit"); err != nil {
		return nil, err
	}
	if s.repo == "" {
		s.repo = os.Getenv("GITHUB_REPOSITORY")
	}
	if s.repo == "" {
		out, _ := exec.CommandContext(ctx, "git", "remote", "get-url", "origin").Output()
		if m := remoteRepo.FindStringSubmatch(strings.TrimSpace(string(out))); m != nil {
			s.repo = m[1]
		}
	}
	if !repoPattern.MatchString(s.repo) {
		return nil, errors.New("no GitHub repository: set store.options.repo (owner/name) or GITHUB_REPOSITORY")
	}
	if s.commit != "" && !shaPattern.MatchString(s.commit) {
		return nil, fmt.Errorf("store.options.commit %q is not a commit sha", s.commit)
	}
	if s.api == "" {
		s.api = os.Getenv("GITHUB_API_URL")
	}
	if s.api == "" {
		s.api = "https://api.github.com"
	}
	s.api = strings.TrimRight(s.api, "/")
	s.token = os.Getenv("GH_TOKEN")
	if s.token == "" {
		s.token = os.Getenv("GITHUB_TOKEN")
	}
	if s.token == "" {
		out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
		if err != nil {
			return nil, errors.New("no GitHub token: set GH_TOKEN or GITHUB_TOKEN, or sign in with gh")
		}
		s.token = strings.TrimSpace(string(out))
	}
	return s, nil
}

func (s *Store) do(ctx context.Context, method, url string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GitHub %s %s: %s: %s", method, strings.TrimPrefix(url, s.api), resp.Status, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// comments returns the record payloads in the comments of one commit, or of
// every commit when commit is "". A comment that isn't a readable record is
// skipped: it can only hide a record, never add one.
func (s *Store) comments(ctx context.Context, commit string) ([]payload, error) {
	url := s.api + "/repos/" + s.repo + "/comments?per_page=100"
	if commit != "" {
		url = s.api + "/repos/" + s.repo + "/commits/" + commit + "/comments?per_page=100"
	}
	var out []payload
	for url != "" {
		resp, err := s.do(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		var page []struct {
			Body string `json:"body"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("GitHub comments: %v", err)
		}
		for _, c := range page {
			if p, ok := parse(c.Body); ok {
				out = append(out, p)
			}
		}
		url = ""
		if m := nextLink.FindStringSubmatch(resp.Header.Get("Link")); m != nil {
			url = m[1]
		}
	}
	return out, nil
}

// parse reads a record comment. The record's bytes must hash to its id.
func parse(body string) (payload, bool) {
	if !strings.HasPrefix(body, Marker) {
		return payload{}, false
	}
	m := fence.FindStringSubmatch(body)
	if m == nil {
		return payload{}, false
	}
	var p payload
	if json.Unmarshal([]byte(m[1]), &p) != nil || !idPattern.MatchString(p.Meta.ID) {
		return payload{}, false
	}
	data, err := base64.StdEncoding.DecodeString(p.Record)
	if err != nil || record.ID(data) != p.Meta.ID {
		return payload{}, false
	}
	return p, true
}

// Body is the comment that holds a record.
func Body(meta store.Meta, data []byte) (string, error) {
	b, err := json.Marshal(payload{Meta: meta, Record: base64.StdEncoding.EncodeToString(data)})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\nprove record: `%s` %s, signed by %s (record %s)\n\n<details><summary>signed record</summary>\n\n```prove-record\n%s\n```\n</details>\n",
		Marker, meta.Check, meta.Result, meta.Signer, meta.ID[:12], b), nil
}

// Put comments the record on meta.Commit, unless that commit already has it.
func (s *Store) Put(ctx context.Context, meta store.Meta, data []byte) error {
	if !idPattern.MatchString(meta.ID) || record.ID(data) != meta.ID {
		return fmt.Errorf("bad record id %q", meta.ID)
	}
	if !shaPattern.MatchString(meta.Commit) {
		return fmt.Errorf("record %s has no commit sha", meta.ID[:12])
	}
	have, err := s.comments(ctx, meta.Commit)
	if err != nil {
		return err
	}
	for _, p := range have {
		if p.Meta.ID == meta.ID {
			return nil
		}
	}
	body, err := Body(meta, data)
	if err != nil {
		return err
	}
	resp, err := s.do(ctx, http.MethodPost, s.api+"/repos/"+s.repo+"/commits/"+meta.Commit+"/comments", map[string]string{"body": body})
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// Get returns the record whose bytes hash to id: from options.commit's
// comments when set, else from every commit comment in the repository.
func (s *Store) Get(ctx context.Context, id string) ([]byte, error) {
	if !idPattern.MatchString(id) {
		return nil, fmt.Errorf("bad record id %q", id)
	}
	scopes := []string{""}
	if s.commit != "" {
		scopes = []string{s.commit, ""}
	}
	for _, c := range scopes {
		ps, err := s.comments(ctx, c)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if p.Meta.ID == id {
				return base64.StdEncoding.DecodeString(p.Record)
			}
		}
	}
	return nil, fmt.Errorf("no record %s in %s's commit comments", id, s.repo)
}

// List returns the meta of the records matching q, one per id.
func (s *Store) List(ctx context.Context, q store.Query) ([]store.Meta, error) {
	ps, err := s.comments(ctx, q.Commit)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []store.Meta{}
	for _, p := range ps {
		if !seen[p.Meta.ID] && q.Match(p.Meta) {
			seen[p.Meta.ID] = true
			out = append(out, p.Meta)
		}
	}
	return out, nil
}

type request struct {
	Op     string         `json:"op"`
	Config map[string]any `json:"config"`
	ID     string         `json:"id"`
	Meta   *store.Meta    `json:"meta"`
	Record string         `json:"record"`
	Query  *store.Query   `json:"query"`
}

// Main runs one adapter request from in and writes the answer to out.
func Main(ctx context.Context, in io.Reader, out, stderr io.Writer) int {
	answer, err := serve(ctx, in)
	if err != nil {
		answer = map[string]string{"error": err.Error()}
	}
	if encErr := json.NewEncoder(out).Encode(answer); encErr != nil {
		fmt.Fprintln(stderr, Program+":", encErr)
		return 1
	}
	if err != nil {
		return 1
	}
	return 0
}

func serve(ctx context.Context, in io.Reader) (any, error) {
	var req request
	if err := json.NewDecoder(in).Decode(&req); err != nil {
		return nil, fmt.Errorf("bad request: %v", err)
	}
	s, err := New(ctx, req.Config)
	if err != nil {
		return nil, err
	}
	switch req.Op {
	case "put":
		if req.Meta == nil {
			return nil, errors.New("put without meta")
		}
		data, err := base64.StdEncoding.DecodeString(req.Record)
		if err != nil {
			return nil, fmt.Errorf("record is not base64: %v", err)
		}
		if err := s.Put(ctx, *req.Meta, data); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	case "get":
		data, err := s.Get(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"record": base64.StdEncoding.EncodeToString(data)}, nil
	case "list":
		var q store.Query
		if req.Query != nil {
			q = *req.Query
		}
		metas, err := s.List(ctx, q)
		if err != nil {
			return nil, err
		}
		return map[string]any{"records": metas}, nil
	default:
		return nil, fmt.Errorf("unknown op %q", req.Op)
	}
}
