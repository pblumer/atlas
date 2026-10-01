package taskfolder

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestARuleWithNoMatchOrConditionsIsStoredAsAll: a folder posted with an empty rule
// is "every task", stated as such on disk — match all over an empty list — rather
// than as blanks a later reader has to interpret.
func TestARuleWithNoMatchOrConditionsIsStoredAsAll(t *testing.T) {
	svc, store := newService(t)
	got := create(t, svc, `{"name":"Alles","rule":{}}`, "usr_me")
	stored, ok, err := store.Get(got.ID)
	if err != nil || !ok {
		t.Fatalf("Get(%s) = %v, %v", got.ID, ok, err)
	}
	if stored.Rule.Match != MatchAll || stored.Rule.Conditions == nil || len(stored.Rule.Conditions) != 0 {
		t.Fatalf("stored rule = %+v, want match all over an empty list", stored.Rule)
	}
}

// TestAnUpdateMayMoveAFolder: the position is part of the sidebar's order, and an
// update that states one puts the folder there; one that does not leaves it.
func TestAnUpdateMayMoveAFolder(t *testing.T) {
	svc, _ := newService(t)
	got := create(t, svc, kundenRule, "usr_me")
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"name":"Kunden","rule":{"match":"all","conditions":[]},"position":7}`, 7},
		{`{"name":"Kunden","rule":{"match":"all","conditions":[]}}`, 7},
	} {
		rec := do(t, svc.HandleUpdate, as(http.MethodPut, tc.body, "usr_me"), map[string]string{"id": got.ID})
		if rec.Code != http.StatusOK {
			t.Fatalf("update = %d %s", rec.Code, rec.Body)
		}
		var after folderResp
		if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
			t.Fatal(err)
		}
		if after.Position != tc.want {
			t.Errorf("position after %s = %d, want %d", tc.body, after.Position, tc.want)
		}
	}
}

// TestAPreviewWithoutAMatchMatchesOnAll: the dialog sends no match until somebody
// picks one, and the count it shows must be the one the saved folder will show.
func TestAPreviewWithoutAMatchMatchesOnAll(t *testing.T) {
	svc, _ := newService(t)
	body := `{"rule":{"conditions":[{"field":"process","op":"is","value":"kunden-anfrage"}]}}`
	rec := do(t, svc.HandlePreview, as(http.MethodPost, body, "usr_me"), nil)
	var out struct {
		OK      bool `json:"ok"`
		Matched int  `json:"matched"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Matched != 2 {
		t.Fatalf("preview = %+v (%s), want the two kunden-anfrage tasks", out, rec.Body)
	}
}

// TestAnAgeOutsideItsRangeIsRefused: an age is between one and 9999 hours or days;
// zero would match everything and a five-digit count is a typo, not a policy.
func TestAnAgeOutsideItsRangeIsRefused(t *testing.T) {
	for _, value := range []string{"0", "10000"} {
		_, err := Compile(Rule{Match: MatchAll, Conditions: []Condition{
			{Field: FieldInstanceAge, Op: OpOlderThan, Value: value, Unit: UnitDays},
		}})
		if err == nil || !strings.Contains(err.Error(), "value must be between 1 and 9999") {
			t.Errorf("Compile(age %s) = %v, want the range refused", value, err)
		}
	}
}
