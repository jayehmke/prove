package record

import "errors"

var errTODO = errors.New("not implemented")

// PAE is the DSSE v1 pre-authentication encoding.
func PAE(payloadType string, payload []byte) []byte { return nil }

// Seal serializes the statement and signs it into an envelope.
func Seal(st Statement, s Signer) (Envelope, error) { return Envelope{}, errTODO }

// Decode decodes the envelope's statement and checks its types. It does not
// check signatures.
func Decode(env Envelope) (Statement, []byte, error) { return Statement{}, nil, errTODO }

// ID is the record's content address: the sha256 of its envelope.
func (e Envelope) ID() string { return "" }
