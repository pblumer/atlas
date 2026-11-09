package pubinterface

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func kreditContract(t *testing.T) (Contract, Binding) {
	t.Helper()
	contract, binding, problems := Realize(Derive(compile(t, kreditBPMN), nil), "kredit", 7, 1, kreditSelection())
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	return contract, binding
}

func meta(rev uint64) Meta { return Meta{ActorID: "usr_pub", At: 1762100000, ExpectedRevision: rev} }

func TestStorePublishesImmutableVersionsAndRecovers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "published-interfaces")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	contract, binding := kreditContract(t)
	in, err := s.Publish("kreditpruefung", "app_kredit", contract, binding, meta(0))
	if err != nil {
		t.Fatal(err)
	}
	if in.Revision != 1 || len(in.Versions) != 1 || in.Latest().Version != 1 || in.History[0].Action != ActionPublish {
		t.Fatalf("first publish = %+v", in)
	}
	if _, err := s.Publish("kreditpruefung", "app_kredit", contract, binding, meta(1)); !errors.Is(err, ErrSameContract) {
		t.Fatalf("publishing the same contract again = %v, want ErrSameContract", err)
	}
	if _, err := s.Publish("kreditpruefung", "app_kredit", contract, binding, meta(0)); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale revision = %v", err)
	}
	other := binding
	other.ProcessID = "anderer"
	if _, err := s.Publish("kreditpruefung", "app_kredit", contract, other, meta(1)); !errors.Is(err, ErrOtherProcess) {
		t.Fatalf("another process = %v", err)
	}
	changed := contract.clone()
	changed.Returns.Messages = nil
	if in, err = s.Publish("kreditpruefung", "app_kredit", changed, binding, meta(1)); err != nil || in.Latest().Version != 2 {
		t.Fatalf("second version = %+v, %v", in, err)
	}
	rebound := binding.clone()
	rebound.DefinitionKey, rebound.DefinitionVersion = 9, 2
	if in, err = s.Rebind("kreditpruefung", 1, rebound, meta(2)); err != nil || in.Versions[0].Binding.DefinitionKey != 9 {
		t.Fatalf("rebind = %+v, %v", in, err)
	}
	if !in.Versions[0].Contract.Equal(contract) {
		t.Fatal("rebinding changed the contract")
	}
	if in, err = s.Deprecate("kreditpruefung", 1, "use version 2", meta(3)); err != nil || !in.Versions[0].Deprecated {
		t.Fatalf("deprecate = %+v, %v", in, err)
	}
	if _, err := s.Deprecate("kreditpruefung", 1, "", meta(4)); !errors.Is(err, ErrDeprecated) {
		t.Fatalf("deprecating twice = %v", err)
	}
	if _, err := s.Deprecate("kreditpruefung", 3, "", meta(4)); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("deprecating a missing version = %v", err)
	}

	again, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	back, ok := again.Get("kreditpruefung")
	if !ok || !reflect.DeepEqual(back, in) {
		t.Fatalf("recovered = %+v\nwant %+v", back, in)
	}
}

func TestStoreKeepsViewAndSendApart(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "published-interfaces"))
	if err != nil {
		t.Fatal(err)
	}
	contract, binding := kreditContract(t)
	if _, err := s.Publish("kreditpruefung", "app_kredit", contract, binding, meta(0)); err != nil {
		t.Fatal(err)
	}
	alice := Principal{UserID: "usr_alice"}
	peer := Principal{UserID: "usr_peer", GroupIDs: []string{"grp_peers"}}
	in, _ := s.Get("kreditpruefung")
	if in.MayView(alice) || in.MaySend(peer) {
		t.Fatal("a new interface grants something")
	}
	if _, ok := s.MaySend("kreditpruefung", 1, peer); ok {
		t.Fatal("send allowed without a grant")
	}
	in, err = s.SetGrants("kreditpruefung", Grants{Public: true, Send: Audience{GroupIDs: []string{"grp_peers"}}}, meta(1))
	if err != nil {
		t.Fatal(err)
	}
	if !in.MayView(alice) || in.MaySend(alice) {
		t.Fatal("public must let anybody view and nobody send")
	}
	if b, ok := s.MaySend("kreditpruefung", 1, peer); !ok || b.DefinitionKey != 7 {
		t.Fatalf("the send grant did not answer the binding: %+v %v", b, ok)
	}
	if _, ok := s.MaySend("kreditpruefung", 2, peer); ok {
		t.Fatal("send answered a version that does not exist")
	}
	if got := in.History[len(in.History)-1]; got.Action != ActionGrants || got.Grants == nil || !got.Grants.Public ||
		!reflect.DeepEqual(got.Grants.Send.GroupIDs, []string{"grp_peers"}) {
		t.Fatalf("grant audit = %+v", got)
	}
	if _, err := s.SetGrants("kreditpruefung", Grants{View: Audience{UserIDs: []string{"a", "a"}}}, meta(2)); err == nil {
		t.Fatal("a repeating audience was accepted")
	}
}

func TestStoreRefusesACorruptDocumentAtStartup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "published-interfaces")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	contract, binding := kreditContract(t)
	if _, err := s.Publish("kreditpruefung", "app_kredit", contract, binding, meta(0)); err != nil {
		t.Fatal(err)
	}
	file := s.files.FileFor("kreditpruefung")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// A document whose audit lost an entry no longer accounts for its own revision.
	tampered := strings.Replace(string(raw), `"revision": 1`, `"revision": 2`, 1)
	tampered = strings.Replace(tampered, `"revision":1`, `"revision":2`, 1)
	if tampered == string(raw) {
		t.Fatalf("no revision to tamper with in %s", raw)
	}
	if err := os.WriteFile(file, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir); err == nil {
		t.Fatal("a document with an incomplete audit loaded")
	}
}
