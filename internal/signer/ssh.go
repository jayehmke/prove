package signer

import (
	"bytes"
	"crypto/rand"
	"crypto/sha512"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Namespace is the SSHSIG namespace of prove signatures, so a prove
// signature can never be replayed as a git commit or file signature.
const Namespace = "prove"

const sshsigMagic = "SSHSIG"

// SSH signs with an SSH key, writing signatures in the SSHSIG format that
// `ssh-keygen -Y verify -n prove` also accepts.
type SSH struct {
	signer   ssh.Signer
	identity string
}

// NewSSH wraps an ssh.Signer.
func NewSSH(s ssh.Signer, identity string) *SSH { return &SSH{signer: s, identity: identity} }

// LoadSSH loads a signing key. path is a private key file, or a public key
// file (.pub) whose private half is held by the ssh-agent at SSH_AUTH_SOCK.
func LoadSSH(path, identity string) (*SSH, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	if pub, _, _, _, perr := ssh.ParseAuthorizedKey(data); perr == nil {
		s, err := agentSigner(pub)
		if err != nil {
			return nil, fmt.Errorf("signing key %s: %w", path, err)
		}
		return NewSSH(s, identity), nil
	}
	s, err := ssh.ParsePrivateKey(data)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		s, aerr := agentSigner(missing.PublicKey)
		if aerr != nil {
			return nil, fmt.Errorf("signing key %s is encrypted and not in ssh-agent: add it with ssh-add (%v)", path, aerr)
		}
		return NewSSH(s, identity), nil
	}
	if err != nil {
		return nil, fmt.Errorf("signing key %s: %w", path, err)
	}
	return NewSSH(s, identity), nil
}

// LoadSSHPublic signs through ssh-agent with the key whose public half is
// given in authorized_keys form.
func LoadSSHPublic(authorized, identity string) (*SSH, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorized))
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	s, err := agentSigner(pub)
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	return NewSSH(s, identity), nil
}

func agentSigner(pub ssh.PublicKey) (ssh.Signer, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, errors.New("SSH_AUTH_SOCK is not set, so no ssh-agent holds the private key")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("ssh-agent: %w", err)
	}
	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		return nil, fmt.Errorf("ssh-agent: %w", err)
	}
	want := pub.Marshal()
	for _, s := range signers {
		if bytes.Equal(s.PublicKey().Marshal(), want) {
			return s, nil
		}
	}
	return nil, fmt.Errorf("ssh-agent does not hold %s", ssh.FingerprintSHA256(pub))
}

// Identity is who the signer says it is.
func (s *SSH) Identity() string { return s.identity }

// PublicKey returns the key in authorized_keys form.
func (s *SSH) PublicKey() string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.signer.PublicKey())))
}

// Sign returns an SSHSIG signature over msg. The key ID is the key's
// SHA256 fingerprint.
func (s *SSH) Sign(msg []byte) (string, []byte, error) {
	h := sha512.Sum512(msg)
	signed := sshsigSignedData(h[:])
	var sig *ssh.Signature
	var err error
	if as, ok := s.signer.(ssh.AlgorithmSigner); ok && s.signer.PublicKey().Type() == ssh.KeyAlgoRSA {
		sig, err = as.SignWithAlgorithm(rand.Reader, signed, ssh.KeyAlgoRSASHA512)
	} else {
		sig, err = s.signer.Sign(rand.Reader, signed)
	}
	if err != nil {
		return "", nil, fmt.Errorf("ssh sign: %w", err)
	}
	blob := ssh.Marshal(struct {
		Magic     [6]byte
		Version   uint32
		PublicKey []byte
		Namespace string
		Reserved  string
		HashAlg   string
		Signature []byte
	}{
		Version:   1,
		PublicKey: s.signer.PublicKey().Marshal(),
		Namespace: Namespace,
		HashAlg:   "sha512",
		Signature: ssh.Marshal(sig),
	})
	copy(blob[:6], sshsigMagic)
	return ssh.FingerprintSHA256(s.signer.PublicKey()), blob, nil
}

func sshsigSignedData(hash []byte) []byte {
	b := ssh.Marshal(struct {
		Magic     [6]byte
		Namespace string
		Reserved  string
		HashAlg   string
		Hash      []byte
	}{Namespace: Namespace, HashAlg: "sha512", Hash: hash})
	copy(b[:6], sshsigMagic)
	return b
}

// verifySSH checks an SSHSIG signature over msg against pub.
func verifySSH(pub ssh.PublicKey, msg, blob []byte) error {
	if len(blob) < 6 || string(blob[:6]) != sshsigMagic {
		return errors.New("not an SSHSIG signature")
	}
	var parsed struct {
		Version   uint32
		PublicKey []byte
		Namespace string
		Reserved  string
		HashAlg   string
		Signature []byte
	}
	if err := ssh.Unmarshal(blob[6:], &parsed); err != nil {
		return fmt.Errorf("malformed SSHSIG signature: %w", err)
	}
	if parsed.Version != 1 {
		return fmt.Errorf("unsupported SSHSIG version %d", parsed.Version)
	}
	if !bytes.Equal(parsed.PublicKey, pub.Marshal()) {
		return errors.New("signature was made by a different key")
	}
	if parsed.Namespace != Namespace {
		return fmt.Errorf("signature namespace is %q, not %q", parsed.Namespace, Namespace)
	}
	if parsed.HashAlg != "sha512" {
		return fmt.Errorf("unsupported SSHSIG hash %q", parsed.HashAlg)
	}
	var sig ssh.Signature
	if err := ssh.Unmarshal(parsed.Signature, &sig); err != nil {
		return fmt.Errorf("malformed SSHSIG signature: %w", err)
	}
	h := sha512.Sum512(msg)
	if err := pub.Verify(sshsigSignedData(h[:]), &sig); err != nil {
		return errors.New("signature does not match the record")
	}
	return nil
}
