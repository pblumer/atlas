package capability

import "sort"

// Turning what the engine recorded into what the record declared it would be held to.
//
// The split is deliberate: everything here is a pure function over values the caller
// supplies, and every read of the engine happens in the resolver that produced them.
// That is what makes the arithmetic testable without a store, and it is also what
// keeps this package free of the engine — the capability register is design-time
// (ADR-0305) and stays so even when it reports on runtime.

// RecordedProcess is what a resolver read for one realising process. It is the shape
// the engine's three data sources reduce to, and nothing in this package knows how
// they were read.
type RecordedProcess struct {
	ProcessID string
	Name      string
	// Restricted: the caller may not see this process. Its numbers are absent, and
	// absent is not zero.
	Restricted bool
	// Deployed: this server currently runs it. A realisation naming a process that is
	// not deployed here is a fact about the installation, and the gap report is where
	// it is a finding; here it only means there is nothing to measure.
	Deployed bool

	// EndEvents and Cancellations are all-time counters (ADR-0080).
	EndEvents     []ElementCount
	Cancellations []ElementCount
	// Durations are the cycle times, in seconds, of the finished instances inside the
	// window — one entry per case.
	//
	// A slice rather than a pre-folded summary because the SLA attainment needs to
	// count against a threshold and the cycle-time summary needs a mean, and folding
	// twice over one slice in memory is cheaper than walking the store twice. The
	// resolver bounds its length by bounding the window, which is the same bound that
	// makes the walk affordable at all.
	Durations []int64
}

// Measure folds what was recorded into the answer for one capability.
//
// now is not a parameter: everything time-dependent was decided when the window was
// parsed, and taking a clock here would let the answer drift between the read and the
// arithmetic over it.
func Measure(c Capability, w Window, recorded []RecordedProcess) Measurement {
	m := Measurement{
		Key:          c.Key,
		Name:         c.Name,
		Window:       w,
		WindowDays:   w.Days(),
		CountedBasis: CountedBasis,
		WalkedBasis:  WalkedBasis,
		Processes:    []ProcessMeasurement{},
		SLAs:         []SLAAttainment{},
		NotMeasured:  []NotMeasured{},
	}

	// All the cases of every realisation, pooled. An SLA is a promise about the
	// capability, not about one of the processes that happen to implement it, so its
	// attainment is over everything that performed it — which is also why a capability
	// realised two ways still has one attainment figure and two cycle times.
	var allDurations []int64
	measurable := false

	for _, r := range recorded {
		p := ProcessMeasurement{
			ProcessID:  r.ProcessID,
			Name:       r.Name,
			Restricted: r.Restricted,
			Deployed:   r.Deployed,
			Outcomes:   []ElementCount{},
		}
		if r.Restricted || !r.Deployed {
			// Nothing to report, and the two flags already say which of the two
			// reasons it is. Zero-filling the figures here would make "not visible"
			// and "ran nothing" identical, which they are not.
			m.Processes = append(m.Processes, p)
			continue
		}
		measurable = true
		p.Outcomes = sortedCounts(r.EndEvents)
		p.Cancellations = sortedCounts(r.Cancellations)
		p.Cases = summarize(r.Durations)
		allDurations = append(allDurations, r.Durations...)
		m.Processes = append(m.Processes, p)
	}

	m.Unrealizable = !measurable

	for _, sla := range c.SLAs {
		if sla.ThresholdSeconds <= 0 {
			m.NotMeasured = append(m.NotMeasured, NotMeasured{
				Kind: "sla", Name: sla.Name, Reason: ReasonProseThreshold,
			})
			continue
		}
		m.SLAs = append(m.SLAs, attainment(sla, allDurations))
	}
	for _, kpi := range c.KPIs {
		m.NotMeasured = append(m.NotMeasured, NotMeasured{
			Kind: "kpi", Name: kpi.Name, Reason: ReasonKPIsAreDirections,
		})
	}
	return m
}

// attainment counts how many of the pooled cases met the threshold.
//
// Counting rather than sorting is what keeps this affordable: the question an SLA
// asks — "what share came in under the threshold" — is a predicate over each case,
// so it needs one pass and no memory beyond two integers. The percentile a reader
// might reach for instead would need every value held at once.
func attainment(sla SLA, durations []int64) SLAAttainment {
	a := SLAAttainment{
		Name:             sla.Name,
		Metric:           sla.Metric,
		ThresholdSeconds: sla.ThresholdSeconds,
		Cases:            int64(len(durations)),
	}
	for _, d := range durations {
		// At the threshold is within it: an SLA of "within ten minutes" is met by a
		// case that took exactly ten.
		if d <= sla.ThresholdSeconds {
			a.Within++
		}
	}
	if a.Cases > 0 {
		a.Share = float64(a.Within) / float64(a.Cases)
	}
	return a
}

// summarize folds cycle times into the streaming figures.
func summarize(durations []int64) CaseStats {
	s := CaseStats{Cases: int64(len(durations))}
	if s.Cases == 0 {
		return s
	}
	var sum int64
	s.MinSeconds, s.MaxSeconds = durations[0], durations[0]
	for _, d := range durations {
		sum += d
		if d < s.MinSeconds {
			s.MinSeconds = d
		}
		if d > s.MaxSeconds {
			s.MaxSeconds = d
		}
	}
	s.MeanSeconds = float64(sum) / float64(s.Cases)
	return s
}

// sortedCounts orders element counts highest first, then by element id.
//
// Highest first because the question these answer is "how did cases end", and the
// commonest ending is the one a reader looks for. By element id after that so two
// outcomes with the same count come back in the same order on every read — an
// ordering that varies between reads makes a diff of two responses unreadable.
func sortedCounts(in []ElementCount) []ElementCount {
	out := append([]ElementCount(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].ElementID < out[j].ElementID
	})
	if out == nil {
		return []ElementCount{}
	}
	return out
}
