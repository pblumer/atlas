package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// A picture from the directory (ADR-0367).
//
// The mirror's discipline is what these mostly hold, not the bytes: the decider
// produces a complete plan and the apply writes it without re-deciding anything,
// so a picture has to make itself visible to the decision rather than arriving
// behind it. Three things follow, and each has a test: a changed picture is a
// changed record, a picture somebody chose is not overwritten, and a report
// carries no bytes.

// aPhoto is a directory entry carrying a real JPEG, base64 as the worker produces
// it. Real bytes, because what receives them checks the content and not the label.
func aPhoto(oid, tail string) directoryPhoto {
	return directoryPhoto{
		ID:          oid,
		ContentType: "image/jpeg",
		Data:        base64.StdEncoding.EncodeToString([]byte("\xff\xd8\xff" + tail)),
	}
}

// mirrored is an account this directory already owns.
func mirrored(id, name, oid string) User {
	return User{ID: id, Username: name, Email: name + "@example.org",
		Source: SourceEntra, ExternalID: oid, DirectoryID: oid, Roles: []string{RoleUser}}
}

// onlyUser is the single account decision in a plan.
func onlyUser(t *testing.T, plan directoryPlan) directoryUserDecision {
	t.Helper()
	if len(plan.Users) != 1 {
		t.Fatalf("the plan decided %d accounts, want 1: %+v", len(plan.Users), plan.Users)
	}
	return plan.Users[0]
}

// TestAPictureFromTheDirectoryReachesANewAccountAndAKnownOne.
//
// Both paths, because they are different functions: an account being created gets
// the picture as part of its first record, and one that already exists gets it as
// a change to a record that may otherwise be identical.
func TestAPictureFromTheDirectoryReachesANewAccountAndAKnownOne(t *testing.T) {
	state := directorySyncState{ID: directorySyncStateID, Revision: 1}

	// New.
	plan := decide(t, directorySyncMessage{FromRevision: 1,
		Users:  []directoryUser{person("oid-a", "ada@example.org", "Ada")},
		Photos: []directoryPhoto{aPhoto("oid-a", "ada")},
	}, state, nil, nil)
	dec := onlyUser(t, plan)
	if dec.Action != dirUserCreate {
		t.Fatalf("action = %q, want create", dec.Action)
	}
	if string(dec.Photo) != "\xff\xd8\xffada" || dec.PhotoType != "image/jpeg" {
		t.Errorf("the plan carries %q as %q", dec.Photo, dec.PhotoType)
	}
	if dec.Record.AvatarSource != AvatarFromDirectory || dec.Record.AvatarFingerprint == "" {
		t.Errorf("the record says %q / %q, want the directory and a fingerprint",
			dec.Record.AvatarSource, dec.Record.AvatarFingerprint)
	}
	if plan.Counts.PhotosWritten != 1 {
		t.Errorf("photosWritten = %d, want 1", plan.Counts.PhotosWritten)
	}

	// Known, and otherwise unchanged. This is the case the fingerprint exists for:
	// without it the record would compare equal and the plan would say unchanged
	// while the apply wrote a file.
	known := []User{mirrored("usr_1", "ada", "oid-a")}
	known[0].DisplayName = "Ada"
	plan = decide(t, directorySyncMessage{FromRevision: 1,
		Users:  []directoryUser{person("oid-a", "ada@example.org", "Ada")},
		Photos: []directoryPhoto{aPhoto("oid-a", "ada")},
	}, state, known, nil)
	dec = onlyUser(t, plan)
	if dec.Action != dirUserUpdate {
		t.Fatalf("an account whose only change is its picture was decided %q", dec.Action)
	}
	if len(dec.Photo) == 0 {
		t.Error("the plan carries no bytes, so the apply would have nothing to write")
	}
}

// TestTheSamePictureTwiceIsNotAWrite.
//
// The other half of what the fingerprint buys. A process that fetches photos on
// every run hands the same bytes back every time, and a mirror that rewrote them
// would report a change on every account, every run, for ever.
func TestTheSamePictureTwiceIsNotAWrite(t *testing.T) {
	state := directorySyncState{ID: directorySyncStateID, Revision: 1}
	msg := directorySyncMessage{FromRevision: 1,
		Users:  []directoryUser{person("oid-a", "ada@example.org", "Ada")},
		Photos: []directoryPhoto{aPhoto("oid-a", "ada")},
	}
	first := onlyUser(t, decide(t, msg, state, nil, nil))

	// The account as the first run would have left it.
	settled := first.Record
	plan := decide(t, msg, state, []User{settled}, nil)
	dec := onlyUser(t, plan)
	if dec.Action != dirUserUnchanged {
		t.Errorf("the same picture again was decided %q, want unchanged", dec.Action)
	}
	if len(dec.Photo) != 0 {
		t.Error("the same picture again would be written a second time")
	}
	if plan.Counts.PhotosUnchanged != 1 || plan.Counts.PhotosWritten != 0 {
		t.Errorf("counts = unchanged %d, written %d", plan.Counts.PhotosUnchanged, plan.Counts.PhotosWritten)
	}
}

// TestAMirrorDoesNotOverwriteAChoice.
//
// In both directions. The directory may replace or remove what the directory gave,
// and neither what somebody picked for themselves — and the run says how often it
// stood back, as a number rather than a line per person, because for anybody who
// ever set their own picture this is the ordinary case.
func TestAMirrorDoesNotOverwriteAChoice(t *testing.T) {
	state := directorySyncState{ID: directorySyncStateID, Revision: 1}
	own := mirrored("usr_1", "ada", "oid-a")
	own.DisplayName = "Ada"
	own.AvatarSource = AvatarUploaded
	own.AvatarFingerprint = "whatever-they-uploaded"

	for _, c := range []struct {
		name  string
		entry directoryPhoto
	}{
		{"a picture the directory offers", aPhoto("oid-a", "from-the-tenant")},
		{"a removal the directory reports", directoryPhoto{ID: "oid-a", Removed: true}},
	} {
		plan := decide(t, directorySyncMessage{FromRevision: 1,
			Users:  []directoryUser{person("oid-a", "ada@example.org", "Ada")},
			Photos: []directoryPhoto{c.entry},
		}, state, []User{own}, nil)
		dec := onlyUser(t, plan)
		if dec.Action != dirUserUnchanged {
			t.Errorf("%s: the account was decided %q; a choice was overwritten", c.name, dec.Action)
		}
		if len(dec.Photo) != 0 || dec.PhotoClear {
			t.Errorf("%s: the plan would touch the picture", c.name)
		}
		if plan.Counts.PhotosKept != 1 {
			t.Errorf("%s: photosKept = %d, want 1", c.name, plan.Counts.PhotosKept)
		}
	}
}

// TestTheDirectoryMayTakeBackWhatItGave.
//
// A removal is an explicit statement, and it has to be: the absence of an entry
// means "not fetched", so without a way to say "there is none" a picture deleted
// in the tenant would stay on the account for ever.
func TestTheDirectoryMayTakeBackWhatItGave(t *testing.T) {
	state := directorySyncState{ID: directorySyncStateID, Revision: 1}
	held := mirrored("usr_1", "ada", "oid-a")
	held.DisplayName = "Ada"
	held.AvatarSource = AvatarFromDirectory
	held.AvatarFingerprint = "abc"

	plan := decide(t, directorySyncMessage{FromRevision: 1,
		Users:  []directoryUser{person("oid-a", "ada@example.org", "Ada")},
		Photos: []directoryPhoto{{ID: "oid-a", Removed: true}},
	}, state, []User{held}, nil)
	dec := onlyUser(t, plan)
	if !dec.PhotoClear {
		t.Fatal("the removal was not planned")
	}
	if dec.Record.AvatarSource != "" || dec.Record.AvatarFingerprint != "" {
		t.Errorf("the record still claims a picture: %q / %q",
			dec.Record.AvatarSource, dec.Record.AvatarFingerprint)
	}
	if dec.Action == dirUserUnchanged {
		t.Error("an account that lost its picture was decided unchanged")
	}
	if plan.Counts.PhotosCleared != 1 {
		t.Errorf("photosCleared = %d, want 1", plan.Counts.PhotosCleared)
	}

	// A removal for somebody who has no picture is not a change.
	plain := mirrored("usr_2", "bob", "oid-b")
	plain.DisplayName = "Bob"
	plan = decide(t, directorySyncMessage{FromRevision: 1,
		Users:  []directoryUser{person("oid-b", "bob@example.org", "Bob")},
		Photos: []directoryPhoto{{ID: "oid-b", Removed: true}},
	}, state, []User{plain}, nil)
	if dec := onlyUser(t, plan); dec.Action != dirUserUnchanged || dec.PhotoClear {
		t.Errorf("removing a picture nobody has was decided %q / clear=%v", dec.Action, dec.PhotoClear)
	}
}

// TestBytesThatAreNotAPictureAreReportedAndNotWritten.
//
// Every refusal here is about the entry and not about the person, and none of them
// may cost the account the rest of its page: a name change carried by a
// change-tracking read will never be sent again, and dropping it to register a bad
// photo would lose it for good.
func TestBytesThatAreNotAPictureAreReportedAndNotWritten(t *testing.T) {
	state := directorySyncState{ID: directorySyncStateID, Revision: 1}
	for _, c := range []struct {
		name  string
		entry directoryPhoto
		says  string
	}{
		{"not base64", directoryPhoto{ID: "oid-a", ContentType: "image/jpeg", Data: "not base64!!"}, "base64"},
		{"a type nothing serves", directoryPhoto{ID: "oid-a", ContentType: "image/svg+xml",
			Data: base64.StdEncoding.EncodeToString([]byte(`<svg/>`))}, "not a kind"},
		{"bytes that are not what they claim", directoryPhoto{ID: "oid-a", ContentType: "image/jpeg",
			Data: base64.StdEncoding.EncodeToString([]byte("just text"))}, "not a image/jpeg"},
		{"an empty entry that says nothing", directoryPhoto{ID: "oid-a", ContentType: "image/jpeg"}, "no bytes"},
	} {
		plan := decide(t, directorySyncMessage{FromRevision: 1,
			Users:  []directoryUser{person("oid-a", "ada@example.org", "Ada Lovelace")},
			Photos: []directoryPhoto{c.entry},
		}, state, nil, nil)
		dec := onlyUser(t, plan)
		// The account is still created, with its name.
		if dec.Action != dirUserCreate || dec.Record.DisplayName != "Ada Lovelace" {
			t.Errorf("%s: a bad picture cost the account its page: %q / %q",
				c.name, dec.Action, dec.Record.DisplayName)
		}
		if len(dec.Photo) != 0 || dec.Record.AvatarSource != "" {
			t.Errorf("%s: the bad picture was planned anyway", c.name)
		}
		if plan.Counts.PhotosRefused != 1 {
			t.Errorf("%s: photosRefused = %d, want 1", c.name, plan.Counts.PhotosRefused)
		}
		found := false
		for _, n := range plan.Notes {
			if n.Kind == notePhoto && strings.Contains(n.Detail, c.says) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no note says why; notes = %+v", c.name, plan.Notes)
		}
	}
}

// TestAReportCarriesNoPictures.
//
// The reporting mode answers with counts and notes. A plan that marshalled its
// bytes would put a megabyte of base64 per person into a response somebody reads
// in a browser — and the record beside it is excluded for the same reason, which
// is why this checks the shape rather than trusting the tag.
func TestAReportCarriesNoPictures(t *testing.T) {
	state := directorySyncState{ID: directorySyncStateID, Revision: 1}
	plan := decide(t, directorySyncMessage{FromRevision: 1,
		Users:  []directoryUser{person("oid-a", "ada@example.org", "Ada")},
		Photos: []directoryPhoto{aPhoto("oid-a", "unmistakable-marker")},
	}, state, nil, nil)
	raw, err := json.Marshal(plan.Users)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leak := range []string{"unmistakable-marker",
		base64.StdEncoding.EncodeToString([]byte("unmistakable-marker"))} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the plan's JSON carries the picture: %s", raw)
		}
	}
}
