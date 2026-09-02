package infomodel

import (
	"strings"
	"testing"
)

// storeModel is the sales model with a store for its orders.
func storeModel() Model {
	m := orderModel()
	m.Stores = []DataStore{{
		ID: "s1", Name: "Orders", Class: "Order", Worker: "clio-main", Mode: StoreModeRead,
		Documentation: "Every order the business has ever taken.",
	}}
	return m
}

// TestValidateAcceptsAWellFormedStore is the baseline: a store holding a class that
// can be addressed by identity, in a mode this build implements.
func TestValidateAcceptsAWellFormedStore(t *testing.T) {
	if res := Validate(storeModel()); !res.Valid {
		t.Fatalf("a well-formed store was rejected: %v", findingCodes(res))
	}
}

// TestValidateStoreNeedsAnAddressableClass is the rule the whole read direction
// rests on: a process reads from a store by naming *which* thing it wants, and the
// only thing that names one is the business key. A class with no identity has
// nothing to look up by, so a store of them could be written to and never read.
func TestValidateStoreNeedsAnAddressableClass(t *testing.T) {
	m := storeModel()
	m.Classes[1].Identity = nil // Order loses its key
	res := Validate(m)
	if !hasCode(res, CodeStoreClassHasNoKey) {
		t.Fatalf("a store over a class with no identity was accepted: %v", findingCodes(res))
	}
	var msg string
	for _, f := range res.Findings {
		if f.Code == CodeStoreClassHasNoKey {
			msg = f.Message
			if f.StoreID != "s1" {
				t.Errorf("finding does not name the store: %+v", f)
			}
		}
	}
	if !strings.Contains(msg, "Order") {
		t.Errorf("message %q does not name the class", msg)
	}
}

// TestValidateStoreClassRules covers what a store may hold: a business object, and
// one this model actually has. A value type has no existence of its own to keep, and
// an enumeration is a set of values rather than things.
func TestValidateStoreClassRules(t *testing.T) {
	tests := []struct {
		name, class, want string
	}{
		{"a business object", "Order", ""},
		{"a class nothing declares", "Claim", CodeStoreUnknownClass},
		{"a value type", "Address", CodeStoreClassNotStorable},
		{"an enumeration", "OrderStatus", CodeStoreClassNotStorable},
		{"no class at all", "", CodeStoreUnknownClass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := storeModel()
			m.Stores[0].Class = tt.class
			res := Validate(m)
			if tt.want == "" {
				if !res.Valid {
					t.Fatalf("rejected: %v", findingCodes(res))
				}
				return
			}
			if !hasCode(res, tt.want) {
				t.Fatalf("expected %s, got %v", tt.want, findingCodes(res))
			}
		})
	}
}

// TestValidateStoreShape covers the rest: a name, a unique one, and a mode this
// build implements. Writing through a store is a decision of its own, so declaring
// it is refused as out of subset rather than silently accepted and ignored.
func TestValidateStoreShape(t *testing.T) {
	m := storeModel()
	m.Stores[0].Name = "  "
	if res := Validate(m); !hasCode(res, CodeStoreMissingName) {
		t.Errorf("a nameless store was accepted: %v", findingCodes(res))
	}

	m = storeModel()
	m.Stores = append(m.Stores, DataStore{ID: "s2", Name: "Orders", Class: "Order", Mode: StoreModeRead})
	if res := Validate(m); !hasCode(res, CodeDuplicateStoreName) {
		t.Errorf("two stores of one name were accepted: %v", findingCodes(res))
	}

	m = storeModel()
	m.Stores[0].Mode = "write"
	res := Validate(m)
	if !hasCode(res, CodeStoreUnknownMode) {
		t.Fatalf("a write store was accepted: %v", findingCodes(res))
	}
	for _, f := range res.Findings {
		if f.Code == CodeStoreUnknownMode && f.Reason != RefusedOutOfSubset {
			t.Errorf("reason = %q, want %q — writing is a thing, this build does not do it yet",
				f.Reason, RefusedOutOfSubset)
		}
	}
}

// TestValidateStoreWithoutAWorkerIsFine pins that a store may be modeled before it
// is wired: drawing where an order lives is a modeling act, configuring the worker
// behind it is an operational one, and they happen on different days.
func TestValidateStoreWithoutAWorkerIsFine(t *testing.T) {
	m := storeModel()
	m.Stores[0].Worker = ""
	if res := Validate(m); !res.Valid {
		t.Errorf("an unwired store was rejected: %v", findingCodes(res))
	}
}

// TestVocabularyCarriesStores covers the read a deploy uses: a process naming a
// <dataStore> resolves it against the application's model.
func TestVocabularyCarriesStores(t *testing.T) {
	vocab := NewVocabulary([]Model{storeModel()})
	store, ok := vocab.Store("Orders")
	if !ok {
		t.Fatal("the store is missing from the vocabulary")
	}
	if store.Class != "Order" || store.Worker != "clio-main" || store.Mode != StoreModeRead {
		t.Errorf("store = %+v", store)
	}
	if _, ok := vocab.Store("Invoices"); ok {
		t.Error("a store nothing declares resolved")
	}
}

// TestSubsetDescribesStores checks the browser is told what a store may be, the
// same way it is told what a class may be — one table, served rather than copied.
func TestSubsetDescribesStores(t *testing.T) {
	s := AuthoringSubset()
	if len(s.StoreModes) == 0 {
		t.Fatal("the subset does not describe what a store may do")
	}
	for _, m := range s.StoreModes {
		if m.Mode == "" || m.Label == "" || m.Meaning == "" {
			t.Errorf("store mode %+v does not say what it is", m)
		}
	}
	var writing bool
	for _, l := range s.Limits {
		if strings.Contains(l.Area, "Writing") {
			writing = true
		}
	}
	if !writing {
		t.Error("the subset does not state that writing through a store is not authored")
	}
}
