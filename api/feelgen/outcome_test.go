package feelgen

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/agent"
)

// What a request came to is the data the prompt is improved from: how many rounds it
// took, what each round failed on, whether the model kept to the contract, and which
// foreign functions it reached for. These tests pin that every way a request can end
// reports itself, and reports what happened rather than what was hoped.

// observed builds a service that records its outcomes and runs on a fixed clock.
func observed(workers []Worker, m agent.Model) (*Service, *[]Outcome) {
	svc := service(workers, m)
	var got []Outcome
	svc.Observe = func(o Outcome) { got = append(got, o) }
	t0 := time.Unix(1_790_000_000, 0)
	calls := 0
	svc.now = func() time.Time {
		calls++
		return t0.Add(time.Duration(calls-1) * 7 * time.Second)
	}
	return svc, &got
}

func onlyOutcome(t *testing.T, got []Outcome) Outcome {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("%d outcomes observed, want exactly one", len(got))
	}
	return got[0]
}

func faults(o Outcome) []string {
	var out []string
	for _, a := range o.Attempts {
		out = append(out, a.Fault)
	}
	return out
}

func TestOutcomeOfACorrectedAnswer(t *testing.T) {
	m := &scripted{answers: []string{
		`{"expression":"trim(name)","variables":{"name":" a "},"expected":"a"}`,
		"```feel\nreplace(name, \"^\\\\s+|\\\\s+$\", \"\")\n```",
	}}
	req := ask("trimmen")
	req.Variables = []byte(`{"name":" a "}`)
	svc, got := observed(one, m)
	if _, _, err := generate(t, svc, req); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	o := onlyOutcome(t, *got)
	if o.Result != ResultSettled || o.Worker != "openrouter" || o.Model != one[0].Model {
		t.Errorf("outcome = %+v", o)
	}
	if strings.Join(faults(o), ",") != FaultCalls+","+FaultNone {
		t.Errorf("faults = %v, want the refused call and then a clean round", faults(o))
	}
	first, second := o.Attempts[0], o.Attempts[1]
	if first.Format != FormatContract || second.Format != FormatCodeBlock {
		t.Errorf("formats = %q, %q", first.Format, second.Format)
	}
	// The refused callee is named: which foreign functions models reach for is the
	// first thing the prompt's list of unavailable names should be held against.
	if !slices.Equal(first.Calls, []string{"trim"}) || !strings.Contains(first.Error, "replace") {
		t.Errorf("first attempt = %+v", first)
	}
	if second.Error != "" || second.Calls != nil {
		t.Errorf("a clean round reports a fault: %+v", second)
	}
	// One read of the clock at the start and one at the end.
	if o.Duration != 7*time.Second {
		t.Errorf("duration = %v", o.Duration)
	}
}

func TestOutcomeOfEveryEnding(t *testing.T) {
	for _, tc := range []struct {
		name    string
		model   agent.Model
		result  string
		faults  []string
		formats []string
	}{
		{"settled at once", &scripted{answers: []string{good}}, ResultSettled,
			[]string{FaultNone}, []string{FormatContract}},
		{"a question", &scripted{answers: []string{"Welche Variable hält den Betrag?"}}, ResultQuestion,
			[]string{FaultNone}, []string{FormatProse}},
		{"never settled", &scripted{answers: []string{`{"expression":"a + b","variables":{"a":1},"expected":1}`}}, ResultUnsettled,
			[]string{FaultMissing, FaultMissing, FaultMissing}, []string{FormatContract, FormatContract, FormatContract}},
		{"disagrees, then compiles not", &scripted{answers: []string{
			`{"expression":"1 + 1","variables":{},"expected":3}`,
			`{"expression":"if 1 then","variables":{},"expected":2}`,
			`{"expression":"","explanation":""}`,
		}}, ResultUnsettled,
			[]string{FaultMismatch, FaultCompile, FaultEmpty}, []string{FormatContract, FormatContract, FormatContract}},
		{"correction cut short", &scripted{
			answers: []string{`{"expression":"trim(x)","variables":{"x":"a"},"expected":"a"}`},
			errs:    []error{nil, errors.New("model endpoint returned 429")},
		}, ResultCutShort, []string{FaultCalls, FaultUnusable}, []string{FormatContract, FormatUnusable}},
		{"no answer at all", &scripted{errs: []error{errors.New("model endpoint returned 401")}}, ResultUnanswered,
			[]string{FaultUnusable}, []string{FormatUnusable}},
		{"an empty answer", &scripted{answers: []string{"  "}}, ResultUnanswered,
			[]string{FaultUnusable}, []string{FormatUnusable}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, got := observed(one, tc.model)
			_, _, _ = generate(t, svc, ask("x"))
			o := onlyOutcome(t, *got)
			if o.Result != tc.result {
				t.Errorf("result = %q, want %q", o.Result, tc.result)
			}
			if !slices.Equal(faults(o), tc.faults) {
				t.Errorf("faults = %v, want %v", faults(o), tc.faults)
			}
			var formats []string
			for _, a := range o.Attempts {
				formats = append(formats, a.Format)
			}
			if !slices.Equal(formats, tc.formats) {
				t.Errorf("formats = %v, want %v", formats, tc.formats)
			}
		})
	}
}

// TestNoOutcomeWithoutAModel: a request refused before any model was asked — no
// conversation, no Worker, a Worker that cannot be dialled — says nothing about the
// prompt, and is not counted as if it did. A dial failure is the operator's to see in
// the response, not a data point about how well a model writes FEEL.
func TestNoOutcomeWithoutAModel(t *testing.T) {
	for name, svc := range map[string]*Service{
		"no Worker":   service(nil, &scripted{answers: []string{good}}),
		"cannot dial": service(one, nil),
	} {
		var got []Outcome
		svc.Observe = func(o Outcome) { got = append(got, o) }
		_, _, _ = generate(t, svc, ask("x"))
		_, _, _ = generate(t, svc, Request{})
		if len(got) != 0 {
			t.Errorf("%s: %d outcomes observed", name, len(got))
		}
	}
}

// TestTheLabelsAreClosed: the result, fault and format values are what the server's
// metrics are labelled by, so each list must be complete — a value the service can
// produce and the list lacks would be a series the server never pre-resolved.
func TestTheLabelsAreClosed(t *testing.T) {
	for _, tc := range []struct {
		list  []string
		value string
	}{
		{Results, ResultSettled}, {Results, ResultQuestion}, {Results, ResultUnsettled},
		{Results, ResultCutShort}, {Results, ResultUnanswered},
		{Faults, FaultNone}, {Faults, FaultUnusable}, {Faults, FaultEmpty}, {Faults, FaultCompile},
		{Faults, FaultCalls}, {Faults, FaultEvaluate}, {Faults, FaultMissing}, {Faults, FaultMismatch},
		{Formats, FormatContract}, {Formats, FormatCodeBlock}, {Formats, FormatProse}, {Formats, FormatUnusable},
	} {
		if !slices.Contains(tc.list, tc.value) {
			t.Errorf("%q is produced but not listed", tc.value)
		}
	}
	if len(Results) != 5 || len(Faults) != 8 || len(Formats) != 4 {
		t.Errorf("lists = %d/%d/%d, a value was added without a test", len(Results), len(Faults), len(Formats))
	}
}

// TestObserveIsOptional: a service nobody measures works exactly as before.
func TestObserveIsOptional(t *testing.T) {
	svc := service(one, &scripted{answers: []string{good}})
	if _, _, err := svc.Generate(httptest.NewRequest(http.MethodPost, "/", nil), ask("x")); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}
