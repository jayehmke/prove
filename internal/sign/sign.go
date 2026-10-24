// Package sign signs and verifies records: the built-in ssh signer (SSHSIG,
// the format of `ssh-keygen -Y sign`) and external `prove-signer-<name>`
// adapters.
package sign

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Namespace is the SSHSIG namespace prove signs in, so a prove signature
// can't be replayed as a git commit or file signature, or the reverse.
const Namespace = "prove"

const sshsigMagic = "SSHSIG"

// SSH signs with an SSH key.
type SSH struct {
	signer ssh.Signer
}

// LoadSSH loads a signing key: an unencrypted private key file, a public key
// file whose private half is in ssh-agent, a passphrase-protected private
// key that is in ssh-agent, or "key::<public key>" for a key in ssh-agent.
func LoadSSH(spec string) (*SSH, error) {
	if literal, ok := strings.CutPrefix(spec, "key::"); ok {
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(literal))
		if err != nil {
			return nil, fmt.Errorf("signing key %q: %v", spec, err)
		}
		return fromAgent(pub, "the key")
	}
	data, err := os.ReadFile(spec)
	if err != nil {
		return nil, fmt.Errorf("signing key: %v", err)
	}
	if pub, _, _, _, err := ssh.ParseAuthorizedKey(data); err == nil {
		return fromAgent(pub, spec)
	}
	s, err := ssh.ParsePrivateKey(data)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		pub := missing.PublicKey
		if pub == nil {
			if pubData, err := os.ReadFile(spec + ".pub"); err == nil {
				pub, _, _, _, _ = ssh.ParseAuthorizedKey(pubData)
			}
		}
		if pub == nil {
			return nil, fmt.Errorf("signing key %s has a passphrase and no public key to find it in ssh-agent", spec)
		}
		return fromAgent(pub, spec)
	}
	if err != nil {
		return nil, fmt.Errorf("signing key %s: %v", spec, err)
	}
	return &SSH{signer: s}, nil
}

func fromAgent(pub ssh.PublicKey, name string) (*SSH, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, fmt.Errorf("signing with %s needs ssh-agent, and SSH_AUTH_SOCK is not set", name)
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("ssh-agent: %v", err)
	}
	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		return nil, fmt.Errorf("ssh-agent: %v", err)
	}
	for _, s := range signers {
		if bytes.Equal(s.PublicKey().Marshal(), pub.Marshal()) {
			return &SSH{signer: s}, nil
		}
	}
	return nil, fmt.Errorf("ssh-agent doesn't hold %s (%s)", name, KeyID(pub))
}

// GenerateKey writes a new ed25519 key pair to path and path.pub.
func GenerateKey(path string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(priv, "prove signing key")
	if err != nil {
		return err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " prove signing key\n"
	return os.WriteFile(path+".pub", []byte(line), 0o644)
}

// PublicKey returns the signer's public key.
func (s *SSH) PublicKey() ssh.PublicKey { return s.signer.PublicKey() }

// Sign implements record.Signer with an SSHSIG signature.
func (s *SSH) Sign(msg []byte) (string, []byte, error) {
	h := sha512.Sum512(msg)
	signed := signedData(Namespace, "sha512", h[:])
	pub := s.signer.PublicKey()
	var sig *ssh.Signature
	var err error
	if as, ok := s.signer.(ssh.AlgorithmSigner); ok && pub.Type() == ssh.KeyAlgoRSA {
		sig, err = as.SignWithAlgorithm(rand.Reader, signed, ssh.KeyAlgoRSASHA512)
	} else {
		sig, err = s.signer.Sign(rand.Reader, signed)
	}
	if err != nil {
		return "", nil, err
	}
	blob := append([]byte(sshsigMagic), ssh.Marshal(sshsigWire{
		Version:   1,
		PublicKey: pub.Marshal(),
		Namespace: Namespace,
		HashAlg:   "sha512",
		Signature: ssh.Marshal(sig),
	})...)
	return KeyID(pub), blob, nil
}

type sshsigWire struct {
	Version   uint32
	PublicKey []byte
	Namespace string
	Reserved  string
	HashAlg   string
	Signature []byte
}

func signedData(namespace, hashAlg string, digest []byte) []byte {
	return append([]byte(sshsigMagic), ssh.Marshal(struct {
		Namespace string
		Reserved  string
		HashAlg   string
		Hash      []byte
	}{namespace, "", hashAlg, digest})...)
}

// KeyID is an SSH key's SHA256 fingerprint, as `ssh-keygen -l` prints it.
func KeyID(pub ssh.PublicKey) string { return ssh.FingerprintSHA256(pub) }

// IsSSHSig reports whether sig looks like an SSHSIG blob.
func IsSSHSig(sig []byte) bool { return bytes.HasPrefix(sig, []byte(sshsigMagic)) }

// VerifySSH checks an SSHSIG signature over msg in prove's namespace and
// returns the key that made it.
func VerifySSH(msg, sig []byte) (ssh.PublicKey, error) {
	if !IsSSHSig(sig) {
		return nil, errors.New("not an SSH signature")
	}
	var w sshsigWire
	if err := ssh.Unmarshal(sig[len(sshsigMagic):], &w); err != nil {
		return nil, fmt.Errorf("malformed SSH signature: %v", err)
	}
	if w.Version != 1 {
		return nil, fmt.Errorf("SSH signature version %d is not supported", w.Version)
	}
	if w.Namespace != Namespace {
		return nil, fmt.Errorf("SSH signature namespace is %q, want %q", w.Namespace, Namespace)
	}
	pub, err := ssh.ParsePublicKey(w.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("malformed SSH signature key: %v", err)
	}
	var s ssh.Signature
	if err := ssh.Unmarshal(w.Signature, &s); err != nil {
		return nil, fmt.Errorf("malformed SSH signature: %v", err)
	}
	var h hash.Hash
	switch w.HashAlg {
	case "sha512":
		h = sha512.New()
	case "sha256":
		h = sha256.New()
	default:
		return nil, fmt.Errorf("SSH signature hash %q is not supported", w.HashAlg)
	}
	h.Write(msg)
	if err := pub.Verify(signedData(w.Namespace, w.HashAlg, h.Sum(nil)), &s); err != nil {
		return nil, fmt.Errorf("SSH signature does not match: %v", err)
	}
	return pub, nil
}

// decodeSig decodes a DSSE signature's base64.
func decodeSig(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("sig is not base64: %v", err)
	}
	return b, nil
}
