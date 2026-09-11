package capability

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The freshness rule, as a property of the record.

func TestStaleAt(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	confirmed := func(d time.Duration) Confirmation {
		return Confirmation{At: now.Add(-d).Unix(), By: "architect"}
	}
	const day = 24 * time.Hour
	tests := []struct {
		name    string
		c       Confirmation
		horizon int
		want    bool
	}{
		{name: "never confirmed is stale", c: Confirmation{}, horizon: 12, want: true},
		{name: "confirmed today", c: confirmed(0), horizon: 12},
		{name: "confirmed eleven months ago", c: confirmed(330 * day), horizon: 12},
		{name: "confirmed thirteen months ago", c: confirmed(400 * day), horizon: 12, want: true},
		{name: "a quarterly horizon", c: confirmed(120 * day), horizon: 3, want: true},
		{name: "a quarterly horizon, just inside", c: confirmed(60 * day), horizon: 3},
		{
			// The hundred-year interval the record names as the way to silence this. It
			// works, and that is accepted: it is one visible number, and every answer
			// that applies it says which one it applied.
			name: "a horizon nothing outlives", c: confirmed(3650 * day), horizon: 1200,
		},
		{
			// Zero switches it off entirely rather than making everything stale, which
			// would be a configuration mistake turning the whole map red.
			name: "a horizon of zero switches the check off", c: confirmed(3650 * day), horizon: 0,
		},
		{name: "a negative horizon is read as off", c: confirmed(3650 * day), horizon: -1},
		{
			// Off still does not rescue a record nobody confirmed: there is no date to
			// compare, so there is nothing to be lenient about.
			name: "never confirmed is stale even with the check off", c: Confirmation{}, horizon: 0, want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.StaleAt(now, tt.horizon); got != tt.want {
				t.Errorf("StaleAt(%d months) = %v, want %v", tt.horizon, got, tt.want)
			}
		})
	}
}

func TestConfirmed(t *testing.T) {
	if (Confirmation{}).Confirmed() {
		t.Error("an empty confirmation reports as confirmed")
	}
	if !(Confirmation{At: 1}).Confirmed() {
		t.Error("a dated confirmation reports as unconfirmed")
	}
}

// TestViewSelfConfirmation: the map confirming itself is shown, never reported.
func TestViewSelfConfirmation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	self := viewConfirmation(Confirmation{At: now.Unix(), By: "architect"}, "architect", now, 12)
	if !self.SelfConfirmed {
		t.Error("a confirmation by the last editor was not marked as self-confirmed")
	}
	other := viewConfirmation(Confirmation{At: now.Unix(), By: "architect"}, "somebody-else", now, 12)
	if other.SelfConfirmed {
		t.Error("a confirmation by somebody other than the last editor was marked self-confirmed")
	}
	// An unconfirmed record is not self-confirmed: nobody confirmed it at all.
	never := viewConfirmation(Confirmation{}, "architect", now, 12)
	if never.SelfConfirmed || never.Ever || !never.Stale {
		t.Errorf("an unconfirmed record views as %+v", never)
	}
	// With auth off there is no principal, so By is empty — and an empty By must not
	// match an empty UpdatedBy into a self-confirmation that names nobody.
	anon := viewConfirmation(Confirmation{At: now.Unix()}, "", now, 12)
	if anon.SelfConfirmed {
		t.Error("two empty names were read as the same person")
	}
	if self.HorizonMonths != 12 {
		t.Error("the view does not carry the horizon it applied")
	}
}

func TestValidateConfirmation(t *testing.T) {
	tests := []struct {
		name string
		req  confirmRequest
		want string
	}{
		{name: "empty is the commonest confirmation", req: confirmRequest{}},
		{name: "who and what", req: confirmRequest{With: "Head of Credit Risk", Note: "SLA renegotiated to 3 days"}},
		{name: "a with that is a distribution list", req: confirmRequest{With: strings.Repeat("x", 201)}, want: "with is longer"},
		{name: "a note that is a report", req: confirmRequest{Note: strings.Repeat("x", 501)}, want: "note is longer"},
		{name: "a multi-line note", req: confirmRequest{Note: "one\ntwo"}, want: "single lines"},
		{name: "a multi-line with", req: confirmRequest{With: "a\r\nb"}, want: "single lines"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := validateConfirmation(tt.req)
			if tt.want == "" {
				if len(findings) != 0 {
					t.Fatalf("refused: %v", findings)
				}
				return
			}
			if len(findings) == 0 {
				t.Fatal("accepted")
			}
			if !strings.Contains(strings.Join(findings, " | "), tt.want) {
				t.Errorf("findings %v do not mention %q", findings, tt.want)
			}
		})
	}
}

// The rule the whole mechanism rests on, through the API.

func TestCreatingARecordConfirmsIt(t *testing.T) {
	fx := newFixture(t)
	rec := fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	created := decode[Capability](t, rec)
	if created.Confirmation.At != fx.now.Unix() || created.Confirmation.By != "architect" {
		t.Errorf("confirmation = %+v; somebody just wrote the record, which is an assertion",
			created.Confirmation)
	}

	rec = fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "v", Name: "V"})
	if got := decode[ValueStream](t, rec).Confirmation; got.At != fx.now.Unix() {
		t.Errorf("value stream confirmation = %+v", got)
	}
}

// TestAClientCannotPostAConfirmationItDidNotMake: the date is the server's to write.
func TestAClientCannotPostAConfirmationItDidNotMake(t *testing.T) {
	fx := newFixture(t)
	forged := map[string]any{"key": "a", "name": "A",
		"confirmation": map[string]any{"at": 99, "by": "somebody-important", "with": "the board"}}
	rec := fx.do(t, "POST", "/api/v1/capabilities", forged)
	got := decode[Capability](t, rec).Confirmation
	if got.At != fx.now.Unix() || got.By != "architect" || got.With != "" {
		t.Errorf("confirmation = %+v, want the server's own", got)
	}
}

// TestNoEditRefreshesTheConfirmation is the rule the mechanism would be worthless
// without. A save that dated the record would let a typo fix in the summary assert that
// the owner, the scope and every SLA had been re-read.
func TestNoEditRefreshesTheConfirmation(t *testing.T) {
	fx := newFixture(t)
	created := decode[Capability](t, fx.do(t, "POST", "/api/v1/capabilities",
		Capability{Key: "a", Name: "A", Summary: "typo"}))
	firstConfirmation := created.Confirmation

	// A year passes, and somebody fixes the typo — and, for good measure, rewrites the
	// owner and the SLA, which are the very fields a confirmation is about.
	fx.now = fx.now.Add(400 * 24 * time.Hour)
	created.Summary = "fixed"
	created.Owner = Owner{Name: "Somebody New"}
	created.SLAs = []SLA{{Name: "Decision", Metric: "cycleTime", Threshold: "1 d", Scope: SLAInternal}}
	updated := decode[Capability](t, fx.do(t, "PUT", "/api/v1/capabilities/a", created))

	if updated.Confirmation != firstConfirmation {
		t.Fatalf("an edit moved the confirmation from %+v to %+v", firstConfirmation, updated.Confirmation)
	}
	if updated.UpdatedAt == updated.Confirmation.At {
		t.Error("the edit and the confirmation carry the same date, so one of them is not what it says")
	}

	// A client cannot smuggle one in through the update body either.
	forged := map[string]any{"name": "A", "confirmation": map[string]any{"at": fx.now.Unix(), "by": "forged"}}
	again := decode[Capability](t, fx.do(t, "PUT", "/api/v1/capabilities/a", forged))
	if again.Confirmation != firstConfirmation {
		t.Errorf("an update body set the confirmation to %+v", again.Confirmation)
	}
}

func TestNoEditRefreshesAValueStreamConfirmation(t *testing.T) {
	fx := newFixture(t)
	created := decode[ValueStream](t, fx.do(t, "POST", "/api/v1/value-streams",
		ValueStream{Key: "v", Name: "V"}))
	first := created.Confirmation
	fx.now = fx.now.Add(400 * 24 * time.Hour)
	created.Name = "V2"
	updated := decode[ValueStream](t, fx.do(t, "PUT", "/api/v1/value-streams/v", created))
	if updated.Confirmation != first {
		t.Errorf("an edit moved the confirmation from %+v to %+v", first, updated.Confirmation)
	}
}

func TestConfirmingARecord(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	fx.now = fx.now.Add(400 * 24 * time.Hour)

	rec := fx.do(t, "POST", "/api/v1/capabilities/a/confirmation", map[string]any{
		"with": "Head of Credit Risk", "note": "SLA renegotiated to 3 days",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", rec.Code, rec.Body)
	}
	view := decode[ConfirmationView](t, rec)
	if view.At != fx.now.Unix() || view.By != "architect" || view.With != "Head of Credit Risk" {
		t.Errorf("view = %+v", view)
	}
	if view.Stale || !view.Ever {
		t.Errorf("a confirmation made now views as %+v", view)
	}
	if view.HorizonMonths != DefaultHorizonMonths {
		t.Errorf("horizonMonths = %d", view.HorizonMonths)
	}

	// It is on the record, and it did not bump the revision: a confirmation asserts
	// nothing about the content, so a client holding a copy has not been made stale.
	stored := decode[Capability](t, fx.do(t, "GET", "/api/v1/capabilities/a", nil))
	if stored.Confirmation.Note != "SLA renegotiated to 3 days" {
		t.Errorf("stored confirmation = %+v", stored.Confirmation)
	}
	if stored.Revision != 1 {
		t.Errorf("revision = %d, want the confirmation to have left it alone", stored.Revision)
	}
}

func TestConfirmingReplacesTheNoteRatherThanAppending(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	fx.do(t, "POST", "/api/v1/capabilities/a/confirmation", map[string]any{"note": "first"})
	fx.do(t, "POST", "/api/v1/capabilities/a/confirmation", map[string]any{"note": "second"})
	got := decode[Capability](t, fx.do(t, "GET", "/api/v1/capabilities/a", nil)).Confirmation
	if got.Note != "second" {
		t.Errorf("note = %q, want only the last review's, not a log", got.Note)
	}
}

// Confirming with no body at all is the commonest case: "I read this and it is still
// true". It must not need a JSON object to say that.
func TestConfirmingWithNothingToSay(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	rec := fx.do(t, "POST", "/api/v1/capabilities/a/confirmation", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", rec.Code, rec.Body)
	}
	if view := decode[ConfirmationView](t, rec); view.With != "" || view.Note != "" {
		t.Errorf("view = %+v", view)
	}
}

func TestConfirmingAValueStream(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "v", Name: "V"})
	fx.now = fx.now.Add(400 * 24 * time.Hour)
	rec := fx.do(t, "POST", "/api/v1/value-streams/v/confirmation", map[string]any{"with": "SVP"})
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", rec.Code, rec.Body)
	}
	if got := decode[ConfirmationView](t, rec); got.With != "SVP" || got.At != fx.now.Unix() {
		t.Errorf("view = %+v", got)
	}
}

func TestConfirmationRefusals(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	cases := []struct {
		name, path string
		body       any
		want       int
	}{
		{"unknown capability", "/api/v1/capabilities/ghost/confirmation", map[string]any{}, http.StatusNotFound},
		{"unknown value stream", "/api/v1/value-streams/ghost/confirmation", map[string]any{}, http.StatusNotFound},
		{"a note that is a report", "/api/v1/capabilities/a/confirmation",
			map[string]any{"note": strings.Repeat("x", 600)}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := fx.do(t, "POST", tc.path, tc.body); rec.Code != tc.want {
				t.Errorf("%s = %d %s, want %d", tc.name, rec.Code, rec.Body, tc.want)
			}
		})
	}
}

// The review backlog, which is the twin of the automation backlog.

func TestStaleFilterIsTheReviewBacklog(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "fresh", Name: "Fresh"})
	fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "fresh-stream", Name: "Fresh stream"})
	fx.now = fx.now.Add(400 * 24 * time.Hour)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "also-fresh", Name: "Also fresh"})

	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"?stale=true", []string{"fresh"}},
		{"?stale=false", []string{"also-fresh"}},
		{"", []string{"also-fresh", "fresh"}},
	} {
		rec := fx.do(t, "GET", "/api/v1/capabilities"+tc.query, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list%s = %d %s", tc.query, rec.Code, rec.Body)
		}
		var keys []string
		for _, row := range decode[[]CapabilitySummary](t, rec) {
			keys = append(keys, row.Key)
		}
		if strings.Join(keys, ",") != strings.Join(tc.want, ",") {
			t.Errorf("list%s = %v, want %v", tc.query, keys, tc.want)
		}
	}

	rec := fx.do(t, "GET", "/api/v1/value-streams?stale=true", nil)
	rows := decode[[]ValueStreamSummary](t, rec)
	if len(rows) != 1 || rows[0].Key != "fresh-stream" || !rows[0].Stale {
		t.Errorf("stale value streams = %+v", rows)
	}
}

// Confirming clears the record from the backlog, which is the loop that makes the
// mechanism a worklist rather than a permanent complaint.
func TestConfirmingClearsTheBacklogRow(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	fx.now = fx.now.Add(400 * 24 * time.Hour)

	if rows := decode[[]CapabilitySummary](t, fx.do(t, "GET", "/api/v1/capabilities?stale=true", nil)); len(rows) != 1 {
		t.Fatalf("backlog = %+v, want the lapsed record", rows)
	}
	fx.do(t, "POST", "/api/v1/capabilities/a/confirmation", map[string]any{})
	if rows := decode[[]CapabilitySummary](t, fx.do(t, "GET", "/api/v1/capabilities?stale=true", nil)); len(rows) != 0 {
		t.Errorf("backlog after confirming = %+v", rows)
	}
}

// The gap report's ninth and tenth findings, through the API.

func TestGapReportOnAnUnconfirmedRecord(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A",
		Realizations: []Realization{{Kind: RealizationManual, Note: "clerk"}}})
	fx.do(t, "POST", "/api/v1/value-streams", ValueStream{Key: "v", Name: "V",
		Stages: []Stage{{Key: "s", Name: "S", Capabilities: []string{"a"}}}})

	// Freshly created records are confirmed, so nothing is reported yet.
	rep := decode[GapReport](t, fx.do(t, "GET", "/api/v1/business-architecture/gaps", nil))
	if rep.Counts[FindingUnconfirmed] != 0 || rep.Counts[FindingStreamUnconfirmed] != 0 {
		t.Fatalf("counts = %v on a map written today", rep.Counts)
	}
	if rep.HorizonMonths != DefaultHorizonMonths {
		t.Errorf("horizonMonths = %d; a reader seeing no findings is entitled to know the interval",
			rep.HorizonMonths)
	}

	fx.now = fx.now.Add(400 * 24 * time.Hour)
	rep = decode[GapReport](t, fx.do(t, "GET", "/api/v1/business-architecture/gaps", nil))
	if rep.Counts[FindingUnconfirmed] != 1 || rep.Counts[FindingStreamUnconfirmed] != 1 {
		t.Fatalf("counts = %v after the horizon passed", rep.Counts)
	}
	for _, f := range rep.Findings {
		if f.Kind != FindingUnconfirmed && f.Kind != FindingStreamUnconfirmed {
			continue
		}
		if !strings.Contains(f.Detail, "months ago") || !strings.Contains(f.Detail, "horizon") {
			t.Errorf("detail %q does not say how long ago or against what", f.Detail)
		}
	}
}

// The two states behind one finding kind read differently, because they are different
// work: never confirmed is a backlog to go through once, lapsed is a review that slipped.
func TestNeverConfirmedReadsDifferentlyFromLapsed(t *testing.T) {
	never := Gaps([]Capability{{Key: "a", Name: "A", State: StateActive}}, nil, Landscape{}, testNow, 12)
	lapsed := Gaps([]Capability{{Key: "a", Name: "A", State: StateActive,
		Confirmation: Confirmation{At: testNow.AddDate(-2, 0, 0).Unix(), By: "architect"}}},
		nil, Landscape{}, testNow, 12)

	neverDetail := findingsOf(never, FindingUnconfirmed)[0].Detail
	lapsedDetail := findingsOf(lapsed, FindingUnconfirmed)[0].Detail
	if !strings.Contains(neverDetail, "nobody has confirmed") {
		t.Errorf("never-confirmed detail = %q", neverDetail)
	}
	if !strings.Contains(lapsedDetail, "months ago") {
		t.Errorf("lapsed detail = %q", lapsedDetail)
	}
	if neverDetail == lapsedDetail {
		t.Error("the two states read identically, so the reader cannot tell which work is which")
	}
}

// A deprecated capability is on its way out and nobody is going to re-read it. The same
// exemption capability.unrealized already makes, for the same reason.
func TestADeprecatedCapabilityIsNotReportedAsUnconfirmed(t *testing.T) {
	rep := Gaps([]Capability{{Key: "fax", Name: "Fax intake", State: StateDeprecated}},
		nil, Landscape{}, testNow, 12)
	if has(rep, FindingUnconfirmed) {
		t.Errorf("a deprecated capability was reported: %+v", rep.Findings)
	}
}

// TestTheHorizonIsTheInstallationsToSet: an operator can widen it, and the answer says
// which interval it applied — so a silenced check is legible rather than hidden.
func TestTheHorizonIsTheInstallationsToSet(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	fx.now = fx.now.Add(200 * 24 * time.Hour) // past a quarter, inside a year

	fx.horizonMonths = 3
	rep := decode[GapReport](t, fx.do(t, "GET", "/api/v1/business-architecture/gaps", nil))
	if rep.Counts[FindingUnconfirmed] != 1 || rep.HorizonMonths != 3 {
		t.Fatalf("quarterly: counts %v horizon %d", rep.Counts, rep.HorizonMonths)
	}

	fx.horizonMonths = 1200
	rep = decode[GapReport](t, fx.do(t, "GET", "/api/v1/business-architecture/gaps", nil))
	if rep.Counts[FindingUnconfirmed] != 0 {
		t.Fatalf("a century-long horizon still reported: %v", rep.Counts)
	}
	if rep.HorizonMonths != 1200 {
		t.Errorf("horizonMonths = %d; silencing the check has to be visible in the answer "+
			"it silenced", rep.HorizonMonths)
	}
}

// An unreadable setting must not refuse the read. The horizon decides how loudly a
// record is reported, never whether the answer is correct.
func TestAnUnreadableHorizonFallsBackToTheDefault(t *testing.T) {
	fx := newFixture(t)
	fx.svc.horizon = func() (int, error) { return 0, errAssert }
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A"})
	rec := fx.do(t, "GET", "/api/v1/business-architecture/gaps", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("gaps = %d %s, want the read to survive an unreadable setting", rec.Code, rec.Body)
	}
	if got := decode[GapReport](t, rec).HorizonMonths; got != DefaultHorizonMonths {
		t.Errorf("horizonMonths = %d, want the default", got)
	}
	// And a service built without a resolver at all reads the default too.
	if got := (&Service{}).horizonMonths(); got != DefaultHorizonMonths {
		t.Errorf("a service with no horizon resolver reads %d", got)
	}
}

func TestCoverageCarriesTheConfirmation(t *testing.T) {
	fx := newFixture(t)
	fx.do(t, "POST", "/api/v1/capabilities", Capability{Key: "a", Name: "A",
		SLAs: []SLA{{Name: "Decision", Metric: "cycleTime", Threshold: "5 d", Scope: SLAInternal}}})
	fx.do(t, "POST", "/api/v1/capabilities/a/confirmation", map[string]any{"with": "Head of Credit Risk"})

	cov := decode[CoverageReport](t, fx.do(t, "GET", "/api/v1/capabilities/a/coverage", nil))
	if cov.Confirmation.With != "Head of Credit Risk" || cov.Confirmation.Stale {
		t.Fatalf("confirmation = %+v", cov.Confirmation)
	}
	if !cov.Confirmation.SelfConfirmed {
		t.Error("the same principal created and confirmed the record, which the read should show")
	}
	if cov.Confirmation.HorizonMonths == 0 {
		t.Error("the read does not say which horizon it applied")
	}
}
