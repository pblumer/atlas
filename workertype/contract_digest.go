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

// DigestTechnicalContractJSON names a technical contract file by the SHA-256 of
// its exact bytes, as sha256:<hex> (ADR-0483). Nothing is re-serialized, so any
// tool can recompute the digest, and a reformatted file is a different artifact.
// The bytes must be one valid contract: within the size bound, valid UTF-8
// without a byte-order mark, LF line endings only, and no duplicate JSON member at
// any depth. A mark or a carriage return would let the same contract hash
// differently on another machine or editor, so such a file gets no digest.
//
// Digesting is an offline publication step, never a processor operation. A
// digest binds bytes only: it does not certify a Worker, grant routing or record
// an approval.
func DigestTechnicalContractJSON(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > maxTechnicalContractBytes {
		return "", fmt.Errorf("technical capability contract must contain 1..%d bytes", maxTechnicalContractBytes)
	}
	if !utf8.Valid(raw) {
		return "", errors.New("technical capability contract must contain valid UTF-8")
	}
	if bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
		return "", errors.New("technical capability contract must not start with a byte-order mark")
	}
	if bytes.IndexByte(raw, '\r') >= 0 {
		return "", errors.New("technical capability contract must use LF line endings, without carriage returns")
	}
	if err := rejectDuplicateTechnicalJSONKeys(raw); err != nil {
		return "", err
	}
	if _, err := ParseTechnicalContractJSON(raw); err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
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
