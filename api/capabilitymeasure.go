package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/capability"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// measureCapability reads what the engine recorded for a capability's realising
// processes, over one window (ADR-0309; the evidence that this
// is affordable at all is benchmarks/results/measurement-381825f.md).
//
// It is the only read in the business-architecture area that does **not** run inside
// the run loop, and the split is the whole point of the function's shape:
//
//   - On the loop, briefly: resolve each process id to the definition currently
//     deployed under it, and decide whether this caller may see it. That is
//     design-time size — the number of deployments — exactly like the landscape read
//     beside it.
//   - Off the loop, through [Server.readOffLoop]: the counters and the instance walk.
//     The walk is linear in the instances the window holds, and holding Atlas's single
//     writer for that is how one operator's question becomes everybody's outage
//     (ADR-0239).
//
// The two halves cannot be merged. A snapshot handle is what makes the off-loop half
// consistent, and taking one is itself a loop turn.
func (s *Server) measureCapability(r *http.Request, processIDs []string, w capability.Window) ([]capability.RecordedProcess, error) {
	// One target per realisation, in the order the record named them: a response that
	// reordered a capability's realisations between reads would be undiffable.
	type target struct {
		processID string
		name      string
		defKey    uint64
		cp        *compiler.CompiledProcess
		visible   bool
		deployed  bool
	}
	targets := make([]target, 0, len(processIDs))

	ran := false
	s.do(func() {
		ran = true
		projs, err := s.projectsByID()
		if err != nil {
			// Reported as "not deployed" rather than failing the whole measurement:
			// the application store being unreadable is not a statement about this
			// capability, and a partial answer that says which realisations it could
			// not read beats no answer at all.
			projs = nil
		}
		for _, pid := range processIDs {
			t := target{processID: pid}
			if d := s.latestDeploymentByProcessID(pid); d != nil && d.cp != nil {
				t.deployed, t.defKey, t.cp, t.name = true, d.cp.Key, d.cp, d.Name
				t.visible = s.canViewArtifact(r, d.ProjectID, d.DeployedBy, projs)
			}
			targets = append(targets, t)
		}
	})
	if !ran {
		return nil, errLoopClosing
	}

	out := make([]capability.RecordedProcess, 0, len(targets))
	for _, t := range targets {
		rec := capability.RecordedProcess{
			ProcessID: t.processID,
			Name:      t.name,
			Deployed:  t.deployed,
			// Restricted only where there is something to be restricted *from*: a
			// process nothing here deploys is not withheld, it is absent, and saying
			// both would leave a reader unable to tell which.
			Restricted: t.deployed && !t.visible,
		}
		out = append(out, rec)
	}

	err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		for i, t := range targets {
			if !t.deployed || !t.visible {
				continue
			}
			var err error
			if out[i].EndEvents, err = endEventCounts(rv, t.defKey, t.cp); err != nil {
				return err
			}
			if out[i].Cancellations, err = cancellationCounts(rv, t.defKey, t.cp); err != nil {
				return err
			}
			if out[i].Durations, err = cycleTimes(rv, t.defKey, w); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// endEventCounts reads the maintained visit counters and keeps the end events.
//
// End events only, because the question these answer is how a case *ended* — and the
// method asks for distinctly named end events precisely so that this count reads as an
// outcome distribution rather than as a heatmap. Every other element's visit count is
// a real number about a real element and simply answers a different question.
//
// **The element's name is not here, and that is a gap rather than a choice.** The
// compiler interns an element name only for a user task, where the Tasks app shows it;
// an end event's name is read and dropped. So what this can report is the BPMN element
// id, which is a join key — a client that wants "Declined" reads the process XML, which
// the API already serves — and not the label the method asks authors to write. Closing
// it means compiling the name, which is a change to the compiled process and belongs
// in its own slice.
//
// All-time: a counter holds a total, not a series, so this cannot be windowed. The
// response says so in its own body rather than leaving the reader to assume.
func endEventCounts(rv *state.ReadView, defKey uint64, cp *compiler.CompiledProcess) ([]capability.ElementCount, error) {
	var out []capability.ElementCount
	err := rv.ElementVisitTotals(defKey, func(elementID int32, count int64) error {
		node := cp.Node(elementID)
		if !isEndEvent(node.Type) {
			return nil
		}
		out = append(out, capability.ElementCount{
			ElementID: cp.ElementBpmnId(elementID),
			Count:     count,
		})
		return nil
	})
	return out, err
}

// cancellationCounts reads the termination counters — how often a token left an
// element cancelled rather than completed. Every element, not just end events: a
// cancellation is interesting wherever it happened, and an end event is the one place
// it cannot.
func cancellationCounts(rv *state.ReadView, defKey uint64, cp *compiler.CompiledProcess) ([]capability.ElementCount, error) {
	var out []capability.ElementCount
	err := rv.ElementTerminationTotals(defKey, func(elementID int32, count int64) error {
		if count == 0 {
			return nil
		}
		out = append(out, capability.ElementCount{
			ElementID: cp.ElementBpmnId(elementID),
			Count:     count,
		})
		return nil
	})
	return out, err
}

// cycleTimes walks the finished instances inside the window and returns each one's
// cycle time in seconds.
//
// The walk is over the completion-ordered index, newest first, so the window is a
// range that stops rather than a filter that reads everything and discards. That is
// what makes it affordable: the cost is the window's contents, not the definition's
// history.
func cycleTimes(rv *state.ReadView, defKey uint64, w capability.Window) ([]int64, error) {
	var out []int64
	err := rv.FinishedInstancesOfDefDesc(defKey, 0, 0, func(_ uint64, v *model.ProcessInstanceValue) error {
		// CompletedAt and CreatedAt are Unix nanoseconds; the window is seconds.
		completed := v.CompletedAt / 1e9
		if completed > w.To {
			return nil // newer than the window; the walk starts at the newest
		}
		if completed < w.From {
			// Older than the window — and the index is ordered by completion, so
			// everything after this is older still. Stopping here is what makes the
			// cost the window rather than the history, and it is the same page-full
			// sentinel every other bounded walk in this package uses.
			return errListTruncated
		}
		if v.CompletedAt > v.CreatedAt {
			out = append(out, (v.CompletedAt-v.CreatedAt)/1e9)
		}
		return nil
	})
	return out, unlessTruncated(err)
}

// isEndEvent reports whether a compiled node is one of BPMN's end events. Every kind
// counts, not only the plain one: a case that ended by throwing an error or by
// terminating its scope ended, and an outcome distribution that silently dropped those
// would report a success rate that is too high.
func isEndEvent(t compiler.BpmnType) bool {
	switch t {
	case compiler.TypeEndEvent, compiler.TypeMessageEndEvent, compiler.TypeSignalEndEvent,
		compiler.TypeErrorEndEvent, compiler.TypeEscalationEndEvent,
		compiler.TypeCompensationEndEvent, compiler.TypeCancelEndEvent,
		compiler.TypeTerminateEndEvent:
		return true
	}
	return false
}
