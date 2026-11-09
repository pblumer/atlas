package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/sharepointserver"
)

func TestSharePointServerRegistryFromEnv(t *testing.T) {
	reg, names, err := sharePointServerRegistryFromEnv(func(string) string { return "" })
	if reg != nil || names != nil || err != nil {
		t.Errorf("unconfigured = %v %v %v", reg, names, err)
	}
	env := map[string]string{
		"ATLAS_SHAREPOINTSERVER_CONNECTORS": "farm",
		"ATLAS_SHAREPOINTSERVER_FARM_URL":   "http://sp01",
	}
	reg, names, err = sharePointServerRegistryFromEnv(func(k string) string { return env[k] })
	if err != nil || len(names) != 1 {
		t.Fatalf("names = %v, err = %v", names, err)
	}
	if _, ok := reg.Client("farm"); !ok {
		t.Error("farm is not registered")
	}
	env["ATLAS_SHAREPOINTSERVER_FARM_URL"] = "nonsense"
	if _, _, err := sharePointServerRegistryFromEnv(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "not usable") {
		t.Errorf("err = %v", err)
	}
}

func TestRunSharePointServerJobNeedsTheResolvedDetail(t *testing.T) {
	if _, err := RunSharePointServerJob(context.Background(), Job{}, sharepointserver.NewRegistry()); err == nil {
		t.Error("a job with no resolved detail must fail")
	}
	j := Job{Connector: &ConnectorPayload{Fields: map[string]any{"connector": 7}}}
	if _, err := RunSharePointServerJob(context.Background(), j, sharepointserver.NewRegistry()); err == nil {
		t.Error("an unreadable detail must fail")
	}
	j = Job{Connector: &ConnectorPayload{Fields: map[string]any{"connector": "nope", "operation": "get-site"}}}
	if _, err := RunSharePointServerJob(context.Background(), j, sharepointserver.NewRegistry()); err == nil {
		t.Error("an unknown Worker must fail")
	}
	// A value JSON cannot encode is the one way the first marshal fails.
	j = Job{Connector: &ConnectorPayload{Fields: map[string]any{"bad": make(chan int)}}}
	if _, err := RunSharePointServerJob(context.Background(), j, sharepointserver.NewRegistry()); err == nil {
		t.Error("an unencodable detail must fail")
	}
}
