package sharepoint_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/sharepoint"
	"github.com/pblumer/atlas/job"
)

// refusingClient refuses every operation under one code.
type refusingClient struct{ code string }

func (c refusingClient) Do(context.Context, sharepoint.Request) (any, error) {
	return nil, &sharepoint.Refusal{Code: c.code, Message: "sharepoint: set-site-quota: refused"}
}

// Run hands a refusal on as the BPMN error when the job's process catches its code, and
// as the failure it is otherwise — the path the example's fallback depends on.
func TestRunThrowsACaughtRefusal(t *testing.T) {
	reg := sharepoint.NewRegistry()
	reg.Register("sp", refusingClient{code: sharepoint.CodeQuotaRefused})
	j := sharepoint.Job{Connector: "sp", Operation: sharepoint.OpSetSiteQuota, Site: "contoso.sharepoint.com:/sites/x", QuotaGB: "5"}

	j.ErrorCodes = []string{sharepoint.CodeAdminNotPermitted, sharepoint.CodeQuotaRefused}
	_, err := sharepoint.Run(context.Background(), j, reg)
	var be *job.BPMNError
	if !errors.As(err, &be) || be.Code != sharepoint.CodeQuotaRefused {
		t.Fatalf("err = %v, want the BPMN error %s", err, sharepoint.CodeQuotaRefused)
	}

	j.ErrorCodes = nil
	_, err = sharepoint.Run(context.Background(), j, reg)
	if err == nil || errors.As(err, &be) {
		t.Fatalf("err = %v, want an ordinary failure when nothing catches the code", err)
	}
}

// The example process catches both codes by exactly the names this package throws, so
// a tenant without the permission reaches the task instead of an incident.
func TestTheSiteExampleCatchesEveryQuotaCode(t *testing.T) {
	f, err := os.Open("../../examples/m365-dienste/sharepoint-site.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cp, err := compiler.Parse(1, 1, f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, code := range sharepoint.Codes {
		if !slices.Contains(cp.CaughtErrorCodes(), code) {
			t.Errorf("sharepoint-site.bpmn does not catch %s; caught: %v", code, cp.CaughtErrorCodes())
		}
	}
}
