package playground

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The panel draws the timing somebody just typed, before anything runs. What it
// draws is the planner's own arithmetic: the same call the run makes, so the
// picture and the run cannot come apart.
func TestAnArrivalProfileIsPreviewedBeforeTheRun(t *testing.T) {
	svc := newService(t)
	vals := map[string]string{"id": openBatchSession(t, svc)}

	var out arrivalProfileResp
	decodeInto(t, call(t, svc.HandleArrivalProfile, http.MethodPost,
		`{"count":120,"arrival":{"mode":"poisson","perHour":12}}`, vals), &out)

	if !out.Scheduled || out.Cases != 120 {
		t.Fatalf("profile = %+v, want 120 scheduled cases", out)
	}
	total := 0
	for _, n := range out.Buckets {
		total += n
	}
	if total != 120 {
		t.Errorf("the buckets hold %d cases, want 120", total)
	}
	if out.Peak < 1 {
		t.Errorf("peak = %d, want the fullest slice", out.Peak)
	}
	start, err := time.Parse(time.RFC3339, out.Start)
	if err != nil {
		t.Fatalf("start %q: %v", out.Start, err)
	}
	end, err := time.Parse(time.RFC3339, out.End)
	if err != nil {
		t.Fatalf("end %q: %v", out.End, err)
	}
	// The bounds are second-resolution on the wire and the span is not, so they
	// agree to within the rounding rather than exactly.
	if drift := out.SpanMillis - end.Sub(start).Milliseconds(); !end.After(start) || drift < 0 || drift >= 1000 {
		t.Errorf("%s–%s spans %dms; the two should agree", out.Start, out.End, out.SpanMillis)
	}
}

// A calendar reaches the profile the same way it reaches a run, so the preview
// shows the stream pausing overnight rather than running through it.
func TestAnArrivalProfileHonoursTheCalendar(t *testing.T) {
	svc := newService(t)
	vals := map[string]string{"id": openBatchSession(t, svc)}

	body := `{"count":20,"arrival":{"mode":"every","intervalMillis":3600000,` +
		`"calendar":{"open":[{"fromMinutes":480,"toMinutes":1020}]}}}`
	var out arrivalProfileResp
	decodeInto(t, call(t, svc.HandleArrivalProfile, http.MethodPost, body, vals), &out)

	// Twenty hourly cases need three working days, so the stream outlasts two.
	if out.SpanMillis <= (48 * time.Hour).Milliseconds() {
		t.Errorf("span = %dms; twenty hourly cases on a nine-hour day should outlast two days", out.SpanMillis)
	}
	end, err := time.Parse(time.RFC3339, out.End)
	if err != nil {
		t.Fatalf("end %q: %v", out.End, err)
	}
	if end.Hour() < 8 || end.Hour() >= 17 {
		t.Errorf("the last case arrives at %s, outside business hours", out.End)
	}
}

// A sequential plan has no schedule ahead of the run, and says so instead of
// sending a flat line that would read as one.
func TestASequentialPlanIsReportedAsUnscheduled(t *testing.T) {
	svc := newService(t)
	vals := map[string]string{"id": openBatchSession(t, svc)}

	var out arrivalProfileResp
	decodeInto(t, call(t, svc.HandleArrivalProfile, http.MethodPost,
		`{"count":10,"arrival":{"mode":"sequential"}}`, vals), &out)

	if out.Scheduled {
		t.Error("a sequential plan should not report a schedule")
	}
	if out.Cases != 10 {
		t.Errorf("cases = %d, want the 10 asked for", out.Cases)
	}
	if len(out.Buckets) != 0 || out.Peak != 0 || out.Start != "" || out.SpanMillis != 0 {
		t.Errorf("profile = %+v, want nothing drawn", out)
	}
}

// Every refusal the plan makes, the preview makes too — with the reason, because
// the panel shows it where the numbers were typed.
func TestArrivalProfileRefusals(t *testing.T) {
	svc := newService(t)
	vals := map[string]string{"id": openBatchSession(t, svc)}

	cases := map[string]struct{ body, want string }{
		"an arrival mode nobody has":    {`{"count":5,"arrival":{"mode":"drip"}}`, "not one of"},
		"a takt with no interval":       {`{"count":5,"arrival":{"mode":"every"}}`, "positive interval"},
		"a Poisson stream with no rate": {`{"count":5,"arrival":{"mode":"poisson"}}`, "positive rate"},
		"no cases at all":               {`{"count":0}`, "at least one case"},
		"more cases than a run holds":   {`{"count":50001}`, "at most 50000 cases"},
		"a window that ends before it starts": {
			`{"count":5,"arrival":{"mode":"every","intervalMillis":60000,` +
				`"calendar":{"open":[{"fromMinutes":1020,"toMinutes":480}]}}}`, "ends at or before it starts"},
		"a body that is not JSON": {`{`, "invalid JSON"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := call(t, svc.HandleArrivalProfile, http.MethodPost, c.body, vals)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), c.want) {
				t.Errorf("body = %s, want it to mention %q", rec.Body, c.want)
			}
		})
	}
}

// A profile of somebody else's session is a 404 before any arithmetic.
func TestAnArrivalProfileNeedsItsOwnSession(t *testing.T) {
	svc := newService(t)
	rec := call(t, svc.HandleArrivalProfile, http.MethodPost,
		`{"count":5}`, map[string]string{"id": "nope"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (body %s)", rec.Code, rec.Body)
	}
}
