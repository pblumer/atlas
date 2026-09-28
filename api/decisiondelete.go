package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
)

// Removing a decision deployment (ADR-0336).
//
// Every Deploy in the decision editor mints a version, and until now nothing could
// take one back: the store only ever grew. ADR-0329 recorded, before any route
// existed, what would have to be true first — and this is that route, with that
// rule.
//
// Two things have to hold, and neither is "no instances are running". A process
// deployment pins its latest-bound decision references to a decision deployment's
// key and keeps no copy of that model (ADR-0327), so the pin is what a delete can
// break, and it breaks at task activation inside a running instance. The second is
// quieter: the per-decision version counter is derived from the surviving records,
// so removing the newest version of a decision that has older ones behind it would
// hand the next deploy a version number that is already spoken for.

// decisionPinRef is one deployed definition that is pinned to a decision
// deployment: which definition, and the decision reference that does the pinning.
type decisionPinRef struct {
	Key        uint64 `json:"key"`
	ProcessID  string `json:"processId"`
	Name       string `json:"name,omitempty"`
	Version    int32  `json:"version"`
	DecisionID string `json:"decisionId"`
}

// handleDeleteDecisionDeployment removes one decision deployment — a key, which is
// one model, which may provide several decisions and therefore one version of
// each.
//
// The key is the unit because the key is what was written, what a process pins to,
// and what the record on disk is named by; a "delete this decision entirely" that
// swept several records would be several deletes with one confirmation, each with
// its own reason to be refused.
//
// Durable before visible (I2), the discipline handleDeleteProcess follows: the
// record goes first, so a deletion that is acknowledged can never come back on
// restart, and the registry is updated after.
func (s *Server) handleDeleteDecisionDeployment(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid decision deployment key")
		return
	}
	var (
		found      bool
		pins       []decisionPinRef
		superseded []string
		stranded   []decisionPinRef
		loadErr    error
		persistErr error
	)
	s.do(func() {
		var rec persistedDecision
		var ok bool
		if rec, ok, loadErr = s.decisionDeploys.load(key); loadErr != nil || !ok {
			return
		}
		found = true
		if pins = s.definitionsPinnedTo(key); len(pins) > 0 {
			return
		}
		var all []persistedDecision
		if all, loadErr = s.decisionDeploys.LoadAll(); loadErr != nil {
			return
		}
		if superseded = currentVersionsWithHistory(rec, all); len(superseded) > 0 {
			return
		}
		if stranded = s.definitionsStrandedBy(rec, all); len(stranded) > 0 {
			return
		}
		if loadErr = s.decisionDeploys.Delete(decisionKeyName(key)); loadErr != nil {
			persistErr, loadErr = loadErr, nil
			return
		}
		s.dmnRegistry.UndeployDecision(key)
		// The version counter follows the records, so it is recomputed here rather
		// than decremented: the guard above has already established that nothing it
		// holds can fall, except where a decision's whole history is going.
		s.recountDecisionVersions(all, key)
	})
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read decision deployment: "+loadErr.Error())
		return
	case persistErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "delete decision deployment: "+persistErr.Error())
		return
	case !found:
		// Already gone is the outcome the caller asked for.
		w.WriteHeader(http.StatusNoContent)
		return
	case len(pins) > 0:
		httpapi.Error(w, http.StatusConflict, pinnedRefusal(key, pins))
		return
	case len(stranded) > 0:
		httpapi.Error(w, http.StatusConflict, strandedRefusal(key, stranded))
		return
	case len(superseded) > 0:
		httpapi.Error(w, http.StatusConflict, fmt.Sprintf(
			"this deployment is the current version of %v, and older versions of %s are still deployed. Deleting it would send the next process deploy back to an older version without saying so, and hand the deploy after that a version number that is already taken — deploy a newer version first, or remove the older ones and then this one",
			superseded, plural(len(superseded), "that decision", "those decisions")))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// definitionsPinnedTo is ADR-0329's guard, stated as a query: which deployed
// process definitions resolved a latest-bound decision reference to this key.
//
// A definition is pinned to key K when PinnedDecisionKey returns K for any of its
// latest-bound references. Two things the record calls out are load-bearing here
// and are not obvious from the outside:
//
//   - a superseded version is as pinned as a current one. A definition deployed
//     while v2 was newest is pinned to v2's key forever, which is exactly what the
//     versioning was built to guarantee;
//   - running instances are not the test. A definition with no instances today can
//     be started tomorrow, and unlike a process delete — which removes the thing
//     that would be started — the definition survives this and keeps its pin.
//
// It reads run-loop state (the deployments map and their compiled pins), so it runs
// on the loop.
func (s *Server) definitionsPinnedTo(key uint64) []decisionPinRef {
	var out []decisionPinRef
	for _, defKey := range s.order {
		d := s.deployments[defKey]
		if d == nil || d.cp == nil {
			continue
		}
		for _, id := range d.cp.LatestBoundDecisions() {
			if pinned, ok := d.cp.PinnedDecisionKey(id); ok && pinned == key {
				out = append(out, decisionPinRef{
					Key: d.Key, ProcessID: d.ProcessID, Name: d.Name, Version: d.Version, DecisionID: id,
				})
			}
		}
		// A fixed version is as pinned as a frozen latest: the task names exactly this
		// deployment and nothing else will do
		// (ADR-draft-a-business-rule-task-chooses-its-decision-version).
		for _, ref := range d.cp.VersionBoundDecisions() {
			if pinned, ok := d.cp.VersionPinnedKey(ref.DecisionID, ref.Version); ok && pinned == key {
				out = append(out, decisionPinRef{
					Key: d.Key, ProcessID: d.ProcessID, Name: d.Name, Version: d.Version, DecisionID: ref.DecisionID,
				})
			}
		}
	}
	return out
}

// definitionsStrandedBy is the guard the runtime policy needs on top of the pins
// (ADR-draft-a-business-rule-task-chooses-its-decision-version): which deployed
// definitions would be left with a latest-bound task that nothing can answer.
//
// Under that policy latest is the newest decision deployment when the job is
// worked, else the model bundled with the process. Deleting a version while an
// older one survives is already refused by currentVersionsWithHistory, so the only
// delete that changes what latest resolves to is the one that removes a decision's
// last deployment — and it strands a definition only when that definition carries
// no copy of the decision itself.
func (s *Server) definitionsStrandedBy(rec persistedDecision, all []persistedDecision) []decisionPinRef {
	remaining := map[string]bool{}
	for _, other := range all {
		if other.Key == rec.Key {
			continue
		}
		for _, d := range other.Decisions {
			remaining[d.ID] = true
		}
	}
	going := map[string]bool{}
	for _, d := range rec.Decisions {
		if !remaining[d.ID] {
			going[d.ID] = true
		}
	}
	if len(going) == 0 {
		return nil
	}
	var out []decisionPinRef
	for _, defKey := range s.order {
		d := s.deployments[defKey]
		if d == nil || d.cp == nil || !d.cp.LatestAtRuntime() {
			continue
		}
		for _, id := range d.cp.LatestBoundDecisions() {
			if going[id] && !s.dmnRegistry.Provides(d.Key, id) {
				out = append(out, decisionPinRef{
					Key: d.Key, ProcessID: d.ProcessID, Name: d.Name, Version: d.Version, DecisionID: id,
				})
			}
		}
	}
	return out
}

// strandedRefusal names the definitions a delete would leave with nothing to
// evaluate.
func strandedRefusal(key uint64, stranded []decisionPinRef) string {
	names := make([]string, 0, len(stranded))
	for _, p := range stranded {
		names = append(names, fmt.Sprintf("%s v%d (key %d, decision %s)", p.ProcessID, p.Version, p.Key, p.DecisionID))
	}
	return fmt.Sprintf(
		"decision deployment %d is the last deployed version of a decision that %s as latest, with no copy bundled: %v. Deleting it would leave a business rule task that cannot evaluate — deploy another version first, or undeploy %s",
		key,
		plural(len(stranded), "a deployed process evaluates", "deployed processes evaluate"),
		names,
		plural(len(stranded), "that definition", "those definitions"))
}

// pinnedRefusal names the definitions rather than counting them, so the operator
// can go and look at each one — the way the process delete's refusal names what is
// running.
func pinnedRefusal(key uint64, pins []decisionPinRef) string {
	names := make([]string, 0, len(pins))
	for _, p := range pins {
		names = append(names, fmt.Sprintf("%s v%d (key %d, decision %s)", p.ProcessID, p.Version, p.Key, p.DecisionID))
	}
	return fmt.Sprintf(
		"decision deployment %d is what %s pinned to at deploy time, and %s carry no copy of it: %v. Deleting it would leave a business rule task that cannot evaluate — undeploy %s first",
		key,
		plural(len(pins), "a deployed process is", "deployed processes are"),
		plural(len(pins), "it does", "they do"),
		names,
		plural(len(pins), "that definition", "those definitions"))
}

// currentVersionsWithHistory reports the decisions this record is the *current*
// version of and that have an older version still deployed.
//
// That combination is the second refusal. Deleting it would do two things quietly:
// send the next process deploy's latest binding back to an older version with
// nothing recording the change, and lower the per-decision version high-water mark,
// which is derived from the surviving records — so the deploy after that would mint
// a version number an existing record, a release manifest and an editor chip
// already use for different content.
//
// Neither applies when the whole history goes: a decision with no deployments left
// starts again at v1, and no survivor claims otherwise.
func currentVersionsWithHistory(rec persistedDecision, all []persistedDecision) []string {
	var out []string
	for _, mine := range rec.Decisions {
		newest, older := int32(0), false
		for _, other := range all {
			if other.Key == rec.Key {
				continue
			}
			for _, d := range other.Decisions {
				if d.ID != mine.ID {
					continue
				}
				older = true
				if d.Version > newest {
					newest = d.Version
				}
			}
		}
		if older && mine.Version > newest {
			out = append(out, fmt.Sprintf("%s v%d", mine.ID, mine.Version))
		}
	}
	sort.Strings(out)
	return out
}

// recountDecisionVersions rebuilds the per-decision version counter from the
// records that will survive, which is what a restart derives it from. Keeping the
// live counter and the recovered one in step is the whole point: a version minted
// after a delete must be the same one minted after a delete and a reboot.
func (s *Server) recountDecisionVersions(all []persistedDecision, removed uint64) {
	next := map[string]int32{}
	s.decisionVersionKeys = map[string]map[int32]uint64{}
	for _, rec := range all {
		if rec.Key == removed {
			continue
		}
		for _, d := range rec.Decisions {
			if d.Version > next[d.ID] {
				next[d.ID] = d.Version
			}
		}
		s.indexDecisionVersions(rec)
	}
	s.decisionVersions = next
}

// plural picks the singular or plural wording for a count, so a refusal reads as a
// sentence rather than as "1 definition(s)".
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
