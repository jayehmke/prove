package store

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/jayehmke/prove/internal/adapter"
)

// External is a prove-store-<name> adapter. See docs/adapters.md.
type External struct {
	Program string
	Options map[string]any
}

// NewExternal returns the prove-store-<name> adapter store.
func NewExternal(name string, options map[string]any) Store {
	return &External{Program: adapter.Program("store", name), Options: options}
}

// Name describes the store.
func (e *External) Name() string { return "store adapter " + e.Program }

type request struct {
	Op     string         `json:"op"`
	Config map[string]any `json:"config"`
	ID     string         `json:"id,omitempty"`
	Meta   *Meta          `json:"meta,omitempty"`
	Record string         `json:"record,omitempty"`
	Query  *Query         `json:"query,omitempty"`
}

func (e *External) config() map[string]any {
	if e.Options == nil {
		return map[string]any{}
	}
	return e.Options
}

// Put sends {"op":"put","meta":...,"record":<base64>}.
func (e *External) Put(ctx context.Context, meta Meta, record []byte) error {
	if err := checkID(meta.ID); err != nil {
		return err
	}
	var resp struct {
		OK bool `json:"ok"`
	}
	req := request{Op: "put", Config: e.config(), Meta: &meta, Record: base64.StdEncoding.EncodeToString(record)}
	if err := adapter.Call(ctx, e.Program, req, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("adapter %s did not answer {\"ok\":true} to put", e.Program)
	}
	return nil
}

// Get sends {"op":"get","id":...} and expects {"record":<base64>}.
func (e *External) Get(ctx context.Context, id string) ([]byte, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	var resp struct {
		Record *string `json:"record"`
	}
	if err := adapter.Call(ctx, e.Program, request{Op: "get", Config: e.config(), ID: id}, &resp); err != nil {
		return nil, err
	}
	if resp.Record == nil {
		return nil, fmt.Errorf("adapter %s answered get without a record", e.Program)
	}
	data, err := base64.StdEncoding.DecodeString(*resp.Record)
	if err != nil {
		return nil, fmt.Errorf("adapter %s returned a record that is not base64: %v", e.Program, err)
	}
	return data, nil
}

// List sends {"op":"list","query":...} and expects {"records":[meta...]}.
// Prove filters the answer again, so an adapter may ignore the query.
func (e *External) List(ctx context.Context, q Query) ([]Meta, error) {
	var resp struct {
		Records *[]Meta `json:"records"`
	}
	if err := adapter.Call(ctx, e.Program, request{Op: "list", Config: e.config(), Query: &q}, &resp); err != nil {
		return nil, err
	}
	if resp.Records == nil {
		return nil, fmt.Errorf("adapter %s answered list without records", e.Program)
	}
	var out []Meta
	for _, m := range *resp.Records {
		if q.Match(m) {
			out = append(out, m)
		}
	}
	return out, nil
}
