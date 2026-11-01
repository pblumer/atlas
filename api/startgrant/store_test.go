package startgrant

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testRequest(app, process, actor string, expected uint64, users, groups []string) ChangeRequest {
	return ChangeRequest{
		Grant: Grant{ApplicationID: app, ProcessID: process, UserIDs: users, GroupIDs: groups},
		ActorID: actor, At: 1762010000, ExpectedRevision: expected,
	}
}

func TestStoreDenyByDefaultAndRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "start-grants")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	alice := Principal{UserID: "alice", GroupIDs: []string{"field"}}
	if s.Allowed("app-a", "credit", alice) {
		t.Fatal("missing grant allowed")
	}
	first, err := s.Set(testRequest("app-a", "credit", "admin", 0, []string{"alice"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || first.Action != ActionGrant || first.SchemaVersion != SchemaVersion {
		t.Fatalf("unexpected first record: %+v", first)
	}
	if !s.Allowed("app-a", "credit", alice) || s.Allowed("app-b", "credit", alice) || s.Allowed("app-a", "refund", alice) {
		t.Fatal("scope was not isolated")
	}
	second, err := s.Set(testRequest("app-a", "credit", "admin", 1, nil, []string{"field"}))
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || !s.Allowed("app-a", "credit", alice) {
		t.Fatal("group replacement failed")
	}
	restarted, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.Allowed("app-a", "credit", alice) {
		t.Fatal("grant was lost across restart")
	}
	history := restarted.History("app-a", "credit")
	if len(history) != 2 || history[0].Revision != 1 || history[1].Revision != 2 {
		t.Fatalf("audit history lost across restart: %+v", history)
	}
	if _, err := os.Stat(filepath.Join(dir, "not-a-record.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected record: %v", err)
	}
}

func TestStoreRevocationIsDurableAndProspective(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := Principal{UserID: "alice"}
	if _, err := s.Set(testRequest("app-a", "credit", "admin", 0, []string{"alice"}, nil)); err != nil {
		t.Fatal(err)
	}
	if !s.Allowed("app-a", "credit", p) {
		t.Fatal("grant not effective")
	}
	revoked, err := s.Set(testRequest("app-a", "credit", "admin", 1, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Action != ActionRevoke || s.Allowed("app-a", "credit", p) {
		t.Fatal("revocation did not take effect")
	}
	restarted, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Allowed("app-a", "credit", p) {
		t.Fatal("revoked grant resurrected after restart")
	}
	rec, ok := restarted.Current("app-a", "credit")
	if !ok || rec.Revision != 2 || rec.Action != ActionRevoke || len(restarted.History("app-a", "credit")) != 2 {
		t.Fatalf("revocation or audit missing: %+v, %v", rec, ok)
	}
}

func TestStoreRejectsStaleRevisionAndInvalidMutation(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	req := testRequest("app-a", "credit", "admin", 0, []string{"alice"}, nil)
	if _, err := s.Set(req); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ChangeRequest{
		req,
		testRequest("", "credit", "admin", 0, []string{"alice"}, nil),
		testRequest("app-a", "", "admin", 0, []string{"alice"}, nil),
		testRequest("app-a", "credit", "", 1, []string{"alice"}, nil),
		testRequest("app-a", "credit", "admin", 1, []string{""}, nil),
		testRequest("app-a", "credit", "admin", 1, []string{"alice", "alice"}, nil),
	} {
		if _, err := s.Set(bad); err == nil {
			t.Fatalf("invalid mutation accepted: %+v", bad)
		}
	}
	rec, ok := s.Current("app-a", "credit")
	if !ok || rec.Revision != 1 {
		t.Fatalf("invalid mutation changed state: %+v", rec)
	}
}

func TestStoreDoesNotExposeMutableGrantOrAudit(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(testRequest("app-a", "credit", "admin", 0, []string{"alice"}, []string{"field"})); err != nil {
		t.Fatal(err)
	}
	rec, _ := s.Current("app-a", "credit")
	rec.Grant.UserIDs[0] = "mallory"
	rec.Grant.GroupIDs[0] = "other"
	history := s.History("app-a", "credit")
	history[0].Grant.UserIDs[0] = "mallory"
	if !s.Allowed("app-a", "credit", Principal{UserID: "alice"}) {
		t.Fatal("caller mutated stored grant")
	}
	rec2, _ := s.Current("app-a", "credit")
	if !reflect.DeepEqual(rec2.Grant.UserIDs, []string{"alice"}) {
		t.Fatalf("grant mutated through returned value: %+v", rec2)
	}
}

func TestStoreFailsClosedOnTamperedHistory(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(testRequest("app-a", "credit", "admin", 0, []string{"alice"}, nil)); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("files: %v, %v", files, err)
	}
	path := filepath.Join(dir, files[0].Name())
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("tampered audit must fail closed: %v", err)
	}
}

func TestStoreWriteFailureNeverGrants(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(testRequest("app-a", "credit", "admin", 0, []string{"alice"}, nil)); err == nil {
		t.Fatal("write failure reported success")
	}
	if s.Allowed("app-a", "credit", Principal{UserID: "alice"}) {
		t.Fatal("failed write became visible")
	}
	if _, err := s.Set(testRequest("app-a", "credit", "admin", 0, []string{"alice"}, nil)); err == nil {
		t.Fatal("uncertain store accepted another write")
	}
}
