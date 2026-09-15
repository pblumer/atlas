package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/pblumer/atlas/model"
)

// What a recertification campaign asks, and what it refuses to assume
// (ADR-0341).
//
// Three questions can be asked about somebody's access. Ordering answers *may they
// have it*; reconciliation answers *do they actually have it*; this answers *do
// they still need it* — and until now nothing asked it at all. It is not the
// smaller sibling of the other two: a right that was properly approved, properly
// provisioned and is properly recorded can still be wrong, and in most estates
// that is the dominant way wrong access accumulates. People change roles and keep
// what the old one needed. Nobody granted anything improperly, and nobody removed
// anything either, because removing is somebody's job and it is nobody's job.
//
// # The one hard problem
//
// A campaign that shows four hundred rows and a "certify all" button produces a
// signed attestation containing no information — worse than none, because an
// auditor reading it has evidence that somebody looked. Everything here follows
// from refusing that:
//
//   - **No bulk decision.** One row, one call, one person. The reading is the
//     product, so nothing that skips the reading may exist.
//   - **Silence is never a decision.** A row nobody answered is *undecided*, and a
//     campaign says so. "Silence means keep" invents an attestation nobody gave;
//     "silence means revoke" locks people out because a manager took holiday.
//   - **The row carries what the reviewer was shown**, frozen. A row that resolved
//     its own facts at render time would show a later reader a different question
//     from the one that was answered.

// The decisions a row can carry. Empty is the fourth state and the important one:
// undecided, which is not a synonym for either.
const (
	decisionKeep   = "keep"
	decisionRevoke = "revoke"
)

// What a revoke actually did. It is recorded because the two are different facts
// about the same decision, and only one of them started anything.
const (
	// outcomeDeprovisioning is the ordinary case: the product's own process was
	// started. The row records that a person decided, not that the target system
	// has caught up — the process's outcome is the process's to report.
	outcomeDeprovisioning = "deprovisioning-started"
	// outcomeAlreadyGone is a revoke arriving after the right had already gone.
	// The campaign is a snapshot and the estate moves under it. The decision is
	// still a decision somebody made and is recorded as one; starting a
	// deprovisioning against nothing would raise an incident about a fact rather
	// than a fault.
	outcomeAlreadyGone = "already-gone"
)

// recertifyOpen is the message that opens a campaign.
type recertifyOpen struct {
	// Name is what a reader calls this campaign a year later. Required: a campaign
	// identified only by an id is one nobody can cite.
	Name string `json:"name"`

	// Items and Principals narrow what is certified. Both empty means the whole
	// inventory, and that is a legitimate answer here rather than the dangerous
	// default it would be in a reconciliation: this endpoint concludes nothing from
	// absence. It builds a list of questions, and a question about every right in
	// the estate is a big campaign, not a wrong one.
	Items      []string `json:"items,omitempty"`
	Principals []string `json:"principals,omitempty"`

	// Reviewers names who answers for whom: principal id (or directory id, or mail)
	// of the holder, to the principal of the person who reviews their rights.
	//
	// Atlas does not derive it. api/escalation.go settled that argument for the
	// line-manager lookup: it is a directory question, and directory questions
	// belong to a modelled process — the `superior` approval rule already works
	// this way. So the process that opens a campaign resolves the managers and
	// names them here.
	//
	// A holder this map does not name is not an error. The row is unassigned, which
	// is the answer "nobody", and it lands with the campaign's owner rather than
	// stopping the campaign: one person with no manager in the directory must not
	// prevent the recertification of an estate.
	Reviewers map[string]string `json:"reviewers,omitempty"`

	// DueAt is advisory and nothing acts on it. A deadline that revoked what nobody
	// answered would take access away because somebody was on holiday, and a
	// deadline that certified it would invent the signature this whole record
	// exists to prevent. It is a date the screen can show and a report can count
	// against.
	DueAt int64 `json:"dueAt,omitempty"`
}

// recertifyInput is everything a campaign is built from, gathered before any of it
// is decided.
type recertifyInput struct {
	Users []User
	Held  []model.EntitlementValue
	// Disputes are the open findings, keyed by principal and item. A right two
	// systems currently disagree about is a right whose certification would be a
	// signature on a contested statement.
	Disputes map[string]discrepancyRecord
}

// rowID is stable for a campaign and a pair, so a decision cannot be applied to
// the wrong row by a retry and a row cannot appear twice.
func rowID(campaign, principal, itemID, variantID string) string {
	sum := sha256.Sum256([]byte(campaign + "\x00" + principal + "\x00" + itemID + "\x00" + variantID))
	return "row_" + hex.EncodeToString(sum[:8])
}

// campaignID is derived from the name and the moment, for the same reason:
// reopening the same campaign name next quarter must be a different campaign.
//
// The moment is in **nanoseconds** even though the campaign records its opening in
// seconds, and that is not an inconsistency to tidy up. Two campaigns of the same
// name opened in the same second would otherwise be one id, and the second would
// overwrite the first — a campaign's rows replaced by another's, which is evidence
// destroyed by a name collision and a clock too coarse to see it.
func campaignID(name string, atNanos int64) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d", name, atNanos))
	return "cmp_" + hex.EncodeToString(sum[:8])
}

// disputeKey is how a row finds the finding that concerns it.
func disputeKey(principal, itemID string) string { return principal + "\x00" + itemID }

// buildCampaign turns the inventory into the questions a campaign asks.
//
// It reads and decides nothing else: the rows are frozen here, at one moment, and
// every fact a reviewer will see is written into them now. That is the snapshot the
// record argues for — what is being attested is what the person saw, and a row that
// re-resolved its origin or its dispute at render time would quietly change the
// question between being asked and being answered.
func buildCampaign(msg recertifyOpen, in recertifyInput, by string, now, atNanos int64) recertifyCampaign {
	id := campaignID(msg.Name, atNanos)

	// The scope, as sets. Empty means "everything", which is stated rather than
	// implied: a campaign over the whole inventory is a big campaign and not a
	// mistake, because nothing here concludes anything from what it does not see.
	wantItem := map[string]bool{}
	for _, raw := range msg.Items {
		if it := strings.TrimSpace(raw); it != "" {
			wantItem[it] = true
		}
	}

	// Principals and reviewers are named in whatever the caller had to hand — a
	// principal id, a directory id, a mail address. A process that knows people by
	// their directory id should not have to learn Atlas's ids to ask a question
	// about them.
	//
	// Deliberately a wider net than a reconciliation's subject resolution, which
	// accepts a directory id or a mail address and never an Atlas principal id.
	// That narrowness is right there: an observation's subject is a fact in the
	// *target system's* vocabulary, and accepting an Atlas id would accept a
	// statement the target system could not have made. Here the caller is asking
	// Atlas a question about its own records, so an Atlas id is a legitimate way to
	// ask it.
	resolve := holderResolver(in.Users)

	wantPrincipal := map[string]bool{}
	for _, raw := range msg.Principals {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if u, ok := resolve(p); ok {
			wantPrincipal[u.ID] = true
			continue
		}
		// Unresolvable, and kept as written. A campaign naming somebody Atlas has
		// never heard of produces no rows for them, which the report says out loud —
		// silently dropping the name would report "nothing to certify" for a person
		// whose name was simply misspelled.
		wantPrincipal[p] = true
	}

	reviewerOf := map[string]string{}
	for holder, reviewer := range msg.Reviewers {
		h, rv := strings.TrimSpace(holder), strings.TrimSpace(reviewer)
		if h == "" || rv == "" {
			continue
		}
		key := h
		if u, ok := resolve(h); ok {
			key = u.ID
		}
		if u, ok := resolve(rv); ok {
			rv = u.ID
		}
		reviewerOf[key] = rv
	}

	cmp := recertifyCampaign{
		ID: id, Name: strings.TrimSpace(msg.Name),
		OpenedBy: by, OpenedAt: now, DueAt: msg.DueAt,
		Items: append([]string{}, msg.Items...), Principals: append([]string{}, msg.Principals...),
		UpdatedAt: now,
	}

	rows := make([]recertifyRow, 0, len(in.Held))
	for _, v := range in.Held {
		if len(wantItem) > 0 && !wantItem[v.ItemID] {
			continue
		}
		if len(wantPrincipal) > 0 && !wantPrincipal[v.Principal] {
			continue
		}
		row := recertifyRow{
			ID: rowID(id, v.Principal, v.ItemID, v.VariantID), CampaignID: id,
			Principal: v.Principal, ItemID: v.ItemID, VariantID: v.VariantID,
			OrderID: v.OrderID, Origin: originName(v.Origin), Since: v.Since,
			Until: v.Until, Reviewer: reviewerOf[v.Principal], UpdatedAt: now,
		}
		if d, disputed := in.Disputes[disputeKey(v.Principal, v.ItemID)]; disputed {
			// Marked, never refused. One open discrepancy must not block a campaign
			// over thousands of rights, and the reviewer of this row may well know
			// exactly what happened. What they must not do is answer without being
			// told that two systems currently disagree about it.
			row.Disputed, row.DisputeKind, row.DisputeID = true, d.Kind, d.ID
		}
		rows = append(rows, row)
	}
	sortRows(rows)
	cmp.Rows = rows
	return cmp
}

// originName renders an origin for a row. It is written as text rather than kept
// as the enum because a row is evidence read years later, and a number whose
// meaning lives in a Go constant is evidence nobody can read.
func originName(o model.EntitlementOrigin) string {
	switch o {
	case model.OriginOrdered:
		return "ordered"
	case model.OriginAdopted:
		return "adopted"
	case model.OriginLegacy:
		return "legacy"
	default:
		return ""
	}
}

// sortRows puts a campaign in the order somebody works through it: by reviewer, so
// one person's questions are together, then by holder, then by product. Stable, so
// two rows that tie stay as the inventory walk found them.
func sortRows(rows []recertifyRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch {
		case a.Reviewer != b.Reviewer:
			return a.Reviewer < b.Reviewer
		case a.Principal != b.Principal:
			return a.Principal < b.Principal
		default:
			return a.ItemID < b.ItemID
		}
	})
}

// recertifyCounts is a campaign by state, and the reason `Undecided` is a first
// class number rather than a subtraction.
//
// A progress bar that reads "83% complete" invites the reader to treat the
// remainder as work in hand. These four say what is actually known: somebody
// certified this many, somebody withdrew this many, nobody has answered this many,
// and this many of them were never addressed to anybody at all.
type recertifyCounts struct {
	Rows       int `json:"rows"`
	Kept       int `json:"kept"`
	Revoked    int `json:"revoked"`
	Undecided  int `json:"undecided"`
	Unassigned int `json:"unassigned"`
	Disputed   int `json:"disputed"`
	// Ending counts the rows whose right ends by itself
	// (ADR-draft-time-bounded-entitlements). It is the one count here that is good
	// news: those questions did not need to be asked, and the number says how much
	// of the campaign a ceiling on the product would have removed.
	Ending int `json:"ending"`
}

func countRows(rows []recertifyRow) recertifyCounts {
	c := recertifyCounts{Rows: len(rows)}
	for _, r := range rows {
		switch r.Decision {
		case decisionKeep:
			c.Kept++
		case decisionRevoke:
			c.Revoked++
		default:
			c.Undecided++
		}
		if r.Reviewer == "" {
			c.Unassigned++
		}
		if r.Disputed {
			c.Disputed++
		}
		if r.Until != 0 {
			c.Ending++
		}
	}
	return c
}

// holderResolver finds an account by whichever identifier the caller had: the
// principal id Atlas assigned, the directory object id the mirror follows, or the
// mail address a human would type.
//
// The order matters where they could collide. A principal id is Atlas's own and
// cannot be anything else, so it wins; a mail address is the one a person is most
// likely to mistype, so it loses.
func holderResolver(users []User) func(string) (User, bool) {
	byID, byDirectory, byEmail := map[string]User{}, map[string]User{}, map[string]User{}
	for _, u := range users {
		byID[u.ID] = u
		if u.DirectoryID != "" {
			byDirectory[u.DirectoryID] = u
		}
		if u.Email != "" {
			byEmail[strings.ToLower(u.Email)] = u
		}
	}
	return func(who string) (User, bool) {
		if u, ok := byID[who]; ok {
			return u, true
		}
		if u, ok := byDirectory[who]; ok {
			return u, true
		}
		u, ok := byEmail[strings.ToLower(who)]
		return u, ok
	}
}
