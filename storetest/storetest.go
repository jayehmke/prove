// Package storetest is the conformance suite for prove-store-<name>
// adapters. It drives an adapter over the JSON protocol in
// docs/adapters.md exactly as prove does, so an adapter in any language
// can run it from a Go test:
//
//	func TestMyStore(t *testing.T) {
//		// put prove-store-mine on PATH first
//		storetest.Run(t, "mine", func(t *testing.T) map[string]any {
//			return map[string]any{"dir": t.TempDir()}
//		})
//	}
package storetest

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/jayehmke/prove/internal/record"
	"github.com/jayehmke/prove/internal/store"
)

// Run checks that the adapter prove-store-<name> on PATH speaks the store
// protocol. newConfig returns the store.options of an empty store; every
// case gets its own.
func Run(t *testing.T, name string, newConfig func(t *testing.T) map[string]any) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := store.NewExternal(name, newConfig(t))
			if err := c.run(context.Background(), s); err != nil {
				t.Fatalf("%s: %v", s.Name(), err)
			}
		})
	}
}

type testCase struct {
	name string
	run  func(ctx context.Context, s store.Store) error
}

var cases = []testCase{
	{"an empty store lists nothing", emptyList},
	{"get returns the bytes put, and list the meta put", roundTrip},
	{"get of a missing record fails", getMissing},
	{"a second put of the same record succeeds and keeps one record", putTwice},
	{"a rerun of the same check by the same signer keeps both records", rerunKeepsBoth},
}

// sample makes a record and its meta. body is part of the record bytes, so
// different bodies give different IDs.
func sample(check, commit, signer, body string) (store.Meta, []byte) {
	// Records are JSON in practice, but a store must keep any bytes.
	data := []byte(fmt.Sprintf("{\"payload\":%q}\n\x00\xff", check+commit+signer+body))
	return store.Meta{
		ID: record.ID(data), Check: check, Commit: commit, Tree: "tree-" + commit, Signer: signer,
		Fingerprint: "sha256:" + body, Result: "pass", FinishedAt: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
	}, data
}

func emptyList(ctx context.Context, s store.Store) error {
	got, err := s.List(ctx, store.Query{})
	if err != nil {
		return fmt.Errorf("list of an empty store: %v", err)
	}
	if len(got) != 0 {
		return fmt.Errorf("list of an empty store returned %d records", len(got))
	}
	return nil
}

func roundTrip(ctx context.Context, s store.Store) error {
	a, aData := sample("unit", "c1", "alice@example.com", "a")
	b, bData := sample("lint", "c2", "bob@example.com", "b")
	for _, r := range []struct {
		m    store.Meta
		data []byte
	}{{a, aData}, {b, bData}} {
		if err := s.Put(ctx, r.m, r.data); err != nil {
			return fmt.Errorf("put: %v", err)
		}
	}
	for _, r := range []struct {
		m    store.Meta
		data []byte
	}{{a, aData}, {b, bData}} {
		got, err := s.Get(ctx, r.m.ID)
		if err != nil {
			return fmt.Errorf("get %s: %v", r.m.ID, err)
		}
		if !bytes.Equal(got, r.data) {
			return fmt.Errorf("get %s returned %q, want the bytes put: %q", r.m.ID, got, r.data)
		}
	}
	return wantList(ctx, s, a, b)
}

func getMissing(ctx context.Context, s store.Store) error {
	a, aData := sample("unit", "c1", "alice@example.com", "a")
	if err := s.Put(ctx, a, aData); err != nil {
		return fmt.Errorf("put: %v", err)
	}
	missing, _ := sample("unit", "c1", "alice@example.com", "never put")
	if got, err := s.Get(ctx, missing.ID); err == nil {
		return fmt.Errorf("get of a record never put returned %q and no error", got)
	}
	return nil
}

func putTwice(ctx context.Context, s store.Store) error {
	a, aData := sample("unit", "c1", "alice@example.com", "a")
	for i := 1; i <= 2; i++ {
		if err := s.Put(ctx, a, aData); err != nil {
			return fmt.Errorf("put #%d of the same record: %v", i, err)
		}
	}
	got, err := s.Get(ctx, a.ID)
	if err != nil || !bytes.Equal(got, aData) {
		return fmt.Errorf("get after two puts = %q, %v", got, err)
	}
	return wantList(ctx, s, a)
}

// rerunKeepsBoth puts two different records for the same check, commit
// and signer, as a rerun does. A store keyed by check must not let the
// second replace the first.
func rerunKeepsBoth(ctx context.Context, s store.Store) error {
	first, firstData := sample("unit", "c1", "alice@example.com", "first run")
	second, secondData := sample("unit", "c1", "alice@example.com", "second run")
	second.Result = "fail"
	if err := s.Put(ctx, first, firstData); err != nil {
		return fmt.Errorf("put first: %v", err)
	}
	if err := s.Put(ctx, second, secondData); err != nil {
		return fmt.Errorf("put second: %v", err)
	}
	for _, r := range []struct {
		m    store.Meta
		data []byte
	}{{first, firstData}, {second, secondData}} {
		got, err := s.Get(ctx, r.m.ID)
		if err != nil {
			return fmt.Errorf("get %s after a rerun: %v", r.m.ID, err)
		}
		if !bytes.Equal(got, r.data) {
			return fmt.Errorf("get %s after a rerun returned another record's bytes", r.m.ID)
		}
	}
	return wantList(ctx, s, first, second)
}

// wantList checks that list returns exactly want, each meta unchanged.
func wantList(ctx context.Context, s store.Store, want ...store.Meta) error {
	got, err := s.List(ctx, store.Query{})
	if err != nil {
		return fmt.Errorf("list: %v", err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].ID < got[j].ID })
	sort.Slice(want, func(i, j int) bool { return want[i].ID < want[j].ID })
	if len(got) != len(want) {
		return fmt.Errorf("list returned %d records, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !got[i].FinishedAt.Equal(want[i].FinishedAt) {
			return fmt.Errorf("list changed finishedAt of %s: %v, want %v", want[i].ID, got[i].FinishedAt, want[i].FinishedAt)
		}
		g, w := got[i], want[i]
		g.FinishedAt, w.FinishedAt = time.Time{}, time.Time{}
		if g != w {
			return fmt.Errorf("list returned meta %+v, want %+v", got[i], want[i])
		}
	}
	return nil
}
