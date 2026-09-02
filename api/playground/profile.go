package playground

import (
	"fmt"
	"net/http"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/playground"
)

// arrivalProfileReq asks what a stream would look like, without running it.
type arrivalProfileReq struct {
	// Count is how many cases arrive. It is a number rather than the cases
	// themselves because the timing does not depend on them.
	Count   int        `json:"count"`
	Arrival arrivalReq `json:"arrival"`
}

// arrivalProfileResp is the shape of a planned stream: how many cases land in
// each slice of the time it covers.
type arrivalProfileResp struct {
	// Scheduled is false for a sequential plan, whose next arrival waits on the
	// run and so has no shape to draw ahead of it.
	Scheduled bool `json:"scheduled"`
	Cases     int  `json:"cases"`
	// Start and End bound the stream, and SpanMillis is the distance between them.
	Start      string `json:"start,omitempty"`
	End        string `json:"end,omitempty"`
	SpanMillis int64  `json:"spanMillis"`
	// Peak is the fullest slice, which is what a drawn profile is scaled against.
	// It is sent rather than left to the client so that the number under the
	// picture and the height of the picture cannot disagree.
	Peak    int   `json:"peak"`
	Buckets []int `json:"buckets,omitempty"`
}

// HandleArrivalProfile reports the shape of a stream before it is run.
//
// It exists so a panel can draw the timing somebody just typed. The arithmetic is
// the planner's own — the same call the run makes — because a profile drawn from
// a second implementation of it in a browser is a picture of a stream nobody is
// going to get.
func (s *Service) HandleArrivalProfile(w http.ResponseWriter, r *http.Request) {
	var req arrivalProfileReq
	if !decode(w, r, maxBodyBytes, &req) {
		return
	}
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	if req.Count > maxCasesPerRun {
		httpapi.Error(w, http.StatusBadRequest,
			fmt.Sprintf("a run holds at most %d cases; this profile asks for %d", maxCasesPerRun, req.Count))
		return
	}
	arrival, err := req.Arrival.toArrival()
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	// A profile the planner refuses is a 400 about what was asked for, so the
	// refusal is carried out of the session rather than reported as a server fault.
	var (
		profile playground.ArrivalProfile
		planErr error
	)
	if !s.ok(w, sess.With(func(sb *playground.Sandbox) error {
		profile, planErr = sb.ArrivalProfileOf(req.Count, arrival)
		return nil
	})) {
		return
	}
	if planErr != nil {
		httpapi.Error(w, http.StatusBadRequest, planErr.Error())
		return
	}
	out := arrivalProfileResp{
		Scheduled: profile.Scheduled, Cases: profile.Cases,
		Peak: profile.Peak(), Buckets: profile.Buckets,
	}
	if profile.Scheduled {
		out.Start, out.End = rfc3339(profile.Start), rfc3339(profile.End)
		out.SpanMillis = profile.Span().Milliseconds()
	}
	httpapi.JSON(w, http.StatusOK, out)
}
