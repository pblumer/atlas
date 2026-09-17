package catalog

import (
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// Which products name an approver that reaches nobody.
//
// An approval rule carries a reference, and what that reference has to be
// differs by kind: `fixed` becomes a task's **assignee**, matched against a
// caller's username, and `role` becomes its **candidate groups**, matched against
// group ids first and group names after. Neither match is checked when the rule
// is written, and neither failure is reported when it fires. The approval is
// created, it lands in nobody's inbox, and the order waits — the first person to
// notice is whoever is waiting for the laptop.
//
// The picker on the catalogue screen stops new ones being written. It does nothing
// about the ones already stored, and it shows a broken rule only to somebody who
// happens to open that product. This is the standing list instead, for the same
// reason the release carries one of the products that need no approval at all: the
// role that writes approval rules is the role that should be able to see them.
//
// # What it deliberately does not report
//
// **A rule that reaches somebody.** `candidateGroups` is a comma-separated list
// and a caller matches if *any* entry does, so a list with one dud entry still
// reaches its group. Naming those here would bury the rules that reach nobody
// among rules that work.
//
// **A kind that is not one of the four.** [Approval.Kind] may name a process
// directly, and what that process does with the reference is the process's
// business. There is nothing to check it against.
//
// **A leftover reference on a kind that needs none.** `none` and `superior`
// resolve without one, so a reference beside them is untidy rather than broken.

// ApproverLookup answers whether an approval reference resolves to anybody. It is
// two questions rather than one because the two are matched differently, and the
// difference is the whole reason this report exists.
type ApproverLookup interface {
	// Account reports whether this names an account that can hold a task. Matched
	// the way an assignee is matched: by username, ignoring case.
	Account(username string) bool
	// Group reports whether this names a group, by id or by name, ignoring case —
	// which is the order and the tolerance candidate groups are matched with.
	Group(ref string) bool
}

// ApproverProblem is one product whose approval rule reaches nobody.
type ApproverProblem struct {
	ItemID string `json:"itemId"`
	// HomeCatalog is where the product is maintained, because that is where the
	// reader has to go to correct it.
	HomeCatalog string `json:"homeCatalog"`
	Kind        string `json:"kind"`
	// Ref is what the rule names, echoed exactly. A reference that differs from
	// what its author believes they typed is the ordinary case here, so the report
	// shows the stored string rather than a tidied one.
	Ref string `json:"ref"`
	// Why is written for the person who has to fix it, and names the thing that
	// does not exist rather than the rule that is invalid.
	Why string `json:"why"`
}

// ApproverReport is what one read answers.
type ApproverReport struct {
	// Checked is how many products were examined, so an empty Problems list can be
	// read as "none of 240" rather than as "nothing was looked at".
	Checked  int               `json:"checked"`
	Problems []ApproverProblem `json:"problems"`
}

// approverProblems walks the items and names every rule that reaches nobody.
//
// A pure function over its inputs: the lookup is the only thing that knows about
// accounts and groups, and it is passed in, so this is testable without a store
// and cannot drift towards a second copy of the matching rule.
func approverProblems(items []Item, look ApproverLookup) []ApproverProblem {
	out := []ApproverProblem{}
	for _, it := range items {
		kind, ref := string(it.Approval.Kind), strings.TrimSpace(it.Approval.Ref)
		var why string
		switch ApprovalKind(kind) {
		case KindFixed:
			switch {
			case ref == "":
				why = "needs a named approver and names none"
			case !look.Account(ref):
				why = "names the account " + quoteRef(ref) + ", which no enabled account answers to"
			}
		case KindRole:
			switch {
			case ref == "":
				why = "needs a group and names none"
			case !anyGroupResolves(ref, look):
				why = "names the group " + quoteRef(ref) + ", which is no group here"
			}
		}
		if why == "" {
			continue
		}
		out = append(out, ApproverProblem{
			ItemID: it.ID, HomeCatalog: it.HomeCatalog, Kind: kind, Ref: it.Approval.Ref, Why: why,
		})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ItemID < out[b].ItemID })
	return out
}

// anyGroupResolves mirrors how candidate groups are matched: the entry list is
// comma-separated and a caller passes if any one of them names their group. A rule
// reaches somebody as soon as one entry does.
func anyGroupResolves(ref string, look ApproverLookup) bool {
	for _, want := range strings.Split(ref, ",") {
		if want = strings.TrimSpace(want); want != "" && look.Group(want) {
			return true
		}
	}
	return false
}

func quoteRef(s string) string { return `"` + s + `"` }

// HandleApproverReport answers which of the products this caller maintains name an
// approver that reaches nobody.
//
// Scoped to what the caller may edit, like the product list it reads: the report
// names products and the people who approve them, which is maintenance
// information and not a public index of who approves what.
func (s *Service) HandleApproverReport(w http.ResponseWriter, r *http.Request) {
	// No resolver, no report. The two wrong answers are both available and both
	// worse than a refusal: treating every reference as unresolvable reports the
	// whole catalogue as broken, and treating every one as fine reports a clean
	// estate that nobody checked.
	if s.Approvers == nil {
		httpapi.Error(w, http.StatusServiceUnavailable,
			"approver report: this server cannot resolve accounts or groups, so it cannot say "+
				"which approvers reach nobody")
		return
	}

	p := httpapi.PrincipalFrom(r.Context())
	var (
		mine    []Item
		loadErr error
	)
	s.loop.Do(func() {
		var all []Item
		if all, loadErr = s.store.Items(); loadErr != nil {
			return
		}
		// One lookup per home catalogue rather than per product, as the listing does:
		// a maintainer's products share a handful of homes.
		seen := map[string]bool{}
		for _, it := range all {
			ok, known := seen[it.HomeCatalog]
			if !known {
				cat, found, e := s.store.Catalog(it.HomeCatalog)
				if e != nil {
					loadErr = e
					return
				}
				ok = found && s.mayEdit(cat, p)
				seen[it.HomeCatalog] = ok
			}
			if ok {
				mine = append(mine, it)
			}
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "approver report: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, ApproverReport{
		Checked: len(mine), Problems: approverProblems(mine, s.Approvers),
	})
}
