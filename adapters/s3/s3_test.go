package s3

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
	"github.com/jayehmke/prove/internal/testutil"
	"github.com/jayehmke/prove/internal/testutil/fakes3"
	"github.com/jayehmke/prove/storetest"
)

const testAccessKey = "AKIDPROVETEST"

// TestMain runs this test binary as the adapter when it is started under
// the name prove-store-s3, so the protocol tests need no separate build.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == Program {
		os.Exit(Main(context.Background(), os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// awsEnv gives the standard credential chain a key from the environment
// only: no shared config, no instance metadata.
func awsEnv(t *testing.T) {
	t.Helper()
	home := testutil.Isolate(t)
	t.Setenv("AWS_ACCESS_KEY_ID", testAccessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(home, "no-aws-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(home, "no-aws-credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

func sample(check, commit, signer, body string) (store.Meta, []byte) {
	data := []byte(`{"payloadType":"application/vnd.in-toto+json","payload":"` + body + `"}`)
	return store.Meta{ID: record.ID(data), Check: check, Commit: commit, Tree: "t-" + commit, Signer: signer,
		Fingerprint: "sha256:" + body, Result: "pass", FinishedAt: time.Unix(1700000000, 0).UTC()}, data
}

func TestS3PutNeverOverwrites(t *testing.T) {
	t.Run("AC-808-1 put, get and list round-trip and a second put to the same key never replaces the first record", func(t *testing.T) {
		awsEnv(t)
		fake, endpoint := fakes3.New(t, "records", testAccessKey)
		ctx := context.Background()
		s, err := New(ctx, map[string]any{"bucket": "records", "prefix": "team/prove/", "endpoint": endpoint, "path_style": true})
		if err != nil {
			t.Fatal(err)
		}

		first, firstData := sample("unit", "c1", "alice@example.com", "first")
		rerun, rerunData := sample("unit", "c1", "alice@example.com", "rerun")
		other, otherData := sample("lint", "c2", "ci/bot", "other")
		for _, r := range []struct {
			m    store.Meta
			data []byte
		}{{first, firstData}, {rerun, rerunData}, {other, otherData}, {first, firstData}} {
			if err := s.Put(ctx, r.m, r.data); err != nil {
				t.Fatalf("put %s: %v", r.m.ID, err)
			}
		}

		// Records live under <prefix>/<check>/<fingerprint>/<id>.json, by
		// what the check ran on and not by commit; the rerun is its own
		// object next to the first.
		for _, r := range []struct {
			m    store.Meta
			data []byte
		}{{first, firstData}, {rerun, rerunData}, {other, otherData}} {
			key := "team/prove/" + r.m.Check + "/" + r.m.Fingerprint + "/" + r.m.ID + ".json"
			if got, _ := fake.Object(key); !bytes.Equal(got, r.data) {
				t.Fatalf("%s holds %q, want the record; keys: %v", key, got, fake.Keys())
			}
		}
		for _, k := range fake.Keys() {
			if strings.Contains(k, "/c1/") || strings.Contains(k, "/c2/") {
				t.Fatalf("key %s names a commit; records are keyed by check inputs", k)
			}
		}
		if len(fake.Overwrites) > 0 || len(fake.Unconditional) > 0 {
			t.Fatalf("overwrote %v; puts without If-None-Match: %v", fake.Overwrites, fake.Unconditional)
		}
		// Three records, three index entries: the repeated put added nothing.
		if n := len(fake.Keys()); n != 6 {
			t.Fatalf("want 6 objects, got %d: %v", n, fake.Keys())
		}

		for _, r := range []struct {
			m    store.Meta
			data []byte
		}{{first, firstData}, {rerun, rerunData}, {other, otherData}} {
			got, err := s.Get(ctx, r.m.ID)
			if err != nil || !bytes.Equal(got, r.data) {
				t.Fatalf("get %s = %q, %v", r.m.ID, got, err)
			}
		}
		if _, err := s.Get(ctx, record.ID([]byte("never put"))); err == nil {
			t.Fatal("get of a missing record should fail")
		}

		// Eight objects in pages of three: list follows continuation tokens.
		third, thirdData := sample("unit", "c1", "alice@example.com", "third")
		if err := s.Put(ctx, third, thirdData); err != nil {
			t.Fatal(err)
		}
		got, err := s.List(ctx, store.Query{})
		if err != nil {
			t.Fatal(err)
		}
		want := []store.Meta{first, rerun, other, third}
		sort.Slice(want, func(i, j int) bool { return want[i].ID < want[j].ID })
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("list = %+v\nwant %+v", got, want)
		}
		got, err = s.List(ctx, store.Query{Check: "lint"})
		if err != nil || len(got) != 1 || got[0] != other {
			t.Fatalf("list check=lint = %+v, %v", got, err)
		}
	})

	t.Run("a record already at the key is kept and the put takes the next key", func(t *testing.T) {
		awsEnv(t)
		fake, endpoint := fakes3.New(t, "records", testAccessKey)
		ctx := context.Background()
		s, err := New(ctx, map[string]any{"bucket": "records", "endpoint": endpoint, "path_style": true})
		if err != nil {
			t.Fatal(err)
		}
		// Another writer's object sits at the key, with no index entry.
		m, data := sample("unit", "c1", "alice@example.com", "mine")
		key := "prove/unit/" + m.Fingerprint + "/" + m.ID + ".json"
		fake.Put(key, []byte("someone else's"))
		if err := s.Put(ctx, m, data); err != nil {
			t.Fatal(err)
		}
		if got, _ := fake.Object(key); string(got) != "someone else's" {
			t.Fatalf("the existing object was replaced with %q", got)
		}
		if got, _ := fake.Object("prove/unit/" + m.Fingerprint + "/" + m.ID + ".2.json"); !bytes.Equal(got, data) {
			t.Fatalf("the record is not at the next key; keys: %v", fake.Keys())
		}
		if got, err := s.Get(ctx, m.ID); err != nil || !bytes.Equal(got, data) {
			t.Fatalf("get = %q, %v; keys: %v", got, err, fake.Keys())
		}
	})

	t.Run("a put whose bytes do not match the id is refused", func(t *testing.T) {
		awsEnv(t)
		fake, endpoint := fakes3.New(t, "records", testAccessKey)
		s, err := New(context.Background(), map[string]any{"bucket": "records", "endpoint": endpoint, "path_style": true})
		if err != nil {
			t.Fatal(err)
		}
		m, _ := sample("unit", "c1", "alice@example.com", "a")
		if err := s.Put(context.Background(), m, []byte("other bytes")); err == nil || !strings.Contains(err.Error(), "sha256") {
			t.Fatalf("put of mismatched bytes: %v", err)
		}
		if keys := fake.Keys(); len(keys) != 0 {
			t.Fatalf("wrote %v", keys)
		}
	})

	t.Run("config errors name the option", func(t *testing.T) {
		awsEnv(t)
		for _, tc := range []struct {
			config map[string]any
			want   string
		}{
			{map[string]any{}, "bucket"},
			{map[string]any{"bucket": 3}, "bucket"},
			{map[string]any{"bucket": "b", "path_style": "yes"}, "path_style"},
			{map[string]any{"bucket": "b", "region": ""}, ""},
		} {
			_, err := New(context.Background(), tc.config)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("%v: %v", tc.config, err)
				}
				continue
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v: error %v should name %q", tc.config, err, tc.want)
			}
		}
	})
}

func TestS3SpeaksStoreProtocol(t *testing.T) {
	t.Run("AC-808-2 prove-store-s3 passes the store-protocol conformance suite", func(t *testing.T) {
		awsEnv(t)
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		bin := t.TempDir()
		if err := os.Symlink(self, filepath.Join(bin, Program)); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		storetest.Run(t, "s3", func(t *testing.T) map[string]any {
			_, endpoint := fakes3.New(t, "records", testAccessKey)
			return map[string]any{"bucket": "records", "endpoint": endpoint, "path_style": true}
		})
	})
}
