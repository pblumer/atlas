package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/lookup"
)

// The AD Worker Type answers form lookups for the directories it holds by name, over
// the same dialer its jobs use — here the mock forest (ADR-0473, amended 2026-11-04).
func TestTheADWorkerServesLookupsForItsNamedDirectories(t *testing.T) {
	seed := seedFile(t, "dn: cn=Anna Muster,ou=users,dc=example,dc=com\n"+
		"objectClass: user\ncn: Anna Muster\nsAMAccountName: anna.muster\ndisplayName: Anna Muster\nuserAccountControl: 512\n")
	built, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_AD_MOCK":                     "1",
		"ATLAS_AD_MOCK_SEED":                seed,
		"ATLAS_AD_CONNECTORS":               "ad_verzeichnis",
		"ATLAS_AD_AD_VERZEICHNIS_URL":       "ldaps://dc.example.com:636",
		"ATLAS_AD_AD_VERZEICHNIS_BIND_DN":   "cn=svc,dc=example,dc=com",
		"ATLAS_AD_AD_VERZEICHNIS_PASSWORD":  "x",
		"ATLAS_AD_AD_VERZEICHNIS_START_TLS": "false",
		"ATLAS_WORKER_ID":                   "ad-worker-1",
	}), "ad")
	if err != nil {
		t.Fatal(err)
	}
	ask, ok := built.Queries[compiler.AdJobType]
	if !ok {
		t.Fatalf("queries = %v, want %s", built.Queries, compiler.AdJobType)
	}
	page, err := ask(context.Background(), "ad_verzeichnis", lookup.Query{
		Resource: "users", Search: "ann", SearchFields: []string{"displayName"},
		Properties: []string{"sAMAccountName", "displayName"}, ActiveOnly: true,
	})
	if err != nil || len(page.Items) != 1 || page.Items[0]["sAMAccountName"] != "anna.muster" {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
	_, err = ask(context.Background(), "elsewhere", lookup.Query{Resource: "users", Properties: []string{"sAMAccountName"}})
	var le *lookup.Error
	if !errors.As(err, &le) || le.Code != lookup.CodeUnconfigured {
		t.Fatalf("err = %v, want %s", err, lookup.CodeUnconfigured)
	}
}

// A worker that holds no named directory has nothing a form could name, and offers no
// lookup rather than one that always fails.
func TestAnADWorkerWithoutDirectoriesOffersNoLookup(t *testing.T) {
	built, err := BuiltinConnectors(envMap(map[string]string{"ATLAS_AD_MOCK": "1", "ATLAS_WORKER_ID": "w"}), "ad")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := built.Queries[compiler.AdJobType]; ok {
		t.Fatal("a worker without directories answers lookups")
	}
}
