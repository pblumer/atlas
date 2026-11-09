package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/workersdk"
)

// Outside a job the adapters a Worker Instance hands its connectors reach no document
// store, and say so; a mail job on a host with no S3 Worker cannot export
// (ADR-draft-a-file-is-a-document-not-a-variable).
func TestDocumentAdaptersOutsideAJob(t *testing.T) {
	ctx := context.Background()
	if _, _, _, err := (sdkDocuments{}).ReadDocument(ctx, "doc_a"); !errors.Is(err, workersdk.ErrNoDocuments) {
		t.Errorf("an S3 read outside a job = %v", err)
	}
	if _, err := (sdkDocuments{}).StoreDocument(ctx, "a.csv", "text/csv", []byte("x")); !errors.Is(err, workersdk.ErrNoDocuments) {
		t.Errorf("an S3 store outside a job = %v", err)
	}
	m := mailDocuments{}
	if m.HoldsObjectStores() {
		t.Error("a host with no S3 Worker claims to hold one")
	}
	if _, err := m.PutDocument(ctx, "archiv", "b", "k", "text/plain", "", []byte("x"), nil); err == nil || !strings.Contains(err.Error(), "archiv") {
		t.Errorf("an export with no S3 Worker = %v", err)
	}
	if _, err := m.StoreDocument(ctx, "a.pdf", "application/pdf", []byte("x")); !errors.Is(err, workersdk.ErrNoDocuments) {
		t.Errorf("a mail store outside a job = %v", err)
	}
}
