package workertype

import (
	"errors"
	"fmt"
)

// Bounds on a reported identity, so a poll cannot grow the server's worker registry
// without limit. An id is a reverse-DNS name, a version a semantic version, a digest
// "sha256:" and 64 hex digits; none of them comes near these.
const (
	maxIdentityID      = 128
	maxIdentityVersion = 64
)

// CheckIdentity validates the Worker Type identity a Worker Instance reports in the
// protocol handshake (ADR-0535 §3): the same stable id and semantic version a manifest
// declares, and, when the instance knows it, the sha256 digest of the artifact it runs.
// An empty digest is allowed — a Worker Instance an operator runs by hand may not know
// its own — and an empty id with nothing else is "no identity reported", which the
// caller decides on before calling this.
//
// It checks shape only. Whether the reported digest is the one atlas installed is the
// supervisor's question, not this function's.
func CheckIdentity(id, version, digest string) error {
	switch {
	case id == "":
		return errors.New("workerType.id is required when a Worker Type is reported")
	case len(id) > maxIdentityID || !workerTypeIDPattern.MatchString(id):
		return fmt.Errorf("workerType.id %q must be a lower-case reverse-DNS identifier such as com.example.sap-s4", id)
	case version == "":
		return errors.New("workerType.version is required when a Worker Type is reported")
	case len(version) > maxIdentityVersion:
		return fmt.Errorf("workerType.version is longer than %d characters", maxIdentityVersion)
	}
	if _, err := parseVersion(version); err != nil {
		return fmt.Errorf("workerType.version: %w", err)
	}
	if digest != "" && !digestPattern.MatchString(digest) {
		return fmt.Errorf("workerType.digest %q must be sha256: and 64 lower-case hex digits", digest)
	}
	return nil
}
