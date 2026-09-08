package api

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// TestIncidentTypeNamesWhatParked pins the label all three operator surfaces read to
// decide what an incident is (ADR-0150): a job incident holds a service-task job whose
// retries ran out, one the execution budget raised holds a token that never got to
// run, and a job-less one is otherwise a timer whose FEEL schedule stopped resolving
// (ADR-0064/0111). Only the first and second are actually known; the third is the
// fallback, and it is wrong for the other job-less sources that carry no reason.
func TestIncidentTypeNamesWhatParked(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    model.IncidentValue
		want string
	}{
		{"parked job", model.IncidentValue{JobKey: 42}, "job"},
		{"job-less timer", model.IncidentValue{}, "timer"},
		{"execution budget", model.IncidentValue{Reason: model.IncidentOverBudget}, "budget"},
		{"a job outranks the reason", model.IncidentValue{JobKey: 42, Reason: model.IncidentOverBudget}, "job"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := incidentType(&tc.v); got != tc.want {
				t.Errorf("incidentType(%+v) = %q, want %q", tc.v, got, tc.want)
			}
		})
	}
}
