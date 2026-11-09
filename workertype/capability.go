package workertype

import (
	"fmt"
	"regexp"
)

// CapabilityClaim is a descriptive assertion that one Worker Type operation
// offers a versioned technical capability. It is not evidence that another
// operation is interchangeable, nor an authorization or routing permission.
// See ADR-0480.
type CapabilityClaim struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	// Profile distinguishes provider-specific semantic constraints. Omitting it
	// does not imply cross-provider equivalence.
	Profile string `json:"profile,omitempty"`
}

var capabilityProfilePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// ValidateCapabilityClaims checks one operation's claims at design time — a
// manifest's at admission, a built-in Worker Type's operation table in its tests —
// never while processing a job. It is the one rule both are held to, so a claim a
// built-in table carries is refused for exactly what would refuse it in a manifest.
// Legacy operations are allowed to declare no claims.
func ValidateCapabilityClaims(operation string, claims []CapabilityClaim) error {
	seen := make(map[string]struct{}, len(claims))
	for _, claim := range claims {
		if !workerTypeIDPattern.MatchString(claim.ID) {
			return fmt.Errorf("spec.operations operation %q capabilities id %q must be a lower-case dotted identifier", operation, claim.ID)
		}
		if _, err := parseVersion(claim.Version); err != nil {
			return fmt.Errorf("spec.operations operation %q capabilities %q version: %w", operation, claim.ID, err)
		}
		if claim.Profile != "" && !capabilityProfilePattern.MatchString(claim.Profile) {
			return fmt.Errorf("spec.operations operation %q capabilities %q profile %q must be lower-case kebab-case", operation, claim.ID, claim.Profile)
		}
		key := claim.ID + "@" + claim.Version
		if _, exists := seen[key]; exists {
			return fmt.Errorf("spec.operations operation %q contains duplicate capability %q", operation, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}
