package s3

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeS3 is an in-process S3 with path-style addressing: PutObject (with
// If-None-Match: *), GetObject, HeadObject and ListObjectsV2, paged.
type fakeS3 struct {
	bucket string
	// accessKey must appear in every request's signature.
	accessKey string
	pageSize  int

	mu      sync.Mutex
	objects map[string][]byte
	// overwrites lists keys a PUT replaced. S3 with versioning keeps the
	// old version, but prove must never ask it to.
	overwrites []string
	// unconditional lists PUTs sent without If-None-Match: *.
	unconditional []string
	errors        []string
}

func newFakeS3(t *testing.T, bucket, accessKey string) (*fakeS3, string) {
	f := &fakeS3{bucket: bucket, accessKey: accessKey, pageSize: 3, objects: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, e := range f.errors {
			t.Errorf("fake S3: %s", e)
		}
	})
	return f, srv.URL
}

func (f *fakeS3) object(key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[key]
	return data, ok
}

func (f *fakeS3) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type s3Error struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

func fail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_ = xml.NewEncoder(w).Encode(s3Error{Code: code, Message: code})
}

type listResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	Name                  string   `xml:"Name"`
	Prefix                string   `xml:"Prefix"`
	KeyCount              int      `xml:"KeyCount"`
	MaxKeys               int      `xml:"MaxKeys"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken,omitempty"`
	Contents              []struct {
		Key  string `xml:"Key"`
		Size int    `xml:"Size"`
	} `xml:"Contents"`
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(r.Header.Get("Authorization"), "Credential="+f.accessKey+"/") {
		f.errors = append(f.errors, "unsigned request "+r.Method+" "+r.URL.Path)
		fail(w, http.StatusForbidden, "AccessDenied")
		return
	}
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if bucket != f.bucket {
		fail(w, http.StatusNotFound, "NoSuchBucket")
		return
	}
	switch {
	case r.Method == http.MethodPut && key != "":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			fail(w, http.StatusBadRequest, "IncompleteBody")
			return
		}
		if r.Header.Get("Content-Encoding") == "aws-chunked" {
			f.errors = append(f.errors, "aws-chunked body for "+key)
		}
		_, exists := f.objects[key]
		if r.Header.Get("If-None-Match") != "*" {
			f.unconditional = append(f.unconditional, key)
		} else if exists {
			fail(w, http.StatusPreconditionFailed, "PreconditionFailed")
			return
		}
		if exists {
			f.overwrites = append(f.overwrites, key)
		}
		f.objects[key] = body
		w.Header().Set("ETag", `"`+strconv.Itoa(len(f.objects))+`"`)
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && key != "":
		data, ok := f.objects[key]
		if !ok {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fail(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(data)
		}
	case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		q := r.URL.Query()
		var keys []string
		for k := range f.objects {
			if strings.HasPrefix(k, q.Get("prefix")) && k > q.Get("continuation-token") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		res := listResult{Name: f.bucket, Prefix: q.Get("prefix"), MaxKeys: f.pageSize}
		if len(keys) > f.pageSize {
			keys = keys[:f.pageSize]
			res.IsTruncated = true
			res.NextContinuationToken = keys[len(keys)-1]
		}
		for _, k := range keys {
			res.Contents = append(res.Contents, struct {
				Key  string `xml:"Key"`
				Size int    `xml:"Size"`
			}{k, len(f.objects[k])})
		}
		res.KeyCount = len(keys)
		w.Header().Set("Content-Type", "application/xml")
		_ = xml.NewEncoder(w).Encode(res)
	default:
		f.errors = append(f.errors, "unexpected request "+r.Method+" "+r.URL.String())
		fail(w, http.StatusNotImplemented, "NotImplemented")
	}
}
