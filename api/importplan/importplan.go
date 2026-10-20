// Package importplan is the vocabulary an import shares when it asks before it
// replaces anything (ADR-0467): how an artifact an
// import carries compares with the one this server holds, what the caller decided
// for those that differ, and what became of each.
//
// The rule it encodes is one sentence: an import writes what is new, leaves what is
// unchanged, and writes over work done on the server only when the caller said so.
// Work done on a server is not replaced by an import nobody looked at, and the same
// import run twice changes nothing the second time.
//
// Telling work done on the server from an import that moved on takes a third
// version: the one the last import brought, remembered per artifact as a hash (its
// base). An artifact still as the last import left it, and different in this one,
// is behind and simply taken; one edited here while the import still carries what
// it carried last time is ahead and simply kept. Only an artifact both sides moved —
// or one no import ever brought, so there is no base to tell by — is asked about.
package importplan

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// How an artifact an import carries compares with this server's.
const (
	New       = "new"
	Unchanged = "unchanged"
	// Changed is an artifact both sides moved since the last import, or one this
	// server holds otherwise and no import ever brought. It is decided.
	Changed = "changed"
	// Behind is an artifact this server still holds as the last import brought it,
	// and that this import brings newer. It is taken unless the caller keeps it.
	Behind = "behind"
	// Ahead is an artifact edited on this server since the last import, which this
	// import brings as it did last time. It is kept unless the caller replaces it.
	Ahead = "ahead"
)

// Hash is the fingerprint an artifact is compared by: the parts that make it what it
// is, each length-prefixed, so that two different splits never hash alike.
func Hash(parts ...[]byte) string {
	h := sha256.New()
	var n [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		h.Write(n[:])
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Classify is how an artifact compares, from the hashes of this server's version,
// the import's and the base — the version the last import brought, "" when none
// did.
func Classify(exists bool, local, incoming, base string) string {
	switch {
	case !exists:
		return New
	case local == incoming:
		return Unchanged
	case base == "":
		return Changed
	case local == base:
		return Behind
	case incoming == base:
		return Ahead
	}
	return Changed
}

// Asks reports whether an artifact of a status needs a decision.
func Asks(status string) bool { return status == Changed }

// What became of an artifact, or would under the decisions given.
const (
	Created   = "created"
	Updated   = "updated"
	Kept      = "kept"
	Undecided = "undecided"
)

// Artifact is one artifact of an import. Ref names it as the overwrite and keep
// lists do — kind:id, such as process:onboarding or product:laptop.
type Artifact struct {
	Ref     string `json:"ref"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status"`
	Outcome string `json:"outcome"`
}

// Decisions is what the caller decided for the artifacts that differ.
type Decisions struct {
	// DryRun asks for the plan alone: nothing is written.
	DryRun bool
	// Overwrite and Keep name artifacts by ref.
	Overwrite, Keep map[string]bool
	// OverwriteAll and KeepAll stand for every changed artifact the other list does
	// not name.
	OverwriteAll, KeepAll bool
}

// Parse reads ?dryRun=, ?overwrite= and ?keep=. Each list is refs separated by
// commas, a parameter may repeat, and * stands for every changed artifact the other
// list does not name. A ref in both lists, or * in both, contradicts itself and is
// refused.
func Parse(q url.Values) (Decisions, error) {
	d := Decisions{Overwrite: map[string]bool{}, Keep: map[string]bool{}}
	d.DryRun = q.Get("dryRun") == "true" || q.Get("dryRun") == "1"
	read := func(param string, refs map[string]bool, all *bool) {
		for _, v := range q[param] {
			for _, ref := range strings.Split(v, ",") {
				switch ref = strings.TrimSpace(ref); ref {
				case "":
				case "*":
					*all = true
				default:
					refs[ref] = true
				}
			}
		}
	}
	read("overwrite", d.Overwrite, &d.OverwriteAll)
	read("keep", d.Keep, &d.KeepAll)
	if d.OverwriteAll && d.KeepAll {
		return d, errors.New("overwrite=* and keep=* contradict each other")
	}
	for ref := range d.Overwrite {
		if d.Keep[ref] {
			return d, fmt.Errorf("%s is named under both overwrite and keep", ref)
		}
	}
	return d, nil
}

// Outcome is what becomes of an artifact of a status under these decisions. A ref
// named in a list wins over everything else. Behind is taken and ahead kept without
// one, and * decides only what is changed: overwrite=* never replaces work done
// only here, and keep=* never holds back what only the import moved.
func (d Decisions) Outcome(ref, status string) string {
	switch {
	case status == New:
		return Created
	case status == Unchanged:
		return Unchanged
	case d.Overwrite[ref]:
		return Updated
	case d.Keep[ref]:
		return Kept
	case status == Behind:
		return Updated
	case status == Ahead:
		return Kept
	case d.OverwriteAll:
		return Updated
	case d.KeepAll:
		return Kept
	}
	return Undecided
}

// Compare is the status of an artifact this server already holds.
func Compare(same bool) string {
	if same {
		return Unchanged
	}
	return Changed
}

// Writes reports whether an outcome writes the import's version.
func Writes(outcome string) bool { return outcome == Created || outcome == Updated }

// UndecidedProblem is what an import stopped for an undecided artifact says of it.
const UndecidedProblem = "differs from this server's; name it under overwrite to replace it or under keep to leave it"
