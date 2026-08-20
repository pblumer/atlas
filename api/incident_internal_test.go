package api

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// TestIncidentTypeNamesWhatParked pins the label all three operator surfaces read to
// decide what an incident is (ADR-0150): a job incident holds a service-task job whose
// retries ran out, a job-less one is a timer whose FEEL schedule stopped resolving
// (ADR-0064/0111). The distinction is the absence of a job key and nothing else.
func TestIncidentTypeNamesWhatParked(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    model.IncidentValue
		want string
	}{
		{"parked job", model.IncidentValue{JobKey: 42}, "job"},
		{"job-less timer", model.IncidentValue{}, "timer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := incidentType(&tc.v); got != tc.want {
				t.Errorf("incidentType(%+v) = %q, want %q", tc.v, got, tc.want)
			}
		})
	}
}
