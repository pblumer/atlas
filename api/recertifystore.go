package api

import (
	"sort"

	"github.com/pblumer/atlas/api/sidecar"
)

// The durable record of what somebody attested about somebody else's access
// (ADR-draft-access-recertification).
//
// # Why a sidecar, and why two of them
//
// A sidecar for the reason the discrepancy journal is one: no process writes an
// attestation, nothing replays it, and applyToState has no business with it. What
// it records is a judgement a person made at a moment, and that is not the engine's
// to reproduce.
//
// Two stores rather than one, and this is arithmetic rather than taste. A campaign
// header is written twice — opened, closed — and there are a handful of them. A row
// is written once per decision and there may be thousands in one campaign. Keeping
// the rows inside the campaign document would rewrite the whole campaign on every
// single decision: a five-thousand-row campaign answered to the end would write its
// own size five thousand times over, which is gigabytes to record a few kilobytes
// of judgements. One record per row makes each decision cost one row.
//
// The price of the split, stated rather than discovered: a campaign and its rows
// are two writes and nothing makes them one. A campaign whose header is written and
// whose rows are not is possible after a crash, and it reads as a campaign with no
// questions in it — visible, wrong in the harmless direction, and re-openable.

// recertifyCampaign is one campaign's header. Rows travel separately and are
// attached when a campaign is read whole.
type recertifyCampaign struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	OpenedBy string `json:"openedBy"`
	OpenedAt int64  `json:"openedAt"`
	DueAt    int64  `json:"dueAt,omitempty"`

	// ClosedAt and ClosedBy are set when somebody declares the campaign over. A
	// campaign closes with undecided rows still in it — that is the normal ending
	// and the number is the point, so closing is a statement about the campaign and
	// never about the rows.
	ClosedAt int64  `json:"closedAt,omitempty"`
	ClosedBy string `json:"closedBy,omitempty"`

	// Items and Principals are the scope as the caller wrote it, echoed so a report
	// read later says what was asked. Empty means the whole inventory.
	Items      []string `json:"items,omitempty"`
	Principals []string `json:"principals,omitempty"`

	// Rows is populated when a campaign is read, never stored on this record.
	Rows []recertifyRow `json:"rows,omitempty"`

	UpdatedAt int64 `json:"updatedAt"`
}

// Open reports whether this campaign still takes decisions.
func (c recertifyCampaign) Open() bool { return c.ClosedAt == 0 }

// recertifyRow is one question and, eventually, one answer.
//
// Everything above the decision is frozen at the moment the campaign was built.
// That is the whole point of storing it rather than resolving it when the row is
// rendered: what is being attested is what the reviewer was shown, and a row that
// re-read the inventory would show a later reader a different question from the one
// that was answered.
type recertifyRow struct {
	ID         string `json:"id"`
	CampaignID string `json:"campaignId"`

	Principal string `json:"principal"`
	ItemID    string `json:"itemId"`
	VariantID string `json:"variantId,omitempty"`

	// OrderID, Origin and Since are what makes the question answerable. An
	// `ordered` right two weeks old and a `legacy` one nobody can trace are the same
	// row without them, and they call for completely different judgements.
	OrderID string `json:"orderId,omitempty"`
	Origin  string `json:"origin,omitempty"`
	Since   int64  `json:"since,omitempty"`

	// Disputed marks a right an open finding concerns: the inventory and the target
	// system currently disagree about it. Certifying one is signing a statement
	// about something contested, so the reviewer is told before they answer.
	Disputed    bool   `json:"disputed,omitempty"`
	DisputeKind string `json:"disputeKind,omitempty"`
	DisputeID   string `json:"disputeId,omitempty"`

	// Reviewer is who was asked. Empty is "nobody", which is a real answer rather
	// than an error, and makes the row the campaign owner's.
	Reviewer string `json:"reviewer,omitempty"`

	// Decision is empty until somebody answers, and empty is *undecided* — never a
	// synonym for keep and never one for revoke.
	Decision  string `json:"decision,omitempty"`
	DecidedAt int64  `json:"decidedAt,omitempty"`
	DecidedBy string `json:"decidedBy,omitempty"`
	// Note is what the person wrote, if anything. It is the only free text in the
	// record and the only part of it a reader can learn something from that the
	// numbers do not carry.
	Note string `json:"note,omitempty"`
	// Outcome says what a revoke actually did.
	Outcome string `json:"outcome,omitempty"`

	UpdatedAt int64 `json:"updatedAt"`
}

// Decided reports whether this row carries an answer.
func (r recertifyRow) Decided() bool { return r.Decision != "" }

// recertifyStore is the durable home of campaigns and their rows.
type recertifyStore struct {
	campaigns *sidecar.Store[recertifyCampaign]
	rows      *sidecar.Store[recertifyRow]
}

func newRecertifyStore(campaignDir, rowDir string) (*recertifyStore, error) {
	c, err := sidecar.NewStore(campaignDir, "recertifycampaignstore",
		func(rec recertifyCampaign) string { return rec.ID })
	if err != nil {
		return nil, err
	}
	// A row is addressed by campaign and row together. The two are already unique
	// apart, and joining them keeps a row's file next to nothing but its own
	// campaign's — the sidecar store hex-encodes keys, so the separator is a
	// separator and not a filename question.
	rw, err := sidecar.NewStore(rowDir, "recertifyrowstore",
		func(rec recertifyRow) string { return rec.CampaignID + ":" + rec.ID })
	if err != nil {
		return nil, err
	}
	return &recertifyStore{campaigns: c, rows: rw}, nil
}

// saveCampaign writes a header and every row it carries.
//
// The rows go first. A header written before its rows would, if the process died
// between them, leave a campaign that looks answerable and has nothing to answer —
// and somebody would close it as complete. The other order leaves rows nothing
// points at, which no screen shows and the next open supersedes.
func (s *recertifyStore) saveCampaign(cmp recertifyCampaign) error {
	for _, row := range cmp.Rows {
		if err := s.rows.Save(row); err != nil {
			return err
		}
	}
	header := cmp
	header.Rows = nil
	return s.campaigns.Save(header)
}

// campaign reads one campaign with its rows attached, in the order they were built.
func (s *recertifyStore) campaign(id string) (recertifyCampaign, bool, error) {
	cmp, found, err := s.campaigns.Get(id)
	if err != nil || !found {
		return recertifyCampaign{}, found, err
	}
	all, err := s.rows.LoadAll()
	if err != nil {
		return recertifyCampaign{}, false, err
	}
	rows := make([]recertifyRow, 0, len(all))
	for _, r := range all {
		if r.CampaignID == id {
			rows = append(rows, r)
		}
	}
	sortRows(rows)
	cmp.Rows = rows
	return cmp, true, nil
}

// row reads one row of one campaign.
func (s *recertifyStore) row(campaignID, id string) (recertifyRow, bool, error) {
	return s.rows.Get(campaignID + ":" + id)
}

// listCampaigns answers what campaigns exist, newest first.
func (s *recertifyStore) listCampaigns() ([]recertifyCampaign, error) {
	all, err := s.campaigns.LoadAll()
	if err != nil {
		return nil, err
	}
	// Newest first, and the id breaks a tie. Opening is recorded in whole seconds,
	// so two campaigns opened in the same second are the same age — and a list whose
	// order then came from whatever the filesystem returned would be a different
	// list on every read.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].OpenedAt != all[j].OpenedAt {
			return all[i].OpenedAt > all[j].OpenedAt
		}
		return all[i].ID < all[j].ID
	})
	return all, nil
}

// decide records one answer on one row.
//
// It refuses a row that already carries one. A campaign is a moment and an
// attestation is evidence of that moment; letting a decision be overwritten would
// make the record answer "what did they decide" with whatever was written last,
// which is the one question it exists to answer truthfully. Changing one's mind is
// a new campaign, or an order — both of which leave their own evidence.
func decideRow(row recertifyRow, decision, by, note, outcome string, now int64) recertifyRow {
	next := row
	next.Decision, next.DecidedBy, next.DecidedAt = decision, by, now
	next.Note, next.Outcome = note, outcome
	next.UpdatedAt = now
	return next
}
