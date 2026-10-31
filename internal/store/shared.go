package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/jayehmke/prove/internal/record"
)

// Shared reads two stores as one: the repository's own store, where every
// record is written, and the share store that prove push --share copies
// records into (an S3 bucket, say), so a verifier finds records that were
// never pushed as git refs.
//
// The share store is untrusted like any store: verify checks the
// signature on every record it reads. If it can't be reached (no
// credentials on this machine, a network error), Shared says so once and
// goes on without it, so a check whose record is only there runs instead
// of passing.
type Shared struct {
	Own   Store
	Share Store

	mu     sync.Mutex
	listed bool
	metas  []Meta // every record the share store listed
	down   error  // why the share store is not used, once it failed
}

// NewShared returns the store that writes to own and reads own and share.
func NewShared(own, share Store) *Shared { return &Shared{Own: own, Share: share} }

// Name describes both stores.
func (s *Shared) Name() string { return s.Own.Name() + " and the " + s.Share.Name() }

// Put stores the record in the repository's own store only. prove push
// --share copies it to the share store.
func (s *Shared) Put(ctx context.Context, meta Meta, data []byte) error {
	return s.Own.Put(ctx, meta, data)
}

// Get reads the record from the repository's own store, else from the
// share store.
func (s *Shared) Get(ctx context.Context, id string) ([]byte, error) {
	data, err := s.Own.Get(ctx, id)
	if err == nil {
		return data, nil
	}
	if s.failed() != nil {
		return nil, err
	}
	shared, shareErr := s.Share.Get(ctx, id)
	if shareErr != nil {
		return nil, errors.Join(err, shareErr)
	}
	return shared, nil
}

// List returns the records of both stores that q matches, each ID once,
// sorted by ID. The share store is listed once and the answer kept, since
// verify lists once per check and an adapter may read every entry on each
// list.
func (s *Shared) List(ctx context.Context, q Query) ([]Meta, error) {
	own, err := s.Own.List(ctx, q)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]Meta, 0, len(own))
	for _, m := range own {
		seen[m.ID] = true
		out = append(out, m)
	}
	for _, m := range s.shared(ctx) {
		if !seen[m.ID] && q.Match(m) {
			seen[m.ID] = true
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Shared) shared(ctx context.Context) []Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listed || s.down != nil {
		return s.metas
	}
	metas, err := s.Share.List(ctx, Query{})
	if err != nil {
		s.down = err
		fmt.Fprintf(Warnings, "prove: not reading the %s: %v\n", s.Share.Name(), err)
		return nil
	}
	s.listed, s.metas = true, metas
	return metas
}

func (s *Shared) failed() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.down
}

// OwnStore returns the store records are written to: st itself, or the
// repository's own store when st also reads a share store.
func OwnStore(st Store) Store {
	if s, ok := st.(*Shared); ok {
		return s.Own
	}
	return st
}

// MetaOf reads a record's index entry from the record itself. The record
// is not verified: meta is only a lookup hint.
func MetaOf(data []byte) (Meta, error) {
	_, st, err := record.Open(data)
	if err != nil {
		return Meta{}, err
	}
	p := st.Predicate
	return Meta{
		ID: record.ID(data), Check: p.Check, Commit: st.Commit(), Tree: st.Tree(),
		Signer: p.Signer, Fingerprint: p.Fingerprint, Result: p.Result, FinishedAt: p.FinishedAt,
	}, nil
}
