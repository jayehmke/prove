package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type local struct{ dir string }

// NewLocal returns a store that keeps each record as <id>.json in dir. Keep
// dir out of the working tree (or ignored), or the tree stops being clean.
func NewLocal(dir string) Store { return &local{dir: dir} }

func (s *local) path(id string) string { return filepath.Join(s.dir, id+".json") }

func (s *local) Put(e Entry) error {
	if !validID(e.ID) {
		return fmt.Errorf("local store: bad record id %q", e.ID)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path(e.ID))
}

func (s *local) read(path string) (Entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, err
	}
	var e Entry
	if err := json.Unmarshal(b, &e); err != nil {
		return Entry{}, fmt.Errorf("local store: %s is not a record: %v", path, err)
	}
	return e, nil
}

func (s *local) Get(id string) (Entry, error) {
	if !validID(id) {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	e, err := s.read(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return e, err
}

func (s *local) List(f Filter) ([]Entry, error) {
	files, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, file := range files {
		name := file.Name()
		if file.IsDir() || !strings.HasSuffix(name, ".json") || !validID(strings.TrimSuffix(name, ".json")) {
			continue
		}
		e, err := s.read(filepath.Join(s.dir, name))
		if err != nil {
			return nil, err
		}
		if f.Match(e) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
