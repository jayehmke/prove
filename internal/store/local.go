package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Local keeps records in a folder: records/<id>.json holds the envelope
// and index/<id>.json its metadata.
type Local struct {
	Dir string
}

// NewLocal returns the store that keeps records in a folder.
func NewLocal(dir string) Store { return &Local{Dir: dir} }

// Name describes the store.
func (l *Local) Name() string { return "local store " + l.Dir }

// Put writes the record and its index entry.
func (l *Local) Put(_ context.Context, meta Meta, record []byte) error {
	if err := checkID(meta.ID); err != nil {
		return err
	}
	m, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(l.Dir, "records", meta.ID+".json"), record); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(l.Dir, "index", meta.ID+".json"), m)
}

// Get reads a record.
func (l *Local) Get(_ context.Context, id string) ([]byte, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(l.Dir, "records", id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("record %s is not in the %s", id, l.Name())
	}
	return data, err
}

// List reads the index.
func (l *Local) List(_ context.Context, q Query) ([]Meta, error) {
	dir := filepath.Join(l.Dir, "index")
	names, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, n := range names {
		if !strings.HasSuffix(n.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, n.Name()))
		if err != nil {
			return nil, err
		}
		var m Meta
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("%s: %v", filepath.Join(dir, n.Name()), err)
		}
		if q.Match(m) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
