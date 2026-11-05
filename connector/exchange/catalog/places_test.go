package catalog_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/exchange/catalog"
)

// TestCreateRoomListIsADistributionGroupMarkedAsOne: Outlook's Room Finder lists rooms
// through room lists, which are distribution groups created with -RoomList. The
// operation sets the switch itself, on either target, and a model cannot turn it off
// (ADR-0532).
func TestCreateRoomListIsADistributionGroupMarkedAsOne(t *testing.T) {
	for _, target := range []catalog.Target{catalog.Online, catalog.OnPrem} {
		inv := build(t, "create-room-list", target, map[string]any{
			"Name": "RL-Bern", "DisplayName": "RL-Bern", "Alias": "rl-bern", "PrimarySmtpAddress": "rl-bern@contoso.ch",
		})
		if inv.Cmdlet != "New-DistributionGroup" || inv.Parameters["RoomList"] != true || inv.Parameters["Alias"] != "rl-bern" {
			t.Errorf("%s: create-room-list = %#v", target, inv)
		}
	}
	spec, _ := catalog.Lookup("create-room-list")
	if err := spec.CheckNames("create-room-list", []string{"Name", "RoomList"}); err == nil || !strings.Contains(err.Error(), "RoomList") {
		t.Errorf("authoring RoomList: err = %v, want a refusal naming it", err)
	}
	// A room list has rooms, not people: who may join or must authenticate to send is
	// not a question it answers.
	if err := spec.CheckNames("create-room-list", []string{"Name", "MemberJoinRestriction"}); err == nil {
		t.Error("create-room-list took MemberJoinRestriction")
	}
	// The room lists of an organization are found with the listing every group uses.
	list, _ := catalog.Lookup("list-distribution-groups")
	if err := list.CheckNames("list-distribution-groups", []string{"Filter", "RecipientTypeDetails"}); err != nil {
		t.Errorf("list-distribution-groups by RecipientTypeDetails: %v", err)
	}
}

// TestPlacesAreExchangeOnlineOnly: Set-Place and Get-Place exist only in Exchange
// Online. Against Exchange Server the operations are refused with what to do instead,
// before anything is sent, so a process copied for an Exchange Server does not fail on
// a cmdlet the server lacks.
func TestPlacesAreExchangeOnlineOnly(t *testing.T) {
	set := build(t, "set-place", catalog.Online, map[string]any{
		"Identity": "rm-fin-sitzung@contoso.ch", "City": "Bern", "Building": "Bern", "Floor": 5, "FloorLabel": "5. Stock", "Capacity": 8,
		"Tags": []any{"Beamer"},
	})
	if set.Cmdlet != "Set-Place" || set.Parameters["City"] != "Bern" || set.Parameters["Floor"] != 5 || set.Parameters["Capacity"] != 8 {
		t.Errorf("set-place = %#v", set)
	}
	if get := build(t, "get-place", catalog.Online, map[string]any{"Identity": "rm-fin-sitzung@contoso.ch"}); get.Cmdlet != "Get-Place" {
		t.Errorf("get-place = %#v", get)
	}
	for _, op := range []string{"set-place", "get-place"} {
		spec, _ := catalog.Lookup(op)
		_, err := spec.Build(op, catalog.OnPrem, "", map[string]any{"Identity": "raum", "City": "Bern"})
		if err == nil || !strings.Contains(err.Error(), "Exchange Online only") || !strings.Contains(err.Error(), "Set-User") {
			t.Errorf("%s on Exchange Server: err = %v, want a refusal that says what to do instead", op, err)
		}
	}

	spec, _ := catalog.Lookup("set-place")
	// A Set-Place that sets nothing is a call made for nothing.
	if err := spec.CheckNames("set-place", []string{"Identity"}); err == nil || !strings.Contains(err.Error(), "at least one of") {
		t.Errorf("set-place without metadata: err = %v", err)
	}
	// A value that evaluated to null is left out, and what remains still counts.
	if _, err := spec.Build("set-place", catalog.Online, "", map[string]any{"Identity": "raum", "City": nil, "Floor": nil}); err == nil {
		t.Error("set-place whose every value is null was built")
	}
	inv := build(t, "set-place", catalog.Online, map[string]any{"Identity": "raum", "City": "Bern", "Floor": nil})
	if _, sent := inv.Parameters["Floor"]; sent {
		t.Errorf("a null Floor was sent: %#v", inv.Parameters)
	}
	// The experimental Places hierarchy is Set-PlaceV3's, not this operation's.
	if err := spec.CheckNames("set-place", []string{"Identity", "ParentId"}); err == nil {
		t.Error("set-place took ParentId")
	}
}
