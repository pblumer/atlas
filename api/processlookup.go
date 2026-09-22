package api

import (
	"slices"
	"strings"

	"github.com/pblumer/atlas/compiler"
)

// What the catalogue's fulfilment report asks the engine.
//
// The report (api/catalog/fulfilmentreport.go) is a pure function over two
// answers this server alone can give: which processes are deployed, and which job
// types anything works. Both live here so the catalogue package stays free of the
// engine, the way its approver report stays free of the account store.

// processLookup answers for the running engine.
type processLookup struct{ s *Server }

// JobTypesOf names the job types the newest deployed version of this process id
// carries, and says whether any version carries it.
//
// The newest version, because that is the one an instance starts on: a superseded
// version whose tasks were bound differently is not what an order placed today
// would run. A process id nothing carries is "not deployed" whatever else is in
// the registry.
func (l processLookup) JobTypesOf(processID string) ([]string, bool) {
	processID = strings.TrimSpace(processID)
	if processID == "" {
		return nil, false
	}
	var (
		found bool
		out   []string
	)
	l.s.do(func() {
		var (
			best int32
			cp   *compiler.CompiledProcess
		)
		for _, d := range l.s.deployments {
			if d.ProcessID != processID || d.cp == nil {
				continue
			}
			if !found || d.Version > best {
				found, best, cp = true, d.Version, d.cp
			}
		}
		if cp == nil {
			return
		}
		for _, idx := range jobCreatingTypes(cp) {
			if name, ok := l.s.jobTypes.Name(idx); ok {
				out = append(out, name)
			}
		}
	})
	return out, found
}

// Served reports whether anything works this job type.
//
// Three ways it can be worked, and the report must not know about any of them:
//
//   - the engine serves the kind itself, holding its credentials (ADR-0163);
//   - it is a user task, which a person works — a queue waiting for a human is
//     the design and not a gap;
//   - a worker has been seen pulling it since this server started.
//
// That last clause is the same honest reading [Server.unservedConnectors] takes,
// and it has the same cost: a worker that has never polled reports nothing, so
// just after a restart a type reads as unworked until the first poll. An operator
// looking at a queue that is not moving wants to know that nothing has claimed
// it, and the answer corrects itself within one heartbeat.
func (l processLookup) Served(jobType string) bool {
	jobType = strings.TrimSpace(jobType)
	if jobType == "" {
		return true // nothing to serve
	}
	served := false
	l.s.do(func() {
		idx, known := l.s.jobTypes.Index(jobType)
		if !known {
			// A type no deployment interned cannot have a job waiting on it.
			served = true
			return
		}
		if idx == compiler.UserTaskJobTypeIndex || l.s.jobRunner.Handles(idx) {
			served = true
			return
		}
		for _, st := range l.s.workers.byName {
			// What it asked for, not what it got. A worker polling a queue with no
			// work in it has leased nothing, and counting only leases would report
			// every quiet queue on a healthy installation as unserved.
			if slices.Contains(st.Serves, jobType) {
				served = true
				return
			}
			if _, pulls := st.Types[jobType]; pulls {
				served = true
				return
			}
		}
	})
	return served
}
