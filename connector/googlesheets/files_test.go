package googlesheets_test

import (
	"context"
	"strings"
	"testing"
	"time"

	gs "github.com/pblumer/atlas/connector/googlesheets"
)

// TestListFilesAsksDriveForOneFolder is the inbound half's whole read: the files a
// folder holds, newest last, bounded by the cursor and the page size
// (ADR-draft-google-inbound-watch).
func TestListFilesAsksDriveForOneFolder(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"GET /drive/v3/files": `{"files":[{"id":"f1","name":"Rechnung.pdf","createdTime":"2026-09-03T08:00:00.000Z"}]}`,
	})
	after := time.Date(2026, 9, 3, 7, 0, 0, 0, time.UTC)
	files, err := f.client().ListFiles(context.Background(), gs.FileQuery{
		Folder: "https://drive.google.com/drive/folders/fold", Field: "createdTime", After: after, Limit: 25,
	})
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0]["id"] != "f1" {
		t.Fatalf("files = %#v; want the one file Drive answered with", files)
	}
	q := f.calls[0].query
	for _, want := range []string{
		"in+parents",        // scoped to the folder
		"trashed+%3D+false", // and not to its bin
		"createdTime+%3E+%272026-09-03T07%3A00%3A00Z%27", // bounded by the cursor
		"orderBy=createdTime",
		"pageSize=25",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q is missing %q", q, want)
		}
	}
	if !strings.Contains(q, "fold") {
		t.Errorf("query %q should name the folder id extracted from the URL", q)
	}
}

// TestListFilesDescendingHasNoLowerBound: priming reads the newest file to place the
// cursor, so it orders the other way and asks for no window at all.
func TestListFilesDescending(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"GET /drive/v3/files": `{"files":[]}`,
	})
	if _, err := f.client().ListFiles(context.Background(), gs.FileQuery{
		Folder: "fold", Field: "modifiedTime", Limit: 1, Descending: true,
	}); err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	q := f.calls[0].query
	if !strings.Contains(q, "orderBy=modifiedTime+desc") {
		t.Errorf("query %q should order newest first", q)
	}
	if strings.Contains(q, "modifiedTime+%3E") {
		t.Errorf("query %q should carry no lower bound when the cursor is unset", q)
	}
}

// TestListFilesNeedsAFolder: a listing with no folder is every file the credential can
// see, which is never what a watch meant.
func TestListFilesNeedsAFolder(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{})
	if _, err := f.client().ListFiles(context.Background(), gs.FileQuery{Field: "createdTime"}); err == nil {
		t.Error("a listing with no folder: want an error, got nil")
	}
	if len(f.calls) != 0 {
		t.Errorf("made %d calls without a folder; want none", len(f.calls))
	}
}

// TestListFilesSkipsEntriesThatAreNotObjects: a malformed answer must not panic the
// bridge that polls it.
func TestListFilesSkipsEntriesThatAreNotObjects(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"GET /drive/v3/files": `{"files":["nope",{"id":"f2"}]}`,
	})
	files, err := f.client().ListFiles(context.Background(), gs.FileQuery{Folder: "fold", Field: "createdTime"})
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0]["id"] != "f2" {
		t.Errorf("files = %#v; want only the entry that is a file", files)
	}
}

// TestListFilesPropagatesAnError: a 403 for a folder the credential was never shared
// with is the failure an operator will actually hit.
func TestListFilesPropagatesAnError(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{}) // every route 404s
	if _, err := f.client().ListFiles(context.Background(), gs.FileQuery{Folder: "fold", Field: "createdTime"}); err == nil {
		t.Error("a failed listing: want an error, got nil")
	}
}
