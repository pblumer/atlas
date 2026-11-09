package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/workertype"
)

// reviewedBuiltInCapabilityClaims is every technical capability claim a built-in
// Worker Type's operation table carries, written out once more on purpose: a claim
// is a statement about what an operation does (ADR-0480), so adding, changing or
// dropping one is a decision somebody makes in review, not a side effect of editing
// a table row. The claims themselves live on the rows (connector/entra.Ops,
// connector/teamsphone.Ops); this is the record of what was reviewed.
//
// The identifiers are provider-neutral and every one carries a provider profile.
// That is the combination ADR-0483 allows without evidence: a neutral name says
// what kind of step it is, the profile says that only Microsoft Teams' behaviour is
// claimed, and no two Worker Types are thereby said to be interchangeable.
var reviewedBuiltInCapabilityClaims = map[string]map[string][]workertype.CapabilityClaim{
	connectorKindEntra: {
		"create-team":     {{ID: "io.atlas.collaboration.space.create", Version: "0.1.0", Profile: "microsoft-teams"}},
		"add-team-member": {{ID: "io.atlas.collaboration.space.member.add", Version: "0.1.0", Profile: "microsoft-teams"}},
		"add-team-owner":  {{ID: "io.atlas.collaboration.space.owner.add", Version: "0.1.0", Profile: "microsoft-teams"}},
		"create-channel":  {{ID: "io.atlas.collaboration.channel.create", Version: "0.1.0", Profile: "microsoft-teams"}},
		"archive-team":    {{ID: "io.atlas.collaboration.space.archive", Version: "0.1.0", Profile: "microsoft-teams"}},
	},
	connectorKindTeamsPhone: {
		"ensure-resource-account": {{ID: "io.atlas.telephony.service-account.ensure", Version: "0.1.0", Profile: "microsoft-teams-phone"}},
		"remove-resource-account": {{ID: "io.atlas.telephony.service-account.remove", Version: "0.1.0", Profile: "microsoft-teams-phone"}},
		"ensure-call-queue":       {{ID: "io.atlas.telephony.call-queue.ensure", Version: "0.1.0", Profile: "microsoft-teams-phone"}},
		"remove-call-queue":       {{ID: "io.atlas.telephony.call-queue.remove", Version: "0.1.0", Profile: "microsoft-teams-phone"}},
		"ensure-auto-attendant":   {{ID: "io.atlas.telephony.auto-attendant.ensure", Version: "0.1.0", Profile: "microsoft-teams-phone"}},
		"remove-auto-attendant":   {{ID: "io.atlas.telephony.auto-attendant.remove", Version: "0.1.0", Profile: "microsoft-teams-phone"}},
		"ensure-phone-number":     {{ID: "io.atlas.telephony.number-assignment.ensure", Version: "0.1.0", Profile: "microsoft-teams-phone"}},
		"remove-phone-number":     {{ID: "io.atlas.telephony.number-assignment.remove", Version: "0.1.0", Profile: "microsoft-teams-phone"}},
		"list-free-phone-numbers": {{ID: "io.atlas.telephony.phone-number.list-unassigned", Version: "0.1.0", Profile: "microsoft-teams-phone"}},
		"remove-hotline":          {{ID: "io.atlas.telephony.hotline.remove", Version: "0.1.0", Profile: "microsoft-teams-phone"}},
	},
}

func projectedClaims(t *testing.T) map[string]map[string][]workertype.CapabilityClaim {
	t.Helper()
	got := map[string]map[string][]workertype.CapabilityClaim{}
	for kind := range builtInClaimSources {
		for _, op := range builtInOperationClaims(kind) {
			if got[kind] == nil {
				got[kind] = map[string][]workertype.CapabilityClaim{}
			}
			claims := make([]workertype.CapabilityClaim, 0, len(op.Capabilities))
			for _, c := range op.Capabilities {
				claims = append(claims, workertype.CapabilityClaim{ID: c.ID, Version: c.Version, Profile: c.Profile})
			}
			got[kind][op.ID] = claims
		}
	}
	return got
}

// TestBuiltInCapabilityClaimsAreTheReviewedOnes: what the operation tables declare is
// exactly what was reviewed — nothing added, changed or lost on the way.
func TestBuiltInCapabilityClaimsAreTheReviewedOnes(t *testing.T) {
	if got := projectedClaims(t); !reflect.DeepEqual(got, reviewedBuiltInCapabilityClaims) {
		t.Fatalf("built-in capability claims drifted from the reviewed set.\n got: %v\nwant: %v\nA claim is a reviewed statement (ADR-0480): change reviewedBuiltInCapabilityClaims in the same change, deliberately.", got, reviewedBuiltInCapabilityClaims)
	}
}

// TestBuiltInCapabilityClaimsPassTheManifestRule holds a built-in table to the rule a
// manifest is admitted by, and to the one stricter rule a built-in claim owes: a
// profile, because a neutral identifier without one would read as a claim about
// every provider.
func TestBuiltInCapabilityClaimsPassTheManifestRule(t *testing.T) {
	for kind := range builtInClaimSources {
		if _, ok := lookupManagedConnectorKind(kind); !ok {
			t.Errorf("claim source %q is no managed Worker Type", kind)
		}
		for _, op := range builtInOperationClaims(kind) {
			claims := make([]workertype.CapabilityClaim, 0, len(op.Capabilities))
			for _, c := range op.Capabilities {
				claims = append(claims, workertype.CapabilityClaim{ID: c.ID, Version: c.Version, Profile: c.Profile})
				if c.Profile == "" {
					t.Errorf("%s %s claims %s without a profile: a built-in claim names the provider whose behaviour it states", kind, op.ID, c.ID)
				}
				if c.State != CapabilityClaimCandidate {
					t.Errorf("%s %s claim %s state=%q, want %q: no evidence is published, so nothing is verified", kind, op.ID, c.ID, c.State, CapabilityClaimCandidate)
				}
			}
			if len(claims) == 0 {
				t.Errorf("%s %s is projected with no claims: an operation without claims is unknown and is left out", kind, op.ID)
			}
			if err := workertype.ValidateCapabilityClaims(op.ID, claims); err != nil {
				t.Errorf("%s: %v", kind, err)
			}
		}
	}
}

// TestBuiltInCapabilityClaimsAssertNoEquivalence: no contract identity (id, version,
// profile) is claimed by two operations. Two operations sharing one would say they
// behave alike, which ADR-0483 admits only with evidence for each — and a neutral
// identifier is exactly where such a claim would slip in unnoticed.
func TestBuiltInCapabilityClaimsAssertNoEquivalence(t *testing.T) {
	seen := map[string]string{}
	for kind := range builtInClaimSources {
		for _, op := range builtInOperationClaims(kind) {
			for _, c := range op.Capabilities {
				key := c.ID + "@" + c.Version + "+" + c.Profile
				where := kind + " " + op.ID
				if prev, dup := seen[key]; dup {
					t.Errorf("%s is claimed by both %s and %s: that asserts interchangeability no evidence supports", key, prev, where)
				}
				seen[key] = where
			}
		}
	}
}

// TestBuiltInOperationClaimsAreSortedAndLeaveOutUnclaimed: the projection lists only
// operations that declare a claim, in a stable order, and a Worker Type with no claim
// source projects nothing rather than an empty list.
func TestBuiltInOperationClaimsAreSortedAndLeaveOutUnclaimed(t *testing.T) {
	ops := builtInOperationClaims(connectorKindEntra)
	if len(ops) != len(reviewedBuiltInCapabilityClaims[connectorKindEntra]) {
		t.Fatalf("entra projects %d operations, want only the %d that claim something", len(ops), len(reviewedBuiltInCapabilityClaims[connectorKindEntra]))
	}
	for i := 1; i < len(ops); i++ {
		if strings.Compare(ops[i-1].ID, ops[i].ID) >= 0 {
			t.Fatalf("operations not sorted: %q before %q", ops[i-1].ID, ops[i].ID)
		}
	}
	if got := builtInOperationClaims(connectorKindJira); got != nil {
		t.Fatalf("jira declares no claims and should project none, got %v", got)
	}
}
