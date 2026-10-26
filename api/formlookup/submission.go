package formlookup

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/connector/lookup"
)

// A submission is the business decision, and the one thing about a lookup that becomes
// durable. So it is not trusted because the form offered it: the value has to have the
// shape of one the field can hold, and — unless the field says otherwise — the directory
// is asked once more whether it holds that entry under the field's filters.
//
// It is a check before the completion, not a transaction with it. The directory can
// change between this lookup and the durable completion, and again before the next
// service task reads the person; Atlas does not pretend otherwise, and a task that reads
// a person by id already has a path for one who stopped existing (ENTRA_USER_NOT_FOUND).

// SubmissionError is a completion refused for a directory field's value.
type SubmissionError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter int
}

func (e *SubmissionError) Error() string { return e.Message }

// Write answers the refused completion in the lookup's error shape.
func (e *SubmissionError) Write(w http.ResponseWriter) {
	(&apiError{status: e.Status, code: e.Code, msg: e.Message, retry: e.RetryAfter}).write(w)
}

func submissionRefusal(format string, a ...any) *apiError {
	return &apiError{status: http.StatusBadRequest, code: "invalid_value", msg: fmt.Sprintf(format, a...)}
}

func (e *apiError) submission() *SubmissionError {
	return &SubmissionError{Status: e.status, Code: e.code, Message: e.msg, RetryAfter: e.retry}
}

// CheckSubmission checks every directory field of schema against the variables a
// completion submits, in the order the form shows them, and returns the first refusal.
// A form without a directory field is not checked at all, so every form that existed
// before them completes exactly as it did.
func (s *Service) CheckSubmission(ctx context.Context, schema []byte, vars map[string]any) *SubmissionError {
	form, err := Parse(schema)
	if err != nil {
		return &SubmissionError{Status: http.StatusUnprocessableEntity, Code: "invalid_configuration",
			Message: "the task's form cannot be checked: " + err.Error()}
	}
	for _, f := range form.Fields {
		if f.Disabled {
			continue
		}
		name := fieldName(form, f.Key)
		raw, present := vars[f.Key]
		if !present || raw == nil || raw == "" {
			if f.Required && !f.Conditional {
				return submissionRefusal("%s is required: choose one entry", name).submission()
			}
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return submissionRefusal("%s takes the identifier of one entry, not a %T", name, raw).submission()
		}
		if !f.Provider.ValidValue(value) {
			return submissionRefusal("%s does not hold %s", name, f.Provider.ValueFormat).submission()
		}
		filters, e := filtersOf(form, f, func(prop string) string {
			for _, fl := range f.Source.Filters {
				if fl.Property == prop && fl.Field != "" {
					if v, ok := vars[fl.Field]; ok && v != nil {
						return strings.TrimSpace(fmt.Sprint(v))
					}
				}
			}
			return ""
		}, submissionRefusal)
		if e != nil {
			return e.submission()
		}
		if f.Verify() == VerifyFormat {
			continue
		}
		if e := s.usable(f); e != nil {
			return e.submission()
		}
		q := lookup.Query{Resource: f.Provider.Resource, Value: value, Filters: filters,
			ActiveOnly: f.ActiveOnly(), Properties: f.Properties()}
		page, err := s.cfg.Ask(ctx, f.Provider.JobType, f.Source.Worker, q, s.cfg.Timeout)
		if err != nil {
			e := failure(err, "the choice in "+name+" could not be checked, so nothing was completed")
			if e.status < http.StatusInternalServerError {
				e.status = http.StatusServiceUnavailable
			}
			return e.submission()
		}
		if len(items(f, page)) == 0 {
			return &SubmissionError{Status: http.StatusUnprocessableEntity, Code: "value_not_found",
				Message: fmt.Sprintf("the entry chosen in %s is no longer in the directory, or no longer matches the field's filters; choose again", name)}
		}
	}
	return nil
}
