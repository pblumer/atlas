package calendarbooking

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/calendar"
	"github.com/pblumer/atlas/connector/calendar/catalog"
	"github.com/pblumer/atlas/workertype"
	"github.com/pblumer/atlas/workertype/evidence"
)

// The drafted contracts, kept where the evidence tests bind unpublished contracts.
// Publishing moves each file unchanged to workertype/contracts/.
var draftContract = map[string]string{
	NameFind:    "../../testdata/contracts/calendar-slot-find.json",
	NameReserve: "../../testdata/contracts/calendar-booking-reserve.json",
	NameConfirm: "../../testdata/contracts/calendar-booking-confirm.json",
	NameCancel:  "../../testdata/contracts/calendar-booking-cancel.json",
	NameGet:     "../../testdata/contracts/calendar-booking-get.json",
}

func loadContract(t *testing.T, name string) workertype.TechnicalContract {
	t.Helper()
	raw, err := os.ReadFile(draftContract[name])
	if err != nil {
		t.Fatal(err)
	}
	c, err := workertype.ParseTechnicalContractJSON(raw)
	if err != nil {
		t.Fatalf("the drafted contract %s does not parse: %v", name, err)
	}
	if _, err := workertype.CompileTechnicalContract(c); err != nil {
		t.Fatalf("the drafted contract %s's schemas do not compile: %v", name, err)
	}
	return c
}

func outcomes(t *testing.T, name string) map[string]evidence.Result {
	t.Helper()
	results, err := Suite(name).Run(context.Background(), loadContract(t, name))
	if err != nil {
		t.Fatalf("%s: Run: %v", name, err)
	}
	out := map[string]evidence.Result{}
	for _, r := range results {
		if _, dup := out[r.Case]; dup {
			t.Fatalf("%s: case %q reported twice", name, r.Case)
		}
		out[r.Case] = r
	}
	if len(out) != len(casesOf[name]) {
		t.Fatalf("%s: %d results for %d cases", name, len(out), len(casesOf[name]))
	}
	return out
}

// TestTheOperationsMeetTheirContractsOnEveryBackend pins every case's outcome: all
// pass, except the one no fake can show.
func TestTheOperationsMeetTheirContractsOnEveryBackend(t *testing.T) {
	notExercised := map[string]bool{caseReserveVisible: true}
	for name := range contractOf {
		for c, r := range outcomes(t, name) {
			want := evidence.OutcomePassed
			if notExercised[c] {
				want = evidence.OutcomeNotExercised
			}
			if r.Outcome != want {
				t.Errorf("%s: %q = %s (%s), want %s", name, c, r.Outcome, r.Detail, want)
			}
		}
	}
}

func TestEverySuiteRefusesAnotherContract(t *testing.T) {
	other := loadContract(t, NameGet)
	for name := range contractOf {
		if name == NameGet {
			continue
		}
		if _, err := Suite(name).Run(context.Background(), other); err == nil {
			t.Errorf("%s ran against the get contract", name)
		}
	}
}

func TestTheContractsClaimTheCatalogsIdentities(t *testing.T) {
	for name, id := range contractOf {
		c := loadContract(t, name)
		if c.Metadata.ID != id || c.Metadata.Version != contractVersion || c.Metadata.Profile != "free-busy-calendar" {
			t.Errorf("%s: contract is %s@%s+%s", name, c.Metadata.ID, c.Metadata.Version, c.Metadata.Profile)
		}
	}
}

// misbehave runs the reservation suite with the operation replaced, and answers the
// outcome of each case.
func misbehave(t *testing.T, name string, op func(context.Context, calendar.Job, *calendar.Registry, time.Time) (map[string]any, error)) map[string]evidence.Result {
	t.Helper()
	runOperation = op
	t.Cleanup(func() { runOperation = calendar.RunAt })
	return outcomes(t, name)
}

func TestAReservationWithoutTheConflictRuleFails(t *testing.T) {
	// It creates the placeholder and answers it, never reading the slot.
	got := misbehave(t, NameReserve, func(ctx context.Context, j calendar.Job, reg *calendar.Registry, now time.Time) (map[string]any, error) {
		b, _ := reg.Client(j.Connector)
		start, _ := time.Parse(time.RFC3339, j.Start)
		end, _ := time.Parse(time.RFC3339, j.End)
		ev, err := b.Create(ctx, j.Calendar, calendar.NewEvent{Key: j.CreateKey, Subject: j.Subject, Start: start, End: end})
		if err != nil {
			return nil, err
		}
		return map[string]any{j.ResultVariable: map[string]any{"bookingId": ev.ID, "calendar": j.Calendar, "start": j.Start, "end": j.End}}, nil
	})
	for _, c := range []string{caseReserveTaken, caseReserveSequential, caseReserveLockstep, caseReserveUnknown, caseReserveReadOnly} {
		if got[c].Outcome != evidence.OutcomeFailed {
			t.Errorf("%q = %s, want failed", c, got[c].Outcome)
		}
	}
	if got[caseReserveFree].Outcome != evidence.OutcomePassed || got[caseReserveRepeat].Outcome != evidence.OutcomePassed {
		t.Errorf("a free slot: %+v / %+v", got[caseReserveFree], got[caseReserveRepeat])
	}
}

func TestAReservationThatIgnoresItsKeyFailsTheRepeat(t *testing.T) {
	got := misbehave(t, NameReserve, func(ctx context.Context, j calendar.Job, reg *calendar.Registry, now time.Time) (map[string]any, error) {
		j.CreateKey += "-" + time.Now().Format(time.RFC3339Nano) // a new key on every delivery
		return calendar.RunAt(ctx, j, reg, now)
	})
	if r := got[caseReserveRepeat]; r.Outcome != evidence.OutcomeFailed {
		t.Fatalf("repeat = %+v, want failed", r)
	}
}

func TestATieBreakThatKeepsOneFailsTheLockstepCase(t *testing.T) {
	// The tempting variant: the reservation whose key sorts first keeps the slot even
	// though it saw the other. When only one read sees the other, that keeps both; the
	// lockstep case, where each sees the other, already shows it keeps one too many.
	got := misbehave(t, NameReserve, func(ctx context.Context, j calendar.Job, reg *calendar.Registry, now time.Time) (map[string]any, error) {
		res, err := calendar.RunAt(ctx, j, reg, now)
		if err == nil || j.CreateKey != "a" {
			return res, err
		}
		b, _ := reg.Client(j.Connector)
		start, _ := time.Parse(time.RFC3339, j.Start)
		end, _ := time.Parse(time.RFC3339, j.End)
		ev, cerr := b.Create(ctx, j.Calendar, calendar.NewEvent{Key: j.CreateKey, Subject: j.Subject, Start: start, End: end})
		if cerr != nil {
			return nil, cerr
		}
		return map[string]any{j.ResultVariable: map[string]any{"bookingId": ev.ID, "calendar": j.Calendar, "start": j.Start, "end": j.End}}, nil
	})
	if r := got[caseReserveLockstep]; r.Outcome != evidence.OutcomeFailed || !strings.Contains(r.Detail, "keeps 1") {
		t.Fatalf("lockstep = %+v, want failed", r)
	}
}

func TestAPlainFailureIsNotACode(t *testing.T) {
	got := misbehave(t, NameCancel, func(ctx context.Context, j calendar.Job, reg *calendar.Registry, now time.Time) (map[string]any, error) {
		j.ErrorCodes = nil // nothing caught: every refusal stays a plain failure
		return calendar.RunAt(ctx, j, reg, now)
	})
	if r := got[caseCancelUnknown]; r.Outcome != evidence.OutcomeFailed {
		t.Fatalf("unknown calendar = %+v, want failed", r)
	}
	if r := got[caseCancelRemoves]; r.Outcome != evidence.OutcomePassed {
		t.Fatalf("removes = %+v, want passed", r)
	}
}

// TestTheCatalogTakesExactlyTheContractsInput holds the operation table to the
// contracts: the keys a model's input object may carry are the contract input's
// properties, and the ones it must carry are the contract's required ones.
func TestTheCatalogTakesExactlyTheContractsInput(t *testing.T) {
	for name, opName := range operationOf {
		raw, err := os.ReadFile(draftContract[name])
		if err != nil {
			t.Fatal(err)
		}
		var c struct {
			Spec struct {
				InputSchema struct {
					Properties map[string]any `json:"properties"`
					Required   []string       `json:"required"`
				} `json:"inputSchema"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		op, _ := catalog.Lookup(opName)
		props := make([]string, 0, len(c.Spec.InputSchema.Properties))
		for k := range c.Spec.InputSchema.Properties {
			props = append(props, k)
		}
		sort.Strings(props)
		inputs := append([]string(nil), op.Inputs...)
		sort.Strings(inputs)
		required := append([]string(nil), op.Required...)
		sort.Strings(required)
		want := append([]string(nil), c.Spec.InputSchema.Required...)
		sort.Strings(want)
		if !reflect.DeepEqual(inputs, props) {
			t.Errorf("%s: the catalog takes %v, the contract %v", opName, inputs, props)
		}
		if !reflect.DeepEqual(required, want) {
			t.Errorf("%s: the catalog requires %v, the contract %v", opName, required, want)
		}
	}
}

// TestAnOperationThatClaimsSuccessForEverythingFailsEveryRefusal: a reservation that
// answers success without touching the calendar passes no case that asks for a refusal
// or for a booking to exist.
func TestAnOperationThatClaimsSuccessForEverythingFailsEveryRefusal(t *testing.T) {
	got := misbehave(t, NameReserve, func(_ context.Context, j calendar.Job, _ *calendar.Registry, _ time.Time) (map[string]any, error) {
		return map[string]any{j.ResultVariable: map[string]any{"bookingId": "x", "calendar": j.Calendar, "start": j.Start, "end": j.End}}, nil
	})
	for _, c := range []string{caseReserveFree, caseReserveTaken, caseReserveSequential, caseReserveLockstep, caseReserveUnknown, caseReserveReadOnly} {
		if got[c].Outcome != evidence.OutcomeFailed {
			t.Errorf("%q = %s, want failed", c, got[c].Outcome)
		}
	}
	if !strings.Contains(got[caseReserveLockstep].Detail, "success") {
		t.Errorf("lockstep detail %q does not say the reservation claimed success", got[caseReserveLockstep].Detail)
	}
}

// TestASuiteInputTheContractRefusesStopsTheSuite: a case whose own input the contract
// refuses is a defect of the suite, reported as an error, never as an outcome.
func TestASuiteInputTheContractRefusesStopsTheSuite(t *testing.T) {
	s := &session{ctx: context.Background(), operation: "get-booking"}
	compiled, err := workertype.CompileTechnicalContract(loadContract(t, NameGet))
	if err != nil {
		t.Fatal(err)
	}
	s.contract = compiled
	w := buildGraphWorld(map[string]calendar.MockCalendarSpec{"werkstatt": {}})
	defer w.close()
	_, err = s.operate(w, map[string]any{"calendar": "werkstatt"}, "")
	if !isSuiteDefect(err) || suiteErr(err) == nil {
		t.Fatalf("err = %v, want a suite defect", err)
	}
	if why, e := expectCode(err, "CALENDAR_NOT_FOUND", nil, "", 0); why != "" || e == nil {
		t.Fatalf("expectCode passed a suite defect on as %q, %v", why, e)
	}
	if describeOrSuccess(nil) != "success" {
		t.Fatal("no error is not described as success")
	}
}

// TestEveryCaseFailsAnOperationThatOnlyFailsOrAnswersNonsense runs every suite with
// two broken operations: one that fails every job plainly, one that answers an object
// no contract allows without touching the calendar. No case may pass either, except
// those that ask only for a refusal, which the failing operation gives — though never
// under the contract's code.
func TestEveryCaseFailsAnOperationThatOnlyFailsOrAnswersNonsense(t *testing.T) {
	broken := map[string]func(context.Context, calendar.Job, *calendar.Registry, time.Time) (map[string]any, error){
		"fails": func(context.Context, calendar.Job, *calendar.Registry, time.Time) (map[string]any, error) {
			return nil, errors.New("the provider is down")
		},
		"nonsense": func(_ context.Context, j calendar.Job, _ *calendar.Registry, _ time.Time) (map[string]any, error) {
			if j.ResultVariable == "" {
				return nil, nil
			}
			return map[string]any{j.ResultVariable: map[string]any{"nonsense": true}}, nil
		},
	}
	// A cancel answers nothing, so "nonsense" is a correct cancel of a booking gone.
	vacuous := map[string]bool{caseCancelGone: true}
	for how, op := range broken {
		for name := range contractOf {
			got := misbehave(t, name, op)
			for c, r := range got {
				if r.Outcome == evidence.OutcomeNotExercised || (how == "nonsense" && vacuous[c]) {
					continue
				}
				if r.Outcome != evidence.OutcomeFailed {
					t.Errorf("%s / %s: %q = %s, want failed", how, name, c, r.Outcome)
				}
			}
		}
	}
}
