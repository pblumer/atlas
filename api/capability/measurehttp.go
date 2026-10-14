package capability

import (
	"net/http"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
)

// MeasurementResolver reads what the engine recorded for a capability's realising
// processes, over one window, filtered for the caller.
//
// It is injected rather than implemented here for the same reason the landscape
// resolver is: this package holds a design-time register and must not reach into the
// engine. But the reason it is a *separate* resolver from the landscape one is
// sharper than symmetry — this read walks instances, so it must run **off the run
// loop** (ADR-0239), and the coverage and gap reads run on it. Handing both to one
// resolver would make the difference invisible at the call site, and the difference is
// the one that decides whether a query can stop the engine.
type MeasurementResolver func(r *http.Request, realizations []string, w Window) ([]RecordedProcess, error)

// HandleMeasurement answers what a capability's declared KPIs and SLAs actually came
// to, over a window the caller must name.
//
// Unlike every other read in this service it does not go through [Service.dispatch].
// The resolver takes what it needs from the loop itself — a snapshot handle and the
// deployment metadata — and does the walking outside it, because the walk is linear in
// the instances the window holds and holding Atlas's single writer for that is how one
// operator's question becomes everybody's outage.
func (s *Service) HandleMeasurement(w http.ResponseWriter, r *http.Request) {
	if s.measure == nil {
		// No resolver wired: the service can hold records but this server cannot read
		// runtime. Said plainly rather than answered with an empty measurement, which
		// would read as "nothing ran".
		httpapi.Error(w, http.StatusNotImplemented,
			"this server cannot measure: no runtime resolver is configured")
		return
	}
	days, ok := windowDays(w, r)
	if !ok {
		return
	}
	window, err := ParseWindow(days, s.now())
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	// One loop turn, for the record only. Everything after it is off the loop.
	var (
		target Capability
		found  bool
		opErr  error
	)
	if !s.dispatch(func() {
		target, found, opErr = s.caps.Get(r.PathValue("key"))
	}) {
		writeShuttingDown(w)
		return
	}
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read capability: "+opErr.Error())
		return
	}
	if !found {
		httpapi.Error(w, http.StatusNotFound, notFoundCapability)
		return
	}

	recorded, err := s.measure(r, processRealizations(target), window)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "measure: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, Measure(target, window, recorded))
}

// processRealizations is the subset of a capability's realisations the engine can
// measure: the ones naming a process here.
//
// A capability realised by a purchased system or by a person is not measurable and is
// not an error — it is the case the register exists to record. Passing those to the
// resolver would make it answer "not deployed" about a realisation that was never
// meant to be deployed, which reads as a gap where there is none.
func processRealizations(c Capability) []string {
	var out []string
	for _, r := range c.Realizations {
		if r.Kind == RealizationProcess && r.ProcessID != "" {
			out = append(out, r.ProcessID)
		}
	}
	return out
}

// windowDays reads ?windowDays=, refusing anything that is not a number. It writes
// its own refusal and reports whether the caller should carry on.
func windowDays(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("windowDays")
	if raw == "" {
		httpapi.Error(w, http.StatusBadRequest,
			"windowDays is required: a measurement is over a window, and an unbounded one is not offered")
		return 0, false
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "windowDays must be a whole number of days")
		return 0, false
	}
	return days, true
}
