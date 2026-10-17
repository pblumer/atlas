package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// An Exchange worker is configured entirely from its environment, one organization per
// name — Exchange Online and Exchange Server side by side, which is how a hybrid
// organization is served.
func TestBuiltinConnectorsRegistersExchange(t *testing.T) {
	got, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_EXCHANGE_CONNECTORS":          "contoso,intern",
		"ATLAS_EXCHANGE_CONTOSO_CREDENTIALS": `{"organization":"contoso.onmicrosoft.com","clientId":"app-1","clientSecret":"s3cr3t"}`,
		"ATLAS_EXCHANGE_INTERN_CREDENTIALS":  `{"target":"onprem"}`,
		"ATLAS_EXCHANGE_INTERN_URL":          "http://exch01.contoso.local/PowerShell/",
		"ATLAS_EXCHANGE_POWERSHELL":          "powershell.exe",
	}), "exchange")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := got.Handlers[compiler.ExchangeJobType]; !ok {
		t.Errorf("no handler for %s; have %v", compiler.ExchangeJobType, got.Handlers)
	}
	if strings.Join(got.Names, ",") != "contoso,intern" {
		t.Errorf("names = %v", got.Names)
	}
}

// With no organization the worker parks, as Entra's does: the kind is supervised by
// default, and failing here would be a restart loop on every server that has not
// configured Exchange.
func TestAnExchangeWorkerWithNoOrganizationParks(t *testing.T) {
	built, err := BuiltinConnectors(envMap(nil), "exchange")
	if err != nil {
		t.Fatalf("an unconfigured exchange worker must not fail at startup: %v", err)
	}
	if len(built.Handlers) != 0 || len(built.Unconfigured) != 1 || built.Unconfigured[0] != "exchange" {
		t.Errorf("handlers %v unconfigured %v", built.Handlers, built.Unconfigured)
	}
}

// A *named* organization whose bundle does not build is refused at startup, quoting the
// variable to fix and not the secret it holds.
func TestBuiltinConnectorsRefusesAMisconfiguredExchangeWorker(t *testing.T) {
	_, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_EXCHANGE_CONNECTORS":          "contoso",
		"ATLAS_EXCHANGE_CONTOSO_CREDENTIALS": `{"organization":"contoso.onmicrosoft.com","clientSecret":"TOPSECRET"}`,
	}), "exchange")
	if err == nil {
		t.Fatal("a bundle without clientId was accepted")
	}
	if !strings.Contains(err.Error(), "ATLAS_EXCHANGE_CONTOSO_CREDENTIALS") || !strings.Contains(err.Error(), "clientId") {
		t.Errorf("err = %v", err)
	}
	if strings.Contains(err.Error(), "TOPSECRET") {
		t.Errorf("the error quotes the secret: %v", err)
	}
}

func TestRunExchangeJobNeedsAResolvedDetail(t *testing.T) {
	if _, err := RunExchangeJob(context.Background(), Job{}, nil); err == nil || !strings.HasPrefix(err.Error(), "exchange:") {
		t.Errorf("err = %v", err)
	}
}
