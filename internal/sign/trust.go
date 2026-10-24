package sign

import (
	"bytes"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/ssh"

	"github.com/jayehmke/prove/internal/adapter"
	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/record"
)

// LocalIdentity names this machine's key when the policy trusts no one.
const LocalIdentity = "local"

// SignatureError means a signature doesn't verify: the record changed after
// it was signed, or the signature is malformed.
type SignatureError struct{ Err error }

func (e *SignatureError) Error() string { return "invalid signature: " + e.Err.Error() }
func (e *SignatureError) Unwrap() error { return e.Err }

// UntrustedError means a valid signature was made by a key the policy
// doesn't trust.
type UntrustedError struct{ KeyID string }

func (e *UntrustedError) Error() string { return "signed by untrusted key " + e.KeyID }

// Trust decides who signed a record and whether they are trusted.
type Trust struct {
	signers []trusted
	dir     string
}

type trusted struct {
	identity string
	keyID    string
	ssh      ssh.PublicKey // built-in signer
	adapter  string        // external signer
	key      string
}

// NewTrust builds trust rules from the policy's signers. local, if not nil,
// is trusted as identity "local" (used when the policy names no signers).
func NewTrust(signers []config.Signer, local ssh.PublicKey) (*Trust, error) {
	t := &Trust{}
	for _, s := range signers {
		if s.Adapter != "" {
			t.signers = append(t.signers, trusted{identity: s.Identity, keyID: s.Key, adapter: s.Adapter, key: s.Key})
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(s.Key))
		if err != nil {
			return nil, fmt.Errorf("signer %q: key is not an SSH public key: %v", s.Identity, err)
		}
		t.signers = append(t.signers, trusted{identity: s.Identity, keyID: KeyID(pub), ssh: pub})
	}
	if local != nil {
		t.signers = append(t.signers, trusted{identity: LocalIdentity, keyID: KeyID(local), ssh: local})
	}
	return t, nil
}

// WithDir sets the working directory for external signer adapters.
func (t *Trust) WithDir(dir string) *Trust { t.dir = dir; return t }

// Empty reports whether the trust rules trust no one.
func (t *Trust) Empty() bool { return len(t.signers) == 0 }

// Identify verifies a signature over msg and returns the trusted identity
// that made it. It returns a *SignatureError when the signature doesn't
// verify and an *UntrustedError when the key isn't trusted.
func (t *Trust) Identify(msg []byte, s record.Signature) (string, error) {
	sig, err := decodeSig(s.Sig)
	if err != nil {
		return "", &SignatureError{err}
	}
	if IsSSHSig(sig) {
		pub, err := VerifySSH(msg, sig)
		if err != nil {
			return "", &SignatureError{err}
		}
		if id := KeyID(pub); s.KeyID != id {
			return "", &SignatureError{fmt.Errorf("keyid %s doesn't match the signing key %s", s.KeyID, id)}
		}
		for _, tr := range t.signers {
			if tr.ssh != nil && bytes.Equal(tr.ssh.Marshal(), pub.Marshal()) {
				return tr.identity, nil
			}
		}
		return "", &UntrustedError{KeyID(pub)}
	}
	for _, tr := range t.signers {
		if tr.adapter == "" || tr.keyID != s.KeyID {
			continue
		}
		ext := &External{Name: tr.adapter, Dir: t.dir}
		if err := ext.Verify(tr.key, msg, sig); err != nil {
			return "", &SignatureError{err}
		}
		return tr.identity, nil
	}
	return "", &UntrustedError{s.KeyID}
}

// External is a signer adapter, the program prove-signer-<Name>.
type External struct {
	Name    string
	Options map[string]any
	Dir     string
}

func (e *External) program() string { return "prove-signer-" + e.Name }

// Sign implements record.Signer.
func (e *External) Sign(msg []byte) (string, []byte, error) {
	var resp struct {
		KeyID string `json:"keyid"`
		Sig   string `json:"sig"`
	}
	err := adapter.Call(e.program(), e.Dir, "sign", map[string]any{
		"options": e.Options,
		"payload": base64.StdEncoding.EncodeToString(msg),
	}, &resp)
	if err != nil {
		return "", nil, err
	}
	sig, err := decodeSig(resp.Sig)
	if err != nil || resp.KeyID == "" {
		return "", nil, fmt.Errorf("%s sign: response needs a keyid and a base64 sig", e.program())
	}
	return resp.KeyID, sig, nil
}

// Verify asks the adapter whether sig over msg was made by key.
func (e *External) Verify(key string, msg, sig []byte) error {
	var resp struct {
		OK bool `json:"ok"`
	}
	err := adapter.Call(e.program(), e.Dir, "verify", map[string]any{
		"options": e.Options,
		"key":     key,
		"keyid":   key,
		"payload": base64.StdEncoding.EncodeToString(msg),
		"sig":     base64.StdEncoding.EncodeToString(sig),
	}, &resp)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s says the signature does not match", e.program())
	}
	return nil
}
