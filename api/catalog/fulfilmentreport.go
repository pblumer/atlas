package catalog

import (
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// Which offered services park forever, because the path from the order to the
// work is not one this installation can walk.
//
// A catalogue binds a product to processes by name. Nothing checks those names
// when the binding is written, nothing checks them when the catalogue is
// published, and — this is the part that hides — nothing complains when an order
// reaches one. A token parked on a job type no worker pulls raises no incident:
// it is not a failure, it is work waiting, and waiting is what a healthy queue
// also looks like. The order sits at "Wartet", and the first person to notice is
// whoever is waiting for the laptop.
//
// It is the approver report's sibling, one stage further along. That one asks
// whether the rule reaches a person; this one asks whether the work reaches a
// worker. Both exist because the same thing is true of both: the failure is
// silent, it is invisible on every screen the maintainer has, and it is discovered
// by the person it was supposed to serve.
//
// # The path this walks
//
// An order does not go straight to the product's process. It goes through the
// fulfilment orchestration, and — where the rule says so — through an approval
// process, and only then to provisioning. Any of the three stops it, so a report
// that checked only the product's own binding would call an installation healthy
// while every order on it stood still. That is the case this was written from:
// fourteen orders held on the orchestration's first service task, every product
// bound correctly, nothing red anywhere.
//
// # What it reads
//
// The newest release of each catalogue the caller maintains, because that is what
// can be ordered today. A live edit nobody has published cannot park an order, and
// a release that is superseded cannot receive one.
//
// # What it deliberately does not report
//
// **A job type a person works.** A user task waits for somebody by design, and
// naming every one of them would bury the queues nobody serves.
//
// **A process named by a kind this installation defines.** [Approval.Kind] may
// name an approval process directly. That name is checked like any other — it is
// either deployed or it is not — but nothing here knows what it was meant to be.
//
// **The shared path, once per product.** The orchestration is one fact about the
// whole installation; repeated against ten products it would bury the ten.

// ProcessLookup answers what the engine knows about the processes a catalogue
// names. Two questions, because a binding fails in two different ways and the
// difference is what the reader has to act on: a process that was never deployed
// is a name to correct, and a deployed process with a job type nobody pulls is a
// worker to start.
type ProcessLookup interface {
	// JobTypesOf returns the job types the tasks of this process id carry, and
	// whether any executable definition carries that id at all. The newest
	// deployed version answers, because that is the one an instance starts on.
	JobTypesOf(processID string) (jobTypes []string, deployed bool)
	// Served reports whether anything works that job type: the engine itself, a
	// worker seen pulling it, or — for a user task — a person.
	Served(jobType string) bool
}

// FulfilmentProblem is one thing that would stop an order, named where the reader
// has to go to fix it.
type FulfilmentProblem struct {
	// ItemID and HomeCatalog are empty for a problem on the shared path, which
	// belongs to no single product and stops every order alike.
	ItemID      string `json:"itemId,omitempty"`
	HomeCatalog string `json:"homeCatalog,omitempty"`
	// Stage says where on the path this sits: "order", "approval", "provision" or
	// "deprovision". It is what tells a reader whether nothing can be ordered at
	// all or only this one service cannot be given back.
	Stage string `json:"stage"`
	// ProcessID is the process the binding names, echoed exactly.
	ProcessID string `json:"processId"`
	// JobType is set where the process is deployed and one of its job types is
	// unserved. Empty where the process itself is missing.
	JobType string `json:"jobType,omitempty"`
	// Why is written for the person who has to fix it and names the thing that is
	// absent, not the binding that is invalid.
	Why string `json:"why"`
}

// FulfilmentReport is what one read answers.
type FulfilmentReport struct {
	// Checked is how many offered services were examined, so an empty Problems
	// list reads as "none of 10" rather than as "nothing was looked at".
	Checked int `json:"checked"`
	// Releases names the releases the products came from, so a reader can tell
	// whether the report is about what they just published.
	Releases []string            `json:"releases"`
	Problems []FulfilmentProblem `json:"problems"`
}

// OrderProcess is the orchestration every order goes through. It is named here
// rather than imported so this package does not depend on the order package for
// one string; the guard in fulfilmentreport_test.go holds the two together.
const OrderProcess = "atlas-auftrag-erfuellung"

// approvalProcessOf mirrors order.Line.ApprovalProcess: the three built-in kinds
// map to Atlas's own models, and any other kind names a process directly.
//
// Duplicated deliberately rather than imported, and held to the original by a
// guard: this package is the catalogue's and the order package already depends on
// it, so importing back would be a cycle.
var approvalProcessOf = map[string]string{
	"fixed":    "atlas-genehmigung-fix",
	"role":     "atlas-genehmigung-rolle",
	"superior": "atlas-genehmigung-vorgesetzter",
}

// fulfilmentProblems walks the offered services and names every binding that
// cannot be walked.
//
// A pure function over its inputs, like approverProblems: the lookup is the only
// thing that knows about deployments and workers, and it is passed in, so this is
// testable without an engine and cannot drift into a second copy of the rule.
func fulfilmentProblems(items []Item, look ProcessLookup) []FulfilmentProblem {
	out := []FulfilmentProblem{}
	// The shared path first, and once. Every order goes through it, so a problem
	// here is the answer to "why is nothing moving" and belongs at the top.
	out = append(out, bindingProblems("", "", "order", OrderProcess, look)...)

	// Then what each service names, with the approval processes reported once each
	// rather than once per product that uses them.
	seenApproval := map[string]bool{}
	var rest []FulfilmentProblem
	for _, it := range items {
		if p := ApprovalProcessFor(it); p != "" && !seenApproval[p] {
			seenApproval[p] = true
			rest = append(rest, bindingProblems("", "", "approval", p, look)...)
		}
		rest = append(rest, bindingProblems(it.ID, it.HomeCatalog, "provision", it.ProvisionProcess, look)...)
		rest = append(rest, bindingProblems(it.ID, it.HomeCatalog, "deprovision", it.DeprovisionProcess, look)...)
	}
	sort.SliceStable(rest, func(a, b int) bool {
		if rest[a].ItemID != rest[b].ItemID {
			return rest[a].ItemID < rest[b].ItemID
		}
		return rest[a].Stage < rest[b].Stage
	})
	return append(out, rest...)
}

// ApprovalProcessFor names the process that decides this item, or "" when it
// needs none. Exported for the guard that holds the table above to the order
// package's own resolver, which is the only thing keeping the copy honest.
func ApprovalProcessFor(it Item) string {
	kind := strings.TrimSpace(string(it.Approval.Kind))
	if kind == "" || kind == string(KindNone) {
		return ""
	}
	if p, ok := approvalProcessOf[kind]; ok {
		return p
	}
	return kind
}

// bindingProblems is the whole check for one binding: the process exists, and every
// job type it carries is worked by something.
func bindingProblems(itemID, home, stage, processID string, look ProcessLookup) []FulfilmentProblem {
	processID = strings.TrimSpace(processID)
	if processID == "" {
		// A service bound to nothing is a catalogue decision, not a defect: an item
		// that is only ever a part of something else provisions through the whole.
		return nil
	}
	at := func(jobType, why string) FulfilmentProblem {
		return FulfilmentProblem{
			ItemID: itemID, HomeCatalog: home, Stage: stage,
			ProcessID: processID, JobType: jobType, Why: why,
		}
	}
	jobTypes, deployed := look.JobTypesOf(processID)
	if !deployed {
		return []FulfilmentProblem{at("", "names the process "+quoteRef(processID)+
			", which no deployed model carries — an order reaching it cannot start one")}
	}
	var out []FulfilmentProblem
	for _, jt := range jobTypes {
		if look.Served(jt) {
			continue
		}
		out = append(out, at(jt, "waits on the job type "+quoteRef(jt)+
			", which nothing here works — its tokens park without failing, so no "+
			"incident is raised and the order stands at waiting"))
	}
	sort.Slice(out, func(a, b int) bool { return out[a].JobType < out[b].JobType })
	return out
}

// HandleFulfilmentReport answers which of the services this caller offers cannot
// be fulfilled on this installation.
//
// Scoped to the catalogues the caller may edit, like the approver report and the
// product list: it names processes and job types, which is maintenance
// information about this installation's wiring.
func (s *Service) HandleFulfilmentReport(w http.ResponseWriter, r *http.Request) {
	// No lookup, no report. Both guesses are worse than a refusal: every process
	// missing reports the whole catalogue as broken, and every one present reports
	// a working estate nobody checked.
	if s.Processes == nil {
		httpapi.Error(w, http.StatusServiceUnavailable,
			"fulfilment report: this server cannot say which processes are deployed or "+
				"which job types are worked, so it cannot say which services park")
		return
	}

	p := httpapi.PrincipalFrom(r.Context())
	var (
		offered  []Item
		releases []string
		loadErr  error
	)
	s.loop.Do(func() {
		cats, err := s.store.Catalogs()
		if err != nil {
			loadErr = err
			return
		}
		for _, cat := range cats {
			if !s.mayEdit(cat, p) {
				continue
			}
			rels, err := s.store.ReleasesOf(cat.ID)
			if err != nil {
				loadErr = err
				return
			}
			if len(rels) == 0 {
				// Never published. Nothing can be ordered from it, so there is nothing
				// here that could park — and saying so would be a finding about a
				// catalogue somebody is still filling.
				continue
			}
			releases = append(releases, rels[0].ID)
			offered = append(offered, rels[0].Items...)
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "fulfilment report: "+loadErr.Error())
		return
	}
	sort.Strings(releases)
	if releases == nil {
		releases = []string{}
	}
	httpapi.JSON(w, http.StatusOK, FulfilmentReport{
		Checked: len(offered), Releases: releases,
		Problems: fulfilmentProblems(offered, s.Processes),
	})
}
