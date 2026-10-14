package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The recovery property for the record kinds a restart on the same directory never
// replays: there the state store is already current, so Recover folds nothing and
// the decode-and-apply path for these records is never taken. Replaying the log into
// an empty store is the only way to show that state rebuilt from the log alone is the
// state the live run built (invariant I4).

// replayIntoFreshStore opens the log under dir beside a new, empty state store.
func replayIntoFreshStore(t *testing.T, dir string) *harness {
	t.Helper()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state-replayed"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	return &harness{dir: dir, log: log, store: store}
}

// TestTheInventoryRebuildsFromTheLogAlone: what is held and what was held — grants,
// a return, a correction — come back from the log into an empty store exactly as the
// live run left them.
func TestTheInventoryRebuildsFromTheLogAlone(t *testing.T) {
	dir := t.TempDir()
	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.GrantEntitlement(model.EntitlementValue{Principal: "usr_alice", ItemID: "vpn", OrderID: "ord_1",
		Since: 1000, Origin: model.OriginOrdered, ApprovedBy: "usr_boss"})
	p1.GrantEntitlement(model.EntitlementValue{Principal: "usr_alice", ItemID: "laptop", Since: 1000, Until: 5000})
	p1.GrantEntitlement(model.EntitlementValue{Principal: "usr_bruno", ItemID: "vpn", Since: 1500, Origin: model.OriginLegacy})
	p1.RevokeEntitlement("usr_alice", "laptop", 2000, model.EndReturned, "usr_ada")
	p1.RevokeEntitlement("usr_bruno", "vpn", 2500, model.EndCorrected, "usr_ops")
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	live := map[string][2]any{}
	for _, who := range []string{"usr_alice", "usr_bruno"} {
		live[who] = [2]any{heldBy(t, h1.store, who), endedBy(t, h1.store, who)}
	}
	if held := live["usr_alice"][0].([]model.EntitlementValue); len(held) != 1 {
		t.Fatalf("live: alice holds %+v, want only the vpn", held)
	}
	h1.close(t)

	h2 := replayIntoFreshStore(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, &manualClock{})
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover (replay): %v", err)
	}
	for who, want := range live {
		got := [2]any{heldBy(t, h2.store, who), endedBy(t, h2.store, who)}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s after replay = %+v, want what the live run built: %+v", who, got, want)
		}
	}
}

// TestAMigrationsIndexChangesRebuildFromTheLogAlone: the membership events a
// migration emits are what put an instance into its new version's index, and a
// replay into an empty store has nothing else to go on.
func TestAMigrationsIndexChangesRebuildFromTheLogAlone(t *testing.T) {
	dir := t.TempDir()
	v1, ids1 := identProcess(t, 7, 1)
	v2, ids2 := identProcess(t, 8, 2, "identityId")
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(v1)
	p1.Deploy(v2)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(v1.Key, model.VariableValue{Name: "identityId", Kind: model.VarString, Text: "MT-1998"})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	piKey := onlyInstance(t, h1.store)
	migrate(t, p1, piKey, v1, v2, ids1, ids2)
	live := instancesByVar(t, h1.store, "identityId", "MT-1998")
	if len(live) != 1 || live[0] != piKey {
		t.Fatalf("live index = %v, want the migrated instance %d", live, piKey)
	}
	h1.close(t)

	h2 := replayIntoFreshStore(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(v1)
	p2.Deploy(v2)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover (replay): %v", err)
	}
	if got := instancesByVar(t, h2.store, "identityId", "MT-1998"); !reflect.DeepEqual(got, live) {
		t.Errorf("index after replay = %v, want %v", got, live)
	}
	if v := readVar(t, h2.store, piKey, "identityId"); v == nil || !v.Indexed {
		t.Errorf("identityId after replay = %+v, want the record marked for the index", v)
	}
}
