package authz

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// digestVersion is part of every digest, so a later change to what a digest covers
// can never make an old approval match a new action by accident.
const digestVersion = 1

// Digest is the identity of one action: SHA-256 over a canonical JSON of the Worker
// Type, the Worker, the operation, the canonical resource and every material
// parameter. An approval is of a digest, so approving one action can never be
// replayed for another (ADR-0475 §9).
//
// The JSON is canonical because encoding/json writes map keys in sorted order and a
// value decoded from JSON re-encodes to the same bytes. Only the server computes a
// digest, over the parameters it resolved itself; a digest a Worker reports is never
// read.
func Digest(workerType, worker, operation, resource string, params map[string]any) (string, error) {
	b, err := json.Marshal(struct {
		V          int            `json:"v"`
		WorkerType string         `json:"workerType"`
		Worker     string         `json:"worker"`
		Operation  string         `json:"operation"`
		Resource   string         `json:"resource"`
		Params     map[string]any `json:"params"`
	}{digestVersion, workerType, worker, operation, resource, params})
	if err != nil {
		return "", fmt.Errorf("authz: digest: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
