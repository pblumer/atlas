package api

import (
	"regexp"
	"testing"
)

var workerTypeSemverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func TestEveryManagedConnectorKindHasBuiltInWorkerTypeMetadata(t *testing.T) {
	if len(builtInManagedWorkerTypes) != len(managedConnectorKinds) {
		t.Fatalf("built-in Worker Types=%d, managed connector kinds=%d: metadata and runtime registry must remain a bijection", len(builtInManagedWorkerTypes), len(managedConnectorKinds))
	}

	seenIDs := make(map[string]string, len(managedConnectorKinds))
	for _, kind := range managedConnectorKinds {
		meta, ok := lookupBuiltInManagedWorkerType(kind.name)
		if !ok {
			t.Errorf("managed connector kind %q has no built-in Worker Type metadata", kind.name)
			continue
		}
		if meta.ID != "atlas."+kind.name {
			t.Errorf("managed connector kind %q Worker Type id=%q, want atlas.%s", kind.name, meta.ID, kind.name)
		}
		if previous, exists := seenIDs[meta.ID]; exists {
			t.Errorf("Worker Type id %q is shared by managed connector kinds %q and %q", meta.ID, previous, kind.name)
		}
		seenIDs[meta.ID] = kind.name
		if !workerTypeSemverPattern.MatchString(meta.Version) {
			t.Errorf("managed connector kind %q Worker Type version=%q, want SemVer", kind.name, meta.Version)
		}
		if meta.Title == "" {
			t.Errorf("managed connector kind %q has no Worker Type title", kind.name)
		}
		if meta.Vendor != "Atlas" {
			t.Errorf("managed connector kind %q Worker Type vendor=%q, want Atlas", kind.name, meta.Vendor)
		}
		if meta.Origin != WorkerTypeOriginBuiltIn {
			t.Errorf("managed connector kind %q Worker Type origin=%q, want %q", kind.name, meta.Origin, WorkerTypeOriginBuiltIn)
		}
		switch meta.RuntimeMode {
		case WorkerRuntimeModeAtlasEmbedded, WorkerRuntimeModeAtlasSupervised:
		case WorkerRuntimeModeExternal:
			t.Errorf("managed connector kind %q is compiled into Atlas but declares external runtime mode", kind.name)
		default:
			t.Errorf("managed connector kind %q has invalid Worker Type runtime mode %q", kind.name, meta.RuntimeMode)
		}
	}

	for _, meta := range builtInManagedWorkerTypes {
		if _, ok := lookupManagedConnectorKind(meta.ConnectorKind); !ok {
			t.Errorf("built-in Worker Type %q references unknown managed connector kind %q", meta.ID, meta.ConnectorKind)
		}
	}
}

func TestWorkerOnlyCompatibilityFlagAgreesWithBuiltInRuntimeMetadata(t *testing.T) {
	for _, kind := range managedConnectorKinds {
		meta, ok := lookupBuiltInManagedWorkerType(kind.name)
		if !ok {
			continue
		}
		want := WorkerRuntimeModeAtlasEmbedded
		if kind.workerOnly {
			want = WorkerRuntimeModeAtlasSupervised
		}
		if meta.RuntimeMode != want {
			t.Errorf("managed connector kind %q workerOnly=%v but Worker Type runtime mode=%q, want %q", kind.name, kind.workerOnly, meta.RuntimeMode, want)
		}
	}
}
