package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/taskfolder"
	"github.com/pblumer/atlas/state"
)

// What is waiting for one person (ADR-0343).
//
// # Why this route has to exist at all
//
// Atlas can already send mail: a modelled process carries a mail task, `to=` names
// a principal or a group, and api/maildirectory.go resolves the address in the
// server at the moment of sending, so no address ever enters a variable, an order
// or the log (ADR-0314). What was missing is the other half — every route that
// answers "what is waiting" answers only *for the caller*. `GET /api/v1/approvals`
// is the approvals addressed to you; a campaign read with `?mine=true` is the rows
// you may answer. A reminder process is not the person it is reminding, so there
// was nothing it could ask.
//
// # The one hard problem: an interruption you cannot act on
//
// A reminder's whole value rests on being right. One about work that is not yours,
// or that you already did, does not waste a minute — it teaches the reader that
// these messages are noise, and the next one, the one that mattered, is deleted
// unread. So nothing is listed here that the person cannot act on right now: not a
// row in a closed campaign, not a row somebody already decided, not an approval
// that has escalated away.

// pendingItem is one thing waiting for somebody, in the words a reminder can use.
type pendingItem struct {
	// Kind is what sort of thing it is, so a message can group them and a reader
	// can tell an approval from a review without parsing the sentence.
	Kind string `json:"kind"`
	// Ref and Sub identify it: an order and its line, or a campaign and its row.
	Ref string `json:"ref"`
	Sub string `json:"sub,omitempty"`
	// What is one line a person reads. It names the product and the people, because
	// "you have an approval waiting" is a sentence that sends somebody looking.
	What string `json:"what"`
	// Since is when it started waiting, in unix seconds where the source keeps
	// seconds and zero where it keeps nothing. A reminder that can say "since
	// Tuesday" is a different message from one that cannot.
	Since int64 `json:"since,omitempty"`
	// Link is a **relative** path into the portal, and relative on purpose: a model
	// already receives `portalBaseUrl` when an order starts its fulfilment, and
	// deciding the origin here would put whichever host this server happened to be
	// reached on into a mail somebody outside may not resolve.
	Link string `json:"link"`
}

// pendingCounts is the answer a reminder reads first.
//
// Counted as well as listed because the first decision a reminder makes is whether
// to send at all, and a process that has to fetch a list to discover it is empty
// sends a mail to say nothing rather often.
type pendingCounts struct {
	Approvals        int `json:"approvals"`
	Recertifications int `json:"recertifications"`
	Total            int `json:"total"`
}

// pendingWork is what one person owes.
type pendingWork struct {
	// Principal is who this is about, echoed so a report read later — or a mail
	// assembled from it — cannot be about somebody else by accident.
	Principal string        `json:"principal"`
	Counts    pendingCounts `json:"counts"`
	Items     []pendingItem `json:"items"`
	// Omitted says the list was cut for reading; the counts are over everything.
	Omitted int `json:"omitted,omitempty"`
}

// handlePendingWork answers what is waiting for the caller, or — for an operator —
// for somebody else.
//
// The split is the point of the whole record. Every signed-in person may ask what
// is waiting for them, which is what the existing per-subsystem routes already
// allow. Asking about somebody else is the capability a reminder needs and the one
// a person must not have: a portal where any user can enumerate any other user's
// pending approvals has turned an inbox into an organisation chart with workloads
// attached.
func (s *Server) handlePendingWork(w http.ResponseWriter, r *http.Request) {
	pr := httpapi.PrincipalFrom(r.Context())

	asked := strings.TrimSpace(r.URL.Query().Get("principal"))
	subject := pr
	switch {
	case asked == "":
		// The caller's own. Nothing to resolve and nothing to authorise.
	case !s.mayAskAboutOthers(pr):
		httpapi.Error(w, http.StatusForbidden,
			"asking what is waiting for somebody else is the operator's. Everybody may ask "+
				"what is waiting for them, which is this route without \"principal\"")
		return
	default:
		var err error
		if subject, err = s.principalOf(asked); err != nil {
			code, msg := principalRefusal(err)
			httpapi.Error(w, code, msg)
			return
		}
	}
	if subject == nil && s.authEnabled {
		httpapi.Error(w, http.StatusUnauthorized, "no principal on this request")
		return
	}

	out := pendingWork{Items: []pendingItem{}}
	if subject != nil {
		out.Principal = subject.UserID
	}

	approvals, err := s.approvalsWaitingFor(subject)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "pending work: "+err.Error())
		return
	}
	reviews, err := s.reviewsWaitingFor(out.Principal)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "pending work: "+err.Error())
		return
	}

	out.Counts = pendingCounts{
		Approvals: len(approvals), Recertifications: len(reviews),
		Total: len(approvals) + len(reviews),
	}
	out.Items = append(append(out.Items, approvals...), reviews...)

	// Oldest first: what has waited longest is what a reminder should lead with,
	// and a list a person works down should start where the delay is.
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].Since < out.Items[j].Since })
	if limit := int(s.budgets().PendingWorkItems); len(out.Items) > limit {
		out.Omitted = len(out.Items) - limit
		out.Items = out.Items[:limit]
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// mayAskAboutOthers is the authority for the second mode.
func (s *Server) mayAskAboutOthers(pr *httpapi.Principal) bool {
	if !s.authEnabled {
		return true
	}
	if pr == nil {
		return false
	}
	for _, role := range pr.Roles {
		if role == RoleAdmin || role == RoleOperator {
			return true
		}
	}
	return false
}

// principalFor builds the principal of somebody who is not calling.
//
// It assembles exactly what a login snapshots — the id, the username and the group
// ids — because the question being asked is "what would this person see", and an
// answer computed from anything else would diverge from what they actually hold.
// Roles are deliberately left empty: nothing downstream of here reads them, and
// filling them in would make this look like a way to act as somebody.
// principalRefusal says how a failed resolution is answered.
//
// A name nobody holds is the caller's mistake: 404, in the resolver's own words,
// which already name the four spellings that resolve. An unreadable user store is
// not, and was reading as one — "no such person" is a definite answer, and giving
// it from a store that could not be read tells an operator their colleague has no
// account when what happened is that Atlas could not look.
//
// It is a function and not two lines in the handler so that both arms can be
// stated in a test. The arm that matters is the one no HTTP test can reach: a
// store that fails to load is not something a request can bring about.
func principalRefusal(err error) (int, string) {
	if errors.Is(err, httpapi.ErrNoSuchPrincipal) {
		return http.StatusNotFound, err.Error()
	}
	return http.StatusInternalServerError, "resolve principal: " + err.Error()
}

func (s *Server) principalOf(who string) (*httpapi.Principal, error) {
	var (
		out   *httpapi.Principal
		ran   bool
		opErr error
	)
	s.do(func() {
		ran = true
		users, err := s.users.LoadAll()
		if err != nil {
			opErr = err
			return
		}
		u, ok := holderResolver(users)(who)
		if !ok {
			// By username too, which holderResolver does not accept: it exists for
			// the target systems' vocabulary, and this question is asked in Atlas's.
			for _, c := range users {
				if strings.EqualFold(c.Username, who) {
					u, ok = c, true
					break
				}
			}
		}
		if !ok {
			opErr = fmt.Errorf("%w: no account %q; name somebody by principal id, "+
				"username, directory id or mail address", httpapi.ErrNoSuchPrincipal, who)
			return
		}
		p := &httpapi.Principal{UserID: u.ID, Username: u.Username}
		groups, err := s.groups.LoadAll()
		if err != nil {
			opErr = err
			return
		}
		for _, g := range groups {
			for _, m := range g.Members {
				if m == u.ID {
					p.GroupIDs = append(p.GroupIDs, g.ID)
					break
				}
			}
		}
		out = p
	})
	if !ran {
		return nil, fmt.Errorf("this server is shutting down")
	}
	return out, opErr
}

// approvalsWaitingFor gathers the open approvals addressed to somebody.
//
// The same two passes handleListApprovals makes, and the same ownership test, so
// the two can never disagree about whose approval something is — a reminder about
// an approval the person then cannot open would be the exact failure this record is
// written against.
func (s *Server) approvalsWaitingFor(pr *httpapi.Principal) ([]pendingItem, error) {
	type candidate struct {
		key uint64
		tr  taskResp
	}
	var held []candidate
	if _, err := s.visitOpenTasks(0, false, func(jobKey uint64, tr taskResp, _ taskfolder.Task) bool {
		if s.holdsApproval(pr, tr) {
			held = append(held, candidate{key: jobKey, tr: tr})
		}
		return true
	}); err != nil {
		return nil, err
	}

	out := []pendingItem{}
	err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		for _, c := range held {
			a, ok, err := s.approvalOf(rv, c.tr)
			if err != nil {
				return err
			}
			if !ok {
				continue // a task of this person's that is not an approval
			}
			out = append(out, pendingItem{
				Kind: "approval", Ref: a.OrderID, Sub: a.ItemID,
				What:  approvalSentence(a),
				Since: waitingSince(a),
				// Into the inbox, which is where an approval is read and decided
				// (ADR-draft-approval-in-the-inbox). It names the
				// order line rather than the task, because a task key does not exist
				// until the task activates and the order line does.
				Link: "/index.html#/tasks?order=" + a.OrderID + "&item=" + a.PositionID,
			})
		}
		return nil
	})
	return out, err
}

// reviewsWaitingFor gathers the recertification rows somebody still owes.
//
// Undecided rows of open campaigns only, and that is the record's rule rather than
// a filter: a row in a closed campaign is not waiting for anybody, and one somebody
// already answered is not either. Either would produce a perfectly formatted
// message about nothing.
func (s *Server) reviewsWaitingFor(principal string) ([]pendingItem, error) {
	if principal == "" {
		return []pendingItem{}, nil
	}
	var (
		campaigns []recertifyCampaign
		ran       bool
		opErr     error
	)
	s.do(func() {
		ran = true
		campaigns, opErr = s.recertifications.listCampaigns()
	})
	if !ran {
		return nil, fmt.Errorf("this server is shutting down")
	}
	if opErr != nil {
		return nil, opErr
	}

	out := []pendingItem{}
	for _, c := range campaigns {
		if !c.Open() {
			continue
		}
		var (
			full  recertifyCampaign
			found bool
			err   error
		)
		s.do(func() { full, found, err = s.recertifications.campaign(c.ID) })
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		for _, row := range full.Rows {
			if row.Decided() {
				continue
			}
			// The same rule mayDecideRow applies, minus the operator shortcut: an
			// operator may answer any row, and reminding them of every row in the
			// estate is how a reminder becomes something nobody reads.
			switch {
			case row.Reviewer != "" && row.Reviewer != principal:
				continue
			case row.Reviewer == "" && full.OpenedBy != principal:
				continue
			}
			out = append(out, pendingItem{
				Kind: "recertification", Ref: c.ID, Sub: row.ID,
				What:  reviewSentence(full, row),
				Since: c.OpenedAt,
				Link:  "/#/tasks/recertification",
			})
		}
	}
	return out, nil
}

// waitingSince is when this approval started waiting for *this* person, which is
// not when the order was placed.
//
// An approval that escalated to somebody yesterday has waited a day, not a
// fortnight, and telling them otherwise is the kind of small wrongness that makes
// the whole message suspect. So the last escalation wins, the first assignment is
// the answer while it has not moved, and zero is the honest answer for an approval
// carrying no assignment record at all — nothing here invents an age.
func waitingSince(a approvalResp) int64 {
	if a.Assignment == nil {
		return 0
	}
	if n := len(a.Assignment.Escalations); n > 0 {
		return a.Assignment.Escalations[n-1].At / 1e9
	}
	return a.Assignment.AssignedAt / 1e9
}

// productWords is the ordered product in words rather than as an id, where the
// release froze any. An approver deciding "vpn-zugang" is reading an id.
//
// Any language, deterministically: the mail this ends up in is addressed to one
// person whose language nothing here knows, and picking the first by name at least
// produces the same sentence twice rather than whichever the map iterated to.
func productWords(a approvalResp) string {
	if len(a.Texts) == 0 {
		return a.ItemID
	}
	langs := make([]string, 0, len(a.Texts))
	for l := range a.Texts {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	if t := strings.TrimSpace(a.Texts[langs[0]]); t != "" {
		return t
	}
	return a.ItemID
}

// approvalSentence is the one line a reminder can put in a mail.
func approvalSentence(a approvalResp) string {
	what := productWords(a)
	who := a.Recipient
	if who == "" {
		who = a.Orderer
	}
	if who == "" {
		return "An order for " + what + " is waiting for your decision"
	}
	return "An order of " + what + " for " + who + " is waiting for your decision"
}

// reviewSentence is the same for a review, and it names the campaign because that
// is what the person was told about.
func reviewSentence(c recertifyCampaign, row recertifyRow) string {
	line := "Access review " + c.Name + ": does " + row.Principal + " still need " + row.ItemID + "?"
	if row.Disputed {
		line += " (a reconciliation finding stands against it)"
	}
	return line
}
