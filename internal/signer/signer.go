// Package signer signs records and checks their signatures against the
// policy's trusted signers. The ssh signer is built in; any other signer is
// an external prove-signer-<name> adapter.
package signer

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jayehmke/prove/internal/adapter"
	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/gitx"
	"golang.org/x/crypto/ssh"
)

// Signer signs records.
type Signer interface {
	// Sign signs msg and returns the key ID to store with the signature.
	Sign(msg []byte) (keyID string, sig []byte, err error)
	// Identity is who the signer says it is.
	Identity() string
	// Key is the trusted-signer entry that verifies this signer's records.
	Key() (config.SignerKey, error)
}

// Key returns the trusted-signer entry for this SSH key.
func (s *SSH) Key() (config.SignerKey, error) {
	return config.SignerKey{Type: "ssh", Key: s.PublicKey()}, nil
}

// FromEnv loads the signer this machine signs with:
//
//   - PROVE_SIGNER picks the signer type: "ssh" (the default) or the name of
//     a prove-signer-<name> adapter.
//   - PROVE_SSH_KEY is the SSH key file. Without it prove uses git's
//     user.signingkey when gpg.format is ssh, then ~/.ssh/id_ed25519,
//     id_ecdsa and id_rsa.
//   - PROVE_IDENTITY is the claimed identity. Without it, git's user.email.
func FromEnv(ctx context.Context, repo *gitx.Repo) (Signer, error) {
	identity := os.Getenv("PROVE_IDENTITY")
	if identity == "" && repo != nil {
		identity, _ = repo.Git(ctx, "config", "user.email")
	}
	if identity == "" {
		identity = "unknown"
	}
	kind := os.Getenv("PROVE_SIGNER")
	if kind != "" && kind != "ssh" {
		if !config.ValidName(kind) {
			return nil, fmt.Errorf("PROVE_SIGNER=%q must be ssh or an adapter name (letters, digits, '.', '_' or '-')", kind)
		}
		return &External{Name: kind, identity: identity, ctx: ctx}, nil
	}
	if p := os.Getenv("PROVE_SSH_KEY"); p != "" {
		return LoadSSH(expandHome(p), identity)
	}
	if repo != nil {
		format, _ := repo.Git(ctx, "config", "gpg.format")
		key, _ := repo.Git(ctx, "config", "user.signingkey")
		if format == "ssh" && key != "" {
			if lit, ok := strings.CutPrefix(key, "key::"); ok {
				return LoadSSHPublic(lit, identity)
			}
			if strings.HasPrefix(key, "ssh-") || strings.HasPrefix(key, "ecdsa-") {
				return LoadSSHPublic(key, identity)
			}
			return LoadSSH(expandHome(key), identity)
		}
	}
	home, _ := os.UserHomeDir()
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		p := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(p); err == nil {
			return LoadSSH(p, identity)
		}
	}
	return nil, errors.New("no signing key: set PROVE_SSH_KEY to an SSH key file, set git's user.signingkey with gpg.format=ssh, or create ~/.ssh/id_ed25519")
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// External is a prove-signer-<Name> adapter.
type External struct {
	Name     string
	identity string
	ctx      context.Context
}

// NewExternal returns the adapter signer prove-signer-<name>.
func NewExternal(ctx context.Context, name, identity string) *External {
	return &External{Name: name, identity: identity, ctx: ctx}
}

// Identity is who the signer says it is.
func (e *External) Identity() string { return e.identity }

// Sign asks the adapter to sign msg.
func (e *External) Sign(msg []byte) (string, []byte, error) {
	var resp struct {
		KeyID string `json:"keyid"`
		Sig   string `json:"sig"`
	}
	req := map[string]any{"op": "sign", "payload": base64.StdEncoding.EncodeToString(msg), "identity": e.identity}
	if err := adapter.Call(e.ctx, adapter.Program("signer", e.Name), req, &resp); err != nil {
		return "", nil, err
	}
	sig, err := base64.StdEncoding.DecodeString(resp.Sig)
	if err != nil || resp.KeyID == "" || len(sig) == 0 {
		return "", nil, fmt.Errorf("adapter %s: sign answer needs keyid and a base64 sig", adapter.Program("signer", e.Name))
	}
	return resp.KeyID, sig, nil
}

// Key asks the adapter for its key ID, which is the trusted key for it.
func (e *External) Key() (config.SignerKey, error) {
	var resp struct {
		KeyID string `json:"keyid"`
	}
	if err := adapter.Call(e.ctx, adapter.Program("signer", e.Name), map[string]any{"op": "key"}, &resp); err != nil {
		return config.SignerKey{}, err
	}
	return config.SignerKey{Type: e.Name, Key: resp.KeyID}, nil
}

// Trusted is one trusted signer.
type Trusted struct {
	Identity string
	Key      config.SignerKey
	KeyID    string
	pub      ssh.PublicKey
}

// TrustSet holds the trusted signers, indexed by key ID.
type TrustSet struct {
	byKeyID map[string][]Trusted
}

// NewTrustSet builds a trust set from the policy's signers.
func NewTrustSet(keys map[string]config.SignerKey) (*TrustSet, error) {
	t := &TrustSet{byKeyID: map[string][]Trusted{}}
	ids := make([]string, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := t.Add(id, keys[id]); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// Add trusts key as identity.
func (t *TrustSet) Add(identity string, key config.SignerKey) error {
	tr := Trusted{Identity: identity, Key: key, KeyID: key.Key}
	if key.Type == "" || key.Type == "ssh" {
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key.Key))
		if err != nil {
			return fmt.Errorf("signer %q: not an SSH public key: %v", identity, err)
		}
		tr.Key.Type = "ssh"
		tr.pub = pub
		tr.KeyID = ssh.FingerprintSHA256(pub)
	}
	t.byKeyID[tr.KeyID] = append(t.byKeyID[tr.KeyID], tr)
	return nil
}

// Len is the number of trusted signers.
func (t *TrustSet) Len() int {
	n := 0
	for _, v := range t.byKeyID {
		n += len(v)
	}
	return n
}

// ErrUntrusted means no trusted signer has the signature's key.
var ErrUntrusted = errors.New("key is not trusted")

// Verify checks sig over msg with the trusted key whose ID is keyID and
// returns that signer's identity. It returns ErrUntrusted when no trusted
// signer has the key.
func (t *TrustSet) Verify(ctx context.Context, keyID string, msg, sig []byte) (string, error) {
	cands := t.byKeyID[keyID]
	if len(cands) == 0 {
		if k := sshsigKeyID(sig); k != "" && k != keyID {
			return "", fmt.Errorf("keyid %s does not match the key that made the signature", keyID)
		}
		return "", ErrUntrusted
	}
	var firstErr error
	for _, tr := range cands {
		var err error
		if tr.pub != nil {
			err = verifySSH(tr.pub, msg, sig)
		} else {
			err = verifyExternal(ctx, tr, msg, sig)
		}
		if err == nil {
			return tr.Identity, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", firstErr
}

func verifyExternal(ctx context.Context, tr Trusted, msg, sig []byte) error {
	var resp struct {
		OK     bool   `json:"ok"`
		Reason string `json:"reason"`
	}
	req := map[string]any{
		"op":      "verify",
		"payload": base64.StdEncoding.EncodeToString(msg),
		"sig":     base64.StdEncoding.EncodeToString(sig),
		"keyid":   tr.KeyID,
		"key":     tr.Key.Key,
	}
	if err := adapter.Call(ctx, adapter.Program("signer", tr.Key.Type), req, &resp); err != nil {
		return err
	}
	if !resp.OK {
		if resp.Reason == "" {
			resp.Reason = "signature does not match the record"
		}
		return errors.New(resp.Reason)
	}
	return nil
}
