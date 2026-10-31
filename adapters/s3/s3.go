// Package s3 is prove-store-s3, a prove store adapter that keeps records
// in an S3 bucket. See docs/adapters.md.
//
// Each record is stored as <prefix>/<check>/<fingerprint>/<id>.json, by
// the check's inputs and not by commit, so a record reused on a later
// commit (or on the squash merge of a pull request) is the same object.
// An index entry <prefix>/index/<id>.json points to it. Every write is
// conditional (If-None-Match: *), so the adapter never replaces an
// object: if another object already holds a record's key, the record goes
// to <id>.2.json, then <id>.3.json. Turn on bucket versioning as well, so
// a writer with other tools can't destroy records either.
//
// Credentials come from the standard AWS chain (environment, shared
// config, SSO, web identity for OIDC in CI, instance roles).
package s3

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
)

// Program is the adapter's executable name.
const Program = "prove-store-s3"

var idPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// maxSuffix bounds the keys tried for one record.
const maxSuffix = 1000

// Store keeps records in one bucket under one prefix.
type Store struct {
	client *s3.Client
	bucket string
	prefix string // "" or ending in "/"
}

// New opens the store that store.options describe:
//
//	bucket:     the bucket (required)
//	prefix:     key prefix (default "prove")
//	region:     the bucket's region (default: from the AWS chain)
//	endpoint:   an S3-compatible endpoint URL, such as MinIO
//	path_style: true for path-style addressing, which most S3-compatible
//	            servers need
func New(ctx context.Context, options map[string]any) (*Store, error) {
	str := func(name, def string) (string, error) {
		v, ok := options[name]
		if !ok || v == nil {
			return def, nil
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("store.options.%s must be a string", name)
		}
		return s, nil
	}
	bucket, err := str("bucket", "")
	if err != nil {
		return nil, err
	}
	if bucket == "" {
		return nil, errors.New("set store.options.bucket in .prove.yaml")
	}
	prefix, err := str("prefix", "prove")
	if err != nil {
		return nil, err
	}
	if prefix = strings.Trim(prefix, "/"); prefix != "" {
		prefix += "/"
	}
	region, err := str("region", "")
	if err != nil {
		return nil, err
	}
	endpoint, err := str("endpoint", "")
	if err != nil {
		return nil, err
	}
	pathStyle := false
	if v, ok := options["path_style"]; ok && v != nil {
		if pathStyle, ok = v.(bool); !ok {
			return nil, errors.New("store.options.path_style must be true or false")
		}
	}

	var opts []func(*config.LoadOptions) error
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %v", err)
	}
	if cfg.Region == "" {
		return nil, errors.New("no AWS region: set store.options.region in .prove.yaml or AWS_REGION")
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = pathStyle
		// Plain bodies with checksums only where S3 requires them, which
		// every S3-compatible server accepts.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &Store{client: client, bucket: bucket, prefix: prefix}, nil
}

// index is the object at <prefix>/index/<id>.json.
type index struct {
	Meta store.Meta `json:"meta"`
	Key  string     `json:"key"`
}

func (s *Store) indexKey(id string) string { return s.prefix + "index/" + id + ".json" }

// recordKey is <prefix>/<check>/<fingerprint>/<id>.json, or
// <id>.<n>.json for n > 1: keyed by what the check ran on (its command and
// inputs), not by the commit. Each part is one key segment.
func (s *Store) recordKey(m store.Meta, n int) string {
	seg := func(v string) string {
		if v == "" || v == "." || v == ".." || v == "index" {
			return "_" + v
		}
		return url.PathEscape(v)
	}
	name := m.ID
	if n > 1 {
		name = fmt.Sprintf("%s.%d", name, n)
	}
	return s.prefix + seg(m.Check) + "/" + seg(m.Fingerprint) + "/" + name + ".json"
}

// Put stores the record. A record whose ID is already indexed is left as
// it is.
func (s *Store) Put(ctx context.Context, meta store.Meta, data []byte) error {
	if id := record.ID(data); id != meta.ID {
		return fmt.Errorf("record bytes have sha256 %s, not the id %s", id, meta.ID)
	}
	if _, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: aws.String(s.indexKey(meta.ID))}); err == nil {
		return nil
	} else if status(err) != http.StatusNotFound {
		return s.fail("checking", s.indexKey(meta.ID), err)
	}

	key := ""
	for n := 1; key == "" && n <= maxSuffix; n++ {
		k := s.recordKey(meta, n)
		created, err := s.create(ctx, k, data)
		if err != nil {
			return s.fail("writing", k, err)
		}
		if created {
			key = k
		}
	}
	if key == "" {
		return fmt.Errorf("%d objects already hold the keys of record %s", maxSuffix, meta.ID)
	}

	entry, err := json.Marshal(index{Meta: meta, Key: key})
	if err != nil {
		return err
	}
	// Losing the race to another put of the same record is fine: its
	// index entry points to the same bytes.
	if _, err := s.create(ctx, s.indexKey(meta.ID), entry); err != nil {
		return s.fail("writing", s.indexKey(meta.ID), err)
	}
	return nil
}

// create writes key only if no object is there. It returns false when one
// is.
func (s *Store) create(ctx context.Context, key string, data []byte) (bool, error) {
	for attempt := 0; ; attempt++ {
		_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &s.bucket, Key: &key, Body: bytes.NewReader(data),
			ContentType: aws.String("application/json"), IfNoneMatch: aws.String("*"),
		})
		switch {
		case err == nil:
			return true, nil
		case status(err) == http.StatusPreconditionFailed:
			return false, nil
		case status(err) == http.StatusConflict && attempt < 3:
			// A concurrent conditional write to the same key; S3 asks
			// for a retry.
			continue
		default:
			return false, err
		}
	}
}

// Get returns the record stored under id.
func (s *Store) Get(ctx context.Context, id string) ([]byte, error) {
	if !idPattern.MatchString(id) {
		return nil, fmt.Errorf("bad record id %q: want 64 lowercase hex digits", id)
	}
	raw, err := s.read(ctx, s.indexKey(id))
	if status(err) == http.StatusNotFound {
		return nil, fmt.Errorf("record %s is not in s3://%s/%s", id, s.bucket, s.prefix)
	}
	if err != nil {
		return nil, s.fail("reading", s.indexKey(id), err)
	}
	var e index
	if err := json.Unmarshal(raw, &e); err != nil || e.Key == "" {
		return nil, fmt.Errorf("index entry %s is unreadable", s.indexKey(id))
	}
	data, err := s.read(ctx, e.Key)
	if err != nil {
		return nil, s.fail("reading", e.Key, err)
	}
	if record.ID(data) != id {
		return nil, fmt.Errorf("s3://%s/%s does not hold record %s", s.bucket, e.Key, id)
	}
	return data, nil
}

// List reads every index entry and returns those q matches, sorted by ID.
// An unreadable entry is skipped with a warning on stderr.
func (s *Store) List(ctx context.Context, q store.Query) ([]store.Meta, error) {
	var keys []string
	pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: aws.String(s.prefix + "index/")})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, s.fail("listing", s.prefix+"index/", err)
		}
		for _, o := range page.Contents {
			if o.Key != nil && strings.HasSuffix(*o.Key, ".json") {
				keys = append(keys, *o.Key)
			}
		}
	}

	entries := make([]*store.Meta, len(keys))
	errs := make([]error, len(keys))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, k := range keys {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			raw, err := s.read(ctx, k)
			if err != nil {
				errs[i] = s.fail("reading", k, err)
				return
			}
			var e index
			if err := json.Unmarshal(raw, &e); err != nil {
				fmt.Fprintf(os.Stderr, "%s: skipping unreadable index entry %s: %v\n", Program, k, err)
				return
			}
			entries[i] = &e.Meta
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	var out []store.Meta
	for _, m := range entries {
		if m != nil && q.Match(*m) {
			out = append(out, *m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) read(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return nil, err
	}
	defer func() { _ = obj.Body.Close() }()
	return io.ReadAll(obj.Body)
}

func (s *Store) fail(doing, key string, err error) error {
	return fmt.Errorf("%s s3://%s/%s: %v", doing, s.bucket, key, err)
}

// status is the HTTP status of an S3 error, or 0.
func status(err error) int {
	var re *awshttp.ResponseError
	if errors.As(err, &re) {
		return re.HTTPStatusCode()
	}
	return 0
}

type request struct {
	Op     string         `json:"op"`
	Config map[string]any `json:"config"`
	ID     string         `json:"id"`
	Meta   *store.Meta    `json:"meta"`
	Record string         `json:"record"`
	Query  store.Query    `json:"query"`
}

// Main answers one store request from in on out, as docs/adapters.md
// describes, and returns the exit code.
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
		metas, err := s.List(ctx, req.Query)
		if err != nil {
			return nil, err
		}
		if metas == nil {
			metas = []store.Meta{}
		}
		return map[string][]store.Meta{"records": metas}, nil
	default:
		return nil, fmt.Errorf("unknown op %q", req.Op)
	}
}
