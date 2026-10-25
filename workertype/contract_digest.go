package workertype

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const maxTechnicalDigestJSONDepth = maxTechnicalSchemaDepth + 8

// CanonicalTechnicalContractJSON returns a stable JSON encoding for the
// exact semantic contract document. It rejects duplicate keys (including
// nested keys), malformed/extra JSON, invalid UTF-8 and invalid contracts.
// Object members are ordered by encoding/json and whitespace is discarded;
// numeric spelling remains significant (for example 1 and 1.0 differ).
// This Go serialization profile is not a claim of RFC 8785 equivalence.
//
// Canonicalization is an offline publication step, never a processor operation.
// It is NOT evidence that a Worker implements this contract.
func CanonicalTechnicalContractJSON(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > maxTechnicalContractBytes {
		return nil, fmt.Errorf("technical capability contract must contain 1..%d bytes", maxTechnicalContractBytes)
	}
	if !utf8.Valid(raw) {
		return nil, errors.New("technical capability contract must contain valid UTF-8")
	}
	if err := rejectDuplicateTechnicalJSONKeys(raw); err != nil {
		return nil, err
	}
	if _, err := ParseTechnicalContractJSON(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode contract for canonicalization: %w", err)
	}
	if err := ensureTechnicalContractEOF(decoder); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode canonical contract: %w", err)
	}
	return encoded, nil
}

// DigestTechnicalContractJSON computes the immutable sha256:<hex> identifier
// of the canonical document. It does not validate any claim or approval.
func DigestTechnicalContractJSON(raw []byte) (string, error) {
	encoded, err := CanonicalTechnicalContractJSON(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func rejectDuplicateTechnicalJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanTechnicalJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); errors.Is(err, io.EOF) {
		return nil
	} else if err == nil {
		return errors.New("technical capability contract contains multiple JSON values")
	} else {
		return fmt.Errorf("invalid JSON trailer: %w", err)
	}
}

func scanTechnicalJSONValue(decoder *json.Decoder, depth int) error {
	if depth > maxTechnicalDigestJSONDepth {
		return fmt.Errorf("technical capability contract nesting exceeds %d", maxTechnicalDigestJSONDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid technical contract JSON: %w", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		members := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("invalid object member: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("technical capability contract object member must be a string")
			}
			if _, exists := members[key]; exists {
				return fmt.Errorf("technical capability contract contains duplicate JSON member %q", key)
			}
			members[key] = struct{}{}
			if err := scanTechnicalJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("invalid technical capability contract object terminator")
		}
		return nil
	case '[':
		for decoder.More() {
			if err := scanTechnicalJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("invalid technical capability contract array terminator")
		}
		return nil
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}
