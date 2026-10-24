package record

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// PAE is the DSSE v1 pre-authentication encoding, the bytes that get signed.
func PAE(payloadType string, payload []byte) []byte {
	return fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(payload), payload)
}

// Seal serializes the statement and signs it into an envelope.
func Seal(st Statement, s Signer) (Envelope, error) {
	payload, err := json.Marshal(st)
	if err != nil {
		return Envelope{}, err
	}
	keyID, sig, err := s.Sign(PAE(PayloadType, payload))
	if err != nil {
		return Envelope{}, fmt.Errorf("signing record: %w", err)
	}
	return Envelope{
		PayloadType: PayloadType,
		Payload:     base64.StdEncoding.EncodeToString(payload),
		Signatures:  []Signature{{KeyID: keyID, Sig: base64.StdEncoding.EncodeToString(sig)}},
	}, nil
}

// PayloadBytes returns the envelope's decoded payload.
func (e Envelope) PayloadBytes() ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("payload is not base64: %v", err)
	}
	return b, nil
}

// Decode decodes the envelope's statement and checks its types. It does not
// check signatures. It also returns the raw payload.
func Decode(env Envelope) (Statement, []byte, error) {
	if env.PayloadType != PayloadType {
		return Statement{}, nil, fmt.Errorf("payloadType is %q, want %q", env.PayloadType, PayloadType)
	}
	payload, err := env.PayloadBytes()
	if err != nil {
		return Statement{}, nil, err
	}
	var st Statement
	if err := json.Unmarshal(payload, &st); err != nil {
		return Statement{}, nil, fmt.Errorf("payload is not a statement: %v", err)
	}
	switch {
	case st.Type != StatementType:
		return Statement{}, nil, fmt.Errorf("_type is %q, want %q", st.Type, StatementType)
	case st.PredicateType != PredicateType:
		return Statement{}, nil, fmt.Errorf("predicateType is %q, want %q", st.PredicateType, PredicateType)
	case st.Tree() == "" || st.Commit() == "":
		return Statement{}, nil, errors.New("subject lacks a git tree or commit digest")
	}
	return st, payload, nil
}

// ID is the record's content address: the sha256 of its envelope.
func (e Envelope) ID() string {
	b, _ := json.Marshal(e)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
