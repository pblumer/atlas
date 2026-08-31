package api

import (
	"strings"
	"testing"
)

func TestConfiguredWorkerProjectionMakesUnknownLegacyKindExplicit(t *testing.T) {
	worker := configuredWorkerFromRecord(connector{
		ID:             "legacy-1",
		Name:           "legacy",
		Kind:           "vendor-old",
		Endpoint:       "https://legacy.example.test",
		CredentialsRef: "legacy-token",
		Enabled:        true,
	})
	if worker.WorkerTypeID != "" || worker.WorkerTypeVersion != "" {
		t.Fatalf("unknown connector kind received an invented Worker Type identity: %+v", worker)
	}
	if !strings.Contains(worker.CompatibilityError, "vendor-old") {
		t.Fatalf("unknown connector kind has no explicit compatibility error: %+v", worker)
	}
	if worker.Config == nil || worker.Config.Endpoint != "https://legacy.example.test" || worker.CredentialsRef != "legacy-token" {
		t.Fatalf("legacy connector configuration was not preserved in the projection: %+v", worker)
	}
}

// A caller below viewer may know a Worker exists and nothing about how it is
// configured (ADR-0205). The projection must carry that boundary from the role check
// rather than blanking fields, which would report an unconfigured Worker.
func TestConfiguredWorkerProjectionPreservesCatalogAccessBoundary(t *testing.T) {
	worker := configuredWorkerFrom(connectorListing{
		record: connector{
			ID: "catalog-1", Name: "mail-prod", Kind: connectorKindMail,
			Endpoint: "smtp.example.test:587", CredentialsRef: "smtp-token",
			Provider: "smtp", Sender: "bot@example.test", Enabled: true,
			CreatedAt: 1700000000, OwnerID: "someone-else", Visibility: VisibilityPrivate,
		},
		problem:     "no credential",
		catalogOnly: true,
	})
	if worker.WorkerTypeID != "atlas.mail" || worker.WorkerTypeVersion != initialBuiltInWorkerTypeVersion {
		t.Fatalf("catalog entry has wrong Worker Type identity: %+v", worker)
	}
	if worker.Name != "mail-prod" || !worker.Enabled || worker.Problem != "no credential" {
		t.Fatalf("catalog entry lost the facts a modeller may see: %+v", worker)
	}
	if worker.Config != nil || worker.CredentialsRef != "" {
		t.Fatalf("catalog-only projection exposed configuration: %+v", worker)
	}
	if worker.OwnerID != "" || worker.Visibility != "" || worker.CreatedAt != 0 {
		t.Fatalf("catalog-only projection exposed ownership: %+v", worker)
	}
}

func TestConfiguredWorkerProjectionCarriesRuntimeAndSharingFacts(t *testing.T) {
	uses := []connectorUse{{ProcessID: "order", Name: "Order", Version: 3}}
	worker := configuredWorkerFrom(connectorListing{
		record: connector{
			ID: "temis-1", Name: "temis-prod", Kind: connectorKindTemis,
			Endpoint: "https://temis.example.test", Enabled: true,
			CreatedAt: 1700000000, UpdatedAt: 1700000001,
			OwnerID: "alice", Visibility: VisibilityPrivate,
			Members: []projectMember{{Role: ScopeRoleEditor}},
		},
		role:    ScopeRoleOwner,
		problem: "endpoint refused the connection",
		usedBy:  uses,
	})
	if worker.Role != ScopeRoleOwner || worker.Problem != "endpoint refused the connection" {
		t.Fatalf("projection dropped what the runtime and the role check decided: %+v", worker)
	}
	if len(worker.UsedBy) != 1 || worker.UsedBy[0].ProcessID != "order" {
		t.Fatalf("projection dropped the blast radius of a delete: %+v", worker)
	}
	if worker.CreatedAt != 1700000000 || worker.UpdatedAt != 1700000001 {
		t.Fatalf("projection dropped the record's timestamps: %+v", worker)
	}
	if worker.OwnerID != "alice" || worker.Visibility != VisibilityPrivate || len(worker.Members) != 1 {
		t.Fatalf("projection dropped the sharing state: %+v", worker)
	}
}

// Every built-in Worker Type must be reachable by its canonical id, or the canonical
// API can configure fewer Worker Types than the catalog advertises.
func TestBuiltInWorkerTypeForIDResolvesEveryBuiltInType(t *testing.T) {
	for _, meta := range builtInManagedWorkerTypes {
		got, ok := builtInWorkerTypeForID(meta.ID)
		if !ok || got.ConnectorKind != meta.ConnectorKind {
			t.Fatalf("worker type %q resolved to %+v (ok=%v)", meta.ID, got, ok)
		}
	}
	if _, ok := builtInWorkerTypeForID("example.unknown"); ok {
		t.Fatal("an unknown Worker Type id resolved to a built-in package")
	}
}
