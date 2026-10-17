package entra

import (
	"context"
	"testing"
)

// A single-object read names the properties it wants (ADR-0172, amended). Graph returns
// only a default set for a user or a group, and some a process needs — the licences a
// resource account has been given, its employeeId — are outside that set, so without a
// select they are simply absent from the answer and a process cannot tell "not set"
// from "not asked for".
func TestSingleObjectReadsCarryASelect(t *testing.T) {
	for _, tc := range []struct {
		job  Job
		path string
	}{
		{Job{Operation: "get-user", UserID: "cq_test@contoso.com", Select: "id, assignedLicenses,employeeId"},
			"/users/cq_test@contoso.com?$select=id%2C+assignedLicenses%2CemployeeId"},
		{Job{Operation: "get-group", GroupID: "g1", Select: "id,displayName"}, "/groups/g1?$select=id%2CdisplayName"},
		// No select is the default property set, as before.
		{Job{Operation: "get-user", UserID: "u1"}, "/users/u1"},
		{Job{Operation: "get-user", UserID: "u1", Select: "   "}, "/users/u1"},
	} {
		rc := &recordingClient{res: map[string]any{"id": "x"}}
		tc.job.Connector = "contoso"
		if _, err := Run(context.Background(), tc.job, regWith("contoso", rc)); err != nil {
			t.Errorf("%s %q: %v", tc.job.Operation, tc.job.Select, err)
			continue
		}
		if rc.path != tc.path {
			t.Errorf("%s with select %q: path = %q, want %q", tc.job.Operation, tc.job.Select, rc.path, tc.path)
		}
	}
}

// Every operation that takes a select is one that reads something whose properties
// can be projected; a write that takes one would silently drop it.
func TestOnlyReadsTakeASelect(t *testing.T) {
	for name, op := range Ops {
		if op.Selects && op.Method != "GET" {
			t.Errorf("Ops[%q] takes a select but is a %s", name, op.Method)
		}
	}
}
