package formlookup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/workerquery"
	"github.com/pblumer/atlas/connector/lookup"
)

func check(f *fakes, schema string, vars map[string]any) *SubmissionError {
	return f.service().CheckSubmission(context.Background(), []byte(schema), vars)
}

func TestAFormWithoutADirectoryFieldIsNotChecked(t *testing.T) {
	f := newFakes()
	if err := check(f, `{"type":"default","components":[{"type":"textfield","key":"user"}]}`, map[string]any{"user": map[string]any{"any": "thing"}}); err != nil {
		t.Fatal(err)
	}
	if len(f.asked) != 0 {
		t.Error("an ordinary form was looked up")
	}
}

// The submitted value is a business decision and is checked as one: its shape first,
// for nothing, and then — by default — against the directory once more, under the
// filters the form holds it to.
func TestASubmittedSelectionIsCheckedBeforeItCompletesTheTask(t *testing.T) {
	f := newFakes()
	f.page = lookup.Page{Items: []map[string]any{{"id": anna}}}
	if err := check(f, exampleForm, map[string]any{"user": anna, "abteilung": "IT"}); err != nil {
		t.Fatalf("a valid selection was refused: %v", err)
	}
	q := f.asked[0]
	if q.Value != anna || len(q.Filters) != 1 || q.Filters[0].Value != "IT" || !q.ActiveOnly {
		t.Errorf("verification query = %+v", q)
	}
}

func TestASubmissionIsRefusedForWhatTheFieldCannotHold(t *testing.T) {
	for name, tc := range map[string]struct {
		vars   map[string]any
		status int
		code   string
	}{
		"missing and required":  {map[string]any{"abteilung": "IT"}, 400, "invalid_value"},
		"empty and required":    {map[string]any{"user": ""}, 400, "invalid_value"},
		"a whole user object":   {map[string]any{"user": map[string]any{"id": anna, "displayName": "Anna"}}, 400, "invalid_value"},
		"a list":                {map[string]any{"user": []any{anna}}, 400, "invalid_value"},
		"not an object id":      {map[string]any{"user": "anna@example.com"}, 400, "invalid_value"},
		"a department off list": {map[string]any{"user": anna, "abteilung": "HR"}, 400, "invalid_value"},
	} {
		f := newFakes()
		err := check(f, exampleForm, tc.vars)
		if err == nil || err.Status != tc.status || err.Code != tc.code {
			t.Errorf("%s: %+v, want %d %s", name, err, tc.status, tc.code)
		}
		if len(f.asked) != 0 {
			t.Errorf("%s: a structurally invalid value was looked up", name)
		}
	}
}

func TestASelectionTheDirectoryNoLongerHoldsIsRefused(t *testing.T) {
	f := newFakes()
	f.page = lookup.Page{Items: []map[string]any{}}
	err := check(f, exampleForm, map[string]any{"user": anna, "abteilung": "IT"})
	if err == nil || err.Status != http.StatusUnprocessableEntity || err.Code != "value_not_found" {
		t.Fatalf("%+v", err)
	}
	if !strings.Contains(err.Message, "Person") {
		t.Errorf("message %q does not name the field", err.Message)
	}
}

// When the directory cannot be asked, nothing is completed — and the person is told it
// is worth trying again, not that the choice was wrong.
func TestASelectionThatCannotBeCheckedIsATransientRefusal(t *testing.T) {
	for _, e := range []error{workerquery.ErrUnavailable, workerquery.ErrTimeout, &lookup.Error{Code: lookup.CodeThrottled, RetryAfter: 5}} {
		f := newFakes()
		f.err = e
		err := check(f, exampleForm, map[string]any{"user": anna, "abteilung": "IT"})
		if err == nil || err.Status < 500 {
			t.Errorf("%v: %+v, want a 5xx", e, err)
		}
	}
}

func TestAFormatOnlyFieldIsNotLookedUp(t *testing.T) {
	f := newFakes()
	schema := strings.Replace(exampleForm, `"pageSize": 25`, `"pageSize": 25, "verify": "format"`, 1)
	if err := check(f, schema, map[string]any{"user": anna}); err != nil {
		t.Fatal(err)
	}
	if len(f.asked) != 0 {
		t.Error("a format-only field was looked up")
	}
}

func TestAnOptionalFieldMayBeLeftEmpty(t *testing.T) {
	f := newFakes()
	schema := strings.Replace(exampleForm, `"validate": {"required": true},`, ``, 1)
	for _, vars := range []map[string]any{{}, {"user": nil}, {"user": ""}} {
		if err := check(f, schema, vars); err != nil {
			t.Errorf("%v: %v", vars, err)
		}
	}
	if len(f.asked) != 0 {
		t.Error("an empty optional field was looked up")
	}
}

// form-js does not submit a field it hides or disables, and the server cannot evaluate
// form-js's condition — so it does not insist on a value it may never be sent.
func TestAHiddenOrDisabledFieldIsNotRequired(t *testing.T) {
	f := newFakes()
	for _, extra := range []string{`"conditional": {"hide": "=abteilung = null"},`, `"disabled": true,`} {
		schema := strings.Replace(exampleForm, `"validate": {"required": true},`, `"validate": {"required": true}, `+extra, 1)
		if err := check(f, schema, map[string]any{"abteilung": "IT"}); err != nil {
			t.Errorf("%s: %v", extra, err)
		}
	}
}

func TestASubmissionThroughAWorkerClosedToFormsIsRefused(t *testing.T) {
	f := newFakes()
	f.workers["ucc_entra"] = Worker{Name: "ucc_entra", WorkerType: "entra", Enabled: true}
	err := check(f, exampleForm, map[string]any{"user": anna, "abteilung": "IT"})
	if err == nil || err.Status != http.StatusForbidden {
		t.Fatalf("%+v", err)
	}
}

func TestASubmissionErrorReadsAsAnError(t *testing.T) {
	var err error = &SubmissionError{Status: 400, Code: "invalid_value", Message: "x"}
	var se *SubmissionError
	if !errors.As(err, &se) || err.Error() != "x" {
		t.Fatalf("%v", err)
	}
}

// A refused completion is written in the lookup's error shape, with the wait where one
// helps, so the Tasks app reads both the same way.
func TestARefusedCompletionIsWrittenLikeARefusedLookup(t *testing.T) {
	rec := httptest.NewRecorder()
	(&SubmissionError{Status: http.StatusServiceUnavailable, Code: "provider_throttled", Message: "wait", RetryAfter: 7}).Write(rec)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusServiceUnavailable || body["code"] != "provider_throttled" || body["error"] != "wait" ||
		rec.Header().Get("Retry-After") != "7" || body["retryAfter"] != float64(7) {
		t.Errorf("%d %v %v", rec.Code, rec.Header(), body)
	}
}

// A form that can no longer be read refuses its completion rather than letting an
// unchecked value through.
func TestAFormThatCannotBeReadCompletesNothing(t *testing.T) {
	f := newFakes()
	err := check(f, strings.Replace(exampleForm, `"version": 1`, `"version": 2`, 1), map[string]any{"user": anna, "abteilung": "IT"})
	if err == nil || err.Status != http.StatusUnprocessableEntity || err.Code != "invalid_configuration" {
		t.Fatalf("err = %+v", err)
	}
	if len(f.asked) != 0 {
		t.Error("an unreadable form was looked up")
	}
}

// When the Worker refuses the check itself, nothing completes, and the refusal is a
// server-side one the person can retry later — never a 4xx that reads as their fault.
func TestACheckTheWorkerRefusesIsNotThePersonsFault(t *testing.T) {
	f := newFakes()
	f.err = lookup.Fail(lookup.CodeInvalid)
	err := check(f, exampleForm, map[string]any{"user": anna, "abteilung": "IT"})
	if err == nil || err.Status != http.StatusServiceUnavailable {
		t.Fatalf("err = %+v, want 503", err)
	}
}
