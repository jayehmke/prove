// Package sign signs and verifies records: the built-in ssh signer (SSHSIG,
// the format of `ssh-keygen -Y sign`) and external `prove-signer-<name>`
// adapters.
package sign

import (
	"errors"

	"golang.org/x/crypto/ssh"

	"github.com/jayehmke/prove/internal/config"
	"github.com/jayehmke/prove/internal/record"
)

var errTODO = errors.New("not implemented")

// Namespace is the SSHSIG namespace prove signs in.
const Namespace = "prove"

// SSH signs with an SSH key.
type SSH struct {
	signer ssh.Signer
}

// LoadSSH loads a signing key: an unencrypted private key file, or a public
// key (.pub) whose private half is in ssh-agent.
func LoadSSH(path string) (*SSH, error) { return nil, errTODO }

// GenerateKey writes a new ed25519 key pair to path and path.pub.
func GenerateKey(path string) error { return errTODO }

// PublicKey returns the signer's public key.
func (s *SSH) PublicKey() ssh.PublicKey { return nil }

// Sign implements record.Signer.
func (s *SSH) Sign(msg []byte) (string, []byte, error) { return "", nil, errTODO }

// KeyID is an SSH key's SHA256 fingerprint.
func KeyID(pub ssh.PublicKey) string { return "" }

// VerifySSH checks an SSHSIG signature over msg and returns the signing key.
func VerifySSH(msg, sig []byte) (ssh.PublicKey, error) { return nil, errTODO }

// Trust decides who signed a record and whether they are trusted.
type Trust struct{}

// NewTrust builds trust rules from the policy's signers. local, if not nil,
// is trusted as identity "local" (used when the policy names no signers).
func NewTrust(signers []config.Signer, local ssh.PublicKey) (*Trust, error) {
	return nil, errTODO
}

// Identify verifies a signature over msg and returns the trusted identity
// that made it.
func (t *Trust) Identify(msg []byte, s record.Signature) (string, error) {
	return "", errTODO
}
