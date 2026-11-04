package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/layout"
)

// Comparing two deployed versions of a process (ADR-0530).
//
// A version is immutable and every deploy mints one, so "what changed between v6
// and v7" is a question every process with a history raises: before migrating
// instances onto the newer one, when a run behaves differently from last week's, or
// in a review of what a publish deployed. The answer used to be two XML documents to
// read side by side — and their diagrams, formatting and attribute order differ in
// every place the model does not.
//
// The comparison is the server's for the reason ADR-0452 gave: "the same model" must
// not depend on how a document was written. layout.Changes lines the two models up
// by element id and lists what each element states differently, the diagram
// ignored; layout.Difference still answers whether the two are the same model at
// all, because the list leaves out what the compiler does not read.

// comparedVersion names one side of a comparison: which version, and who deployed it.
type comparedVersion struct {
	Key            uint64 `json:"key"`
	Version        int32  `json:"version"`
	VersionTag     string `json:"versionTag,omitempty"`
	DeployedAt     int64  `json:"deployedAt"`
	DeployedBy     string `json:"deployedBy,omitempty"`
	DeployedByName string `json:"deployedByName,omitempty"`
}

// changeCounts sums a change list by kind, so a reader can say "3 added, 1 removed,
// 4 changed" without walking it.
type changeCounts struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed int `json:"changed"`
}

func countChanges(cs []layout.Change) changeCounts {
	var n changeCounts
	for _, c := range cs {
		switch c.Kind {
		case layout.ChangeAdded:
			n.Added++
		case layout.ChangeRemoved:
			n.Removed++
		default:
			n.Changed++
		}
	}
	return n
}

// processComparisonResp is what changed from one version of a process to another.
type processComparisonResp struct {
	ProcessID string          `json:"processId"`
	From      comparedVersion `json:"from"`
	To        comparedVersion `json:"to"`
	// SameModel reports whether the two describe the same process everywhere the
	// engine looks; the diagram is not compared.
	SameModel bool `json:"sameModel"`
	// Difference names where they first part, when they do — said even when the
	// list is empty, because the list leaves out the hints the compiler ignores.
	Difference string `json:"difference,omitempty"`
	// Changes is every element added, removed or changed from From to To.
	Changes []layout.Change `json:"changes"`
	Counts  changeCounts    `json:"counts"`
}

// handleProcessComparison answers what changed from one deployed version of a
// process to another: from the version ?with= names to the one in the path. Without
// ?with= it is the version before — the highest version number below the path's —
// which is the question a new version raises. Both must be versions of the same
// process: two different processes share no element ids, and a list that said every
// element was removed and added would be true and useless.
func (s *Server) handleProcessComparison(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid definition key")
		return
	}
	var with uint64
	if q := r.URL.Query().Get("with"); q != "" {
		if with, err = strconv.ParseUint(q, 10, 64); err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid definition key in ?with=")
			return
		}
	}

	type side struct {
		ref comparedVersion
		xml []byte
	}
	var (
		processID    string
		to, from     side
		found        bool
		unknownWith  bool
		otherProcess string
		noEarlier    int32
	)
	p := httpapi.PrincipalFrom(r.Context())
	// The XML slices are copied out of the registry to be read after the writer's
	// turn: a layout save replaces a deployment's XML, never writes into it.
	take := func(d *deployment) side {
		return side{ref: comparedVersion{
			Key: d.Key, Version: d.Version, VersionTag: d.cp.VersionTag(),
			DeployedAt: d.DeployedAt, DeployedBy: d.DeployedBy,
		}, xml: d.xml}
	}
	s.do(func() {
		d, ok := s.deployments[key]
		if !ok || !definitionReadable(p, d) {
			return
		}
		found = true
		processID = d.ProcessID
		to = take(d)
		if with != 0 {
			o, ok := s.deployments[with]
			switch {
			case !ok || !definitionReadable(p, o):
				unknownWith = true
			case o.ProcessID != d.ProcessID:
				otherProcess = o.ProcessID
			default:
				from = take(o)
			}
			return
		}
		var prev *deployment
		for _, k := range s.order {
			v := s.deployments[k]
			if v == nil || v.ProcessID != d.ProcessID || v.Version >= d.Version || !definitionReadable(p, v) {
				continue
			}
			if prev == nil || v.Version > prev.Version {
				prev = v
			}
		}
		if prev == nil {
			noEarlier = d.Version
			return
		}
		from = take(prev)
	})
	switch {
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
		return
	case unknownWith:
		httpapi.Error(w, http.StatusNotFound, "no deployment with the key ?with= names")
		return
	case otherProcess != "":
		httpapi.Error(w, http.StatusBadRequest, fmt.Sprintf(
			"definition %d is a version of %q, not of %q: only versions of one process can be compared", with, otherProcess, processID))
		return
	case noEarlier != 0:
		httpapi.Error(w, http.StatusNotFound, fmt.Sprintf(
			"version %d is the first version of %q: there is no earlier version to compare it with; name one with ?with=", noEarlier, processID))
		return
	}

	// Off the loop: parses of two documents that grow with the model.
	diff, changes, err := layout.Compare(from.xml, to.xml, fmt.Sprintf("version %d", from.ref.Version), fmt.Sprintf("version %d", to.ref.Version))
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "compare the versions: "+err.Error())
		return
	}
	name := s.principalNamer()
	from.ref.DeployedByName = name(from.ref.DeployedBy)
	to.ref.DeployedByName = name(to.ref.DeployedBy)
	httpapi.JSON(w, http.StatusOK, processComparisonResp{
		ProcessID: processID, From: from.ref, To: to.ref,
		SameModel: diff == "", Difference: diff,
		Changes: changes, Counts: countChanges(changes),
	})
}
