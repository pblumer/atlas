package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// What does not travel in a portable backup
// (ADR-0357).
//
// The design-time archive (ADR-0107) is documented as a file an author moves between
// installations, and it is applied as an overlay onto a running one. Two of the things
// it carried are not an author's to move, because they belong to the installation that
// wrote them rather than to the models inside it:
//
//   - the **node identity** (`settings/node.json`, ADR-0189 §6). Measured: restore A's
//     archive onto B and B answers with A's node id, so two running installations claim
//     one identity and the provenance of everything else in the archive is gone with it;
//   - a **definition key**. `deployments/` and `decisions/` are filed by key, and the
//     counter that issues keys (`keyspace/`, ADR-0339) is runtime and stays behind. So
//     the archive carries records whose identity was minted by a sequence it does not
//     carry, and the overlay resolved the ambiguity by overwriting. Measured: a process
//     deployed on A, restored onto B where that key belonged to another process,
//     reported one finished instance and a visit on an element it had never reached —
//     the local history, re-labelled onto a foreign definition's elements.
//
// The whole-instance snapshot (ADR-0109) is not affected and is deliberately left
// alone: it carries `keyspace/`, `jobtypes/` and the WAL together and drops the
// derived state, so it is one consistent point in time — and reconstituting *this*
// engine elsewhere, node id included, is its job.

// nodeIdentityFile is the one file in the settings store that is this installation
// rather than its configuration. Everything else there — the theme, the OIDC claim
// mapping, the mock seeds — is an author's or an operator's setting and travels.
var nodeIdentityFile = filepath.Join("settings", "node.json")

// portable reports whether an archive-relative path may ride in the design-time
// backup, and may be written by its restore. It is applied on both sides on purpose:
// excluding it from new archives alone would leave every archive taken before this
// able to overwrite an identity.
func portable(archivePath string) bool {
	return filepath.Clean(filepath.FromSlash(archivePath)) != nodeIdentityFile
}

// restoreCollision is one record a restore did not take: the key it claims, what the
// installation already has there, and what the archive wanted to put there.
type restoreCollision struct {
	// Kind is "process" or "decision".
	Kind string `json:"kind"`
	Key  uint64 `json:"key"`
	// Here and Incoming name the two deployments in the form an operator reads them —
	// "order-process v3", "eligibility v2".
	Here     string `json:"here"`
	Incoming string `json:"incoming"`
}

// foreignDeployment reports whether writing this archive entry would replace a
// deployed definition with a different one, and describes the clash when it would.
//
// The test is identity, not equality. A record is the same deployment when it is the
// same thing an operator sees on the screen: a process is its `processId` at its
// `version`, a decision deployment is the decisions it provides at their versions.
// Restoring an installation's own backup — including an older one whose record has
// since been edited — therefore still overwrites, which is what ADR-0107 decided and
// is not revisited here. Only a record that would make a key mean something else is
// held back.
//
// Anything that is not a keyed record, or is not readable as one, is left to the
// caller's ordinary path: this decides what to skip, never what to reject.
func foreignDeployment(dataDir, dest string, incoming []byte) (restoreCollision, bool) {
	rel, err := filepath.Rel(dataDir, dest)
	if err != nil {
		return restoreCollision{}, false
	}
	top, _, ok := strings.Cut(filepath.ToSlash(rel), "/")
	if !ok {
		return restoreCollision{}, false
	}
	here, err := os.ReadFile(dest)
	if err != nil {
		return restoreCollision{}, false // nothing there: the key is free
	}
	switch top {
	case "deployments":
		var a, b persistedDeployment
		if json.Unmarshal(here, &a) != nil || json.Unmarshal(incoming, &b) != nil {
			return restoreCollision{}, false
		}
		if a.ProcessID == b.ProcessID && a.Version == b.Version {
			return restoreCollision{}, false
		}
		return restoreCollision{
			Kind: "process", Key: a.Key,
			Here: processName(a), Incoming: processName(b),
		}, true
	case "decisions":
		var a, b persistedDecision
		if json.Unmarshal(here, &a) != nil || json.Unmarshal(incoming, &b) != nil {
			return restoreCollision{}, false
		}
		if deployedDecisionName(a) == deployedDecisionName(b) {
			return restoreCollision{}, false
		}
		return restoreCollision{
			Kind: "decision", Key: a.Key,
			Here: deployedDecisionName(a), Incoming: deployedDecisionName(b),
		}, true
	}
	return restoreCollision{}, false
}

// processName is a deployed definition as an operator names it.
func processName(rec persistedDeployment) string {
	return fmt.Sprintf("%s v%d", rec.ProcessID, rec.Version)
}

// deployedDecisionName is a decision deployment as the thing a pin and a business rule
// task mean by it: the decisions it provides, each at the version this deployment
// gave it. Sorted, because the order inside a record is the model's and not an
// identity.
func deployedDecisionName(rec persistedDecision) string {
	parts := make([]string, 0, len(rec.Decisions))
	for _, d := range rec.Decisions {
		parts = append(parts, fmt.Sprintf("%s v%d", d.ID, d.Version))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "no decisions"
	}
	return strings.Join(parts, ", ")
}

// restoreNote is the response's prose. A restore that took everything says what it
// always said; one that held records back leads with that, because a partial restore
// is the fact the operator has to act on.
func restoreNote(collisions []restoreCollision) string {
	const base = "Design-time artifacts (drafts, projects, forms, …) are live immediately; deployed processes take effect after a server restart."
	if len(collisions) == 0 {
		return base
	}
	return fmt.Sprintf(
		"%d deployed definition(s) in the archive were NOT restored: their keys are already in use here by other definitions, and taking them would attach this installation's instance history to them. A definition key is issued per installation and does not travel — restore onto a fresh instance to bring these across. %s",
		len(collisions), base)
}
