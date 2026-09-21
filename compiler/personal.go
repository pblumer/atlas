package compiler

import (
	"fmt"
	"sort"

	"github.com/pblumer/atlas/expr"
)

// The deploy-time refusal ADR-0314 rests on.
//
// A variable declared personal is enciphered under its subject's data key before it ever
// becomes a command, so what the engine holds is bytes. Ciphertext cannot be compared,
// matched or routed on — so an expression reading one would either fail at runtime or, far
// worse, succeed against the bytes and route on them. Neither is a thing to discover in
// production.
//
// The record puts the check here for a reason worth restating: the compiler holds both the
// declaration and every compiled expression, so this is decidable at deploy time (I5). The
// alternative is what the ISDS recommendation R-06 already is — a rule that relies on a
// modeller remembering — and that is the stated reason R-06 is amber.
//
// The rule's boundary is not a matter of taste. It is *who evaluates the expression*:
//
//   - the engine, during command processing → refused, because the value is ciphertext there;
//   - a connector worker → outside the rule, because that is the edge where ADR-0314
//     permits plaintext, and where it sends a transform that must combine personal values.
//
// Which side each expression falls on was read out of the code rather than assumed: see
// engineEvaluatedExpressions and workerEvaluatedExpressions below.

// engineEvaluatedExpressions are the compiled expressions the *engine* evaluates, during
// command processing. Each one is checked against the personal-variable declaration,
// because at that point a declared value is ciphertext and ciphertext cannot be compared,
// matched or routed on.
//
// The set is not a judgement call: it is what the type walk in personal_visitor_test.go
// finds, minus what a worker evaluates. That test fails if a path appears in neither list,
// so a new expression kind cannot slip past the rule.
var engineEvaluatedExpressions = map[string]bool{
	"adHocs.CompletionCondition":             true,
	"adHocs.ResultElement":                   true,
	"boundaryEventDets.Condition":            true,
	"boundaryEventDets.CorrelationKey":       true,
	"boundaryEventDets.Schedule.Expr":        true,
	"businessRuleTasks.InputMappings.Source": true,
	"conditionals.Condition":                 true,
	"dataInAssocs.Value":                     true,
	"dataOutAssocs.Writes.Value":             true,
	"eventSubProcesses.Condition":            true,
	"eventSubProcesses.CorrelationKey":       true,
	"eventSubProcesses.Schedule.Expr":        true,
	"flows.Condition":                        true,
	"ioInputs.Source":                        true,
	"ioOutputs.Source":                       true,
	"messageCatches.CorrelationKey":          true,
	"messageStarts.CorrelationKey":           true,
	"messageThrows.CorrelationKey":           true,
	"mockupTasks.Expr":                       true,
	"multiInstances.Cardinality":             true,
	"multiInstances.CompletionCondition":     true,
	"multiInstances.InputCollection":         true,
	"multiInstances.LoopCondition":           true,
	"multiInstances.OutputElement":           true,
	"receiveTasks.CorrelationKey":            true,
	"scriptTasks.Expr":                       true,
	"timerCatches.Schedule.Expr":             true,
	"timerStarts.Schedule.Expr":              true,
	"userTasks.AssigneeExpr":                 true,
	"userTasks.CandidateGroupsExpr":          true,
}

// workerEvaluatedExpressions are evaluated in a connector worker rather than by the
// engine, and are therefore outside the rule — deliberately, not by omission.
//
// This is the boundary ADR-0314 draws and it falls exactly here: "Out at the edge — the
// job payload handed to a worker after fsync... Deciphering happens in the post-commit
// phase and in handlers, where reading a key is permitted." A worker expression is that
// edge. It is also where the record sends a transform that has to combine personal values:
// "belong in the worker that needs the result (ADR-0047), where the plaintext exists for
// the duration of one call and is never written back in the clear."
//
// Every one of these is a ConnectorTaskDetail field, and none of them is evaluated in
// engine/: the evaluation sites are connector/rest/worker.go:95, connector/mail/worker.go:89,
// connector/ldap/worker.go:190, connector/ad/worker.go:296, connector/jira/worker.go:82,
// connector/discord/offload.go:141 and connector/agent/aitask.go:133, each over the scope
// variables handed to that call.
//
// THE EXEMPTION IS CONDITIONAL, and the condition is the next step's to satisfy: it holds
// only while the values a worker binds are deciphered before it binds them. If the
// enciphering step lands without deciphering at that point, every entry here becomes a
// worker evaluating FEEL over ciphertext — and this comment is where to come back to.
var workerEvaluatedExpressions = map[string]string{
	"connectorTasks.AdBaseDN.Expr":          "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.AdBindDN.Expr":          "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.AdDN.Expr":              "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.AdFilter.Expr":          "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.AdMemberDN.Expr":        "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.AdNewDN.Expr":           "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.AdNewPassword.Expr":     "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.AdURL.Expr":             "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.AgentPrompt.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.Bcc.Expr":               "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.Body.Expr":              "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.BodyHTML.Expr":          "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.Cc.Expr":                "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.DiscordAfter.Expr":      "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.DiscordChannel.Expr":    "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.DiscordContent.Expr":    "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.DiscordFields.Val.Expr": "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.DiscordMessage.Expr":    "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.DiscordName.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.EntraAttributes.Expr":   "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.EntraConnector.Expr":    "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.EntraDeltaLink.Expr":    "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.EntraFilter.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.EntraGroupID.Expr":      "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.EntraNewPassword.Expr":  "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.EntraSearch.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.EntraUserID.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.Fields.Val.Expr":        "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.From.Expr":              "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.Headers.Val.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.JiraAssignee.Expr":      "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.JiraComment.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.JiraDescription.Expr":   "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.JiraFields.Val.Expr":    "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.JiraIssue.Expr":         "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.JiraIssueType.Expr":     "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.JiraJQL.Expr":           "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.JiraProject.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.JiraQuery.Expr":         "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.JiraSummary.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.JiraTransition.Expr":    "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.LdapBaseDN.Expr":        "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.LdapBindDN.Expr":        "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.LdapDN.Expr":            "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.LdapFilter.Expr":        "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.LdapNewPassword.Expr":   "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.LdapURL.Expr":           "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.List.Expr":              "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.MailSubject.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.Query.Val.Expr":         "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.RemedyFields.Val.Expr":  "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.RemedyForm.Expr":        "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.ScimBaseURL.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.ScimFilter.Expr":        "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.ScimResource.Expr":      "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.ScimResourceID.Expr":    "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.ScrapeSelector.Expr":    "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.SheetsFolder.Expr":      "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.SheetsID.Expr":          "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.SheetsRange.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.SheetsTab.Expr":         "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.SheetsTitle.Expr":       "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.SheetsValues.Expr":      "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.Site.Expr":              "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.SoapAction.Expr":        "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.SoapBody.Expr":          "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.SoapEndpoint.Expr":      "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.To.Expr":                "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.Url.Expr":               "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.UserDisplayName.Expr":   "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.UserEmail.Expr":         "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.UserName.Expr":          "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.UserPassword.Expr":      "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
	"connectorTasks.UserRoles.Expr":         "evaluated in a connector worker, not by the engine — the edge where ADR-0314 permits plaintext",
}

// expressionKindByPath names each engine-evaluated path in the modeller's vocabulary, so
// the refusal can say which expression it found rather than only which variable. Kept as a
// map keyed by the same path strings as the list above, which is what lets
// personal_visitor_test.go check that expressionSites actually reads every path the rule
// claims to cover — a list of paths with no reader would pass every other check and refuse
// nothing.
var expressionKindByPath = map[string]string{
	"adHocs.CompletionCondition":             "ad-hoc completion condition",
	"adHocs.ResultElement":                   "ad-hoc result element",
	"boundaryEventDets.Condition":            "boundary event condition",
	"boundaryEventDets.CorrelationKey":       "boundary event correlation key",
	"boundaryEventDets.Schedule.Expr":        "boundary timer schedule",
	"businessRuleTasks.InputMappings.Source": "decision input mapping",
	"conditionals.Condition":                 "conditional event condition",
	"dataInAssocs.Value":                     "data input association",
	"dataOutAssocs.Writes.Value":             "data output association",
	"eventSubProcesses.Condition":            "event subprocess condition",
	"eventSubProcesses.CorrelationKey":       "event subprocess correlation key",
	"eventSubProcesses.Schedule.Expr":        "event subprocess timer schedule",
	"flows.Condition":                        "sequence flow condition",
	"ioInputs.Source":                        "input mapping",
	"ioOutputs.Source":                       "output mapping",
	"messageCatches.CorrelationKey":          "message catch correlation key",
	"messageStarts.CorrelationKey":           "message start correlation key",
	"messageThrows.CorrelationKey":           "message throw correlation key",
	"mockupTasks.Expr":                       "mockup task",
	"multiInstances.Cardinality":             "multi-instance cardinality",
	"multiInstances.CompletionCondition":     "multi-instance completion condition",
	"multiInstances.InputCollection":         "multi-instance input collection",
	"multiInstances.LoopCondition":           "loop condition",
	"multiInstances.OutputElement":           "multi-instance output element",
	"receiveTasks.CorrelationKey":            "receive task correlation key",
	"scriptTasks.Expr":                       "script task",
	"timerCatches.Schedule.Expr":             "timer schedule",
	"timerStarts.Schedule.Expr":              "timer start schedule",
	"userTasks.AssigneeExpr":                 "user task assignee expression",
	"userTasks.CandidateGroupsExpr":          "user task candidate groups expression",
}

// expressionSite is one compiled expression and what to call it when refusing. There is
// deliberately no element id: not one of the compiled detail types carrying an expression
// holds a node reference, so naming the element would mean a different index-to-node
// mapping per detail type. The expression's own source text is exact and greppable, which
// is what a modeller needs to find it.
type expressionSite struct {
	path string // the field path, so the coverage test can check this reads every one
	e    *expr.Compiled
}

// refuseExpressionsReadingPersonalData is called by Build on the assembled process. It
// returns nil when nothing is declared, which is every process that has not opted in — the
// rule cannot break an existing model that says nothing.
func (p *CompiledProcess) refuseExpressionsReadingPersonalData() error {
	if len(p.personalSet) == 0 {
		return nil
	}
	for _, site := range p.expressionSites() {
		if site.e == nil {
			continue
		}
		var hits []string
		for _, in := range site.e.Inputs() {
			if _, ok := p.personalSet[in]; ok {
				hits = append(hits, in)
			}
		}
		if len(hits) == 0 {
			continue
		}
		sort.Strings(hits)
		kind := expressionKindByPath[site.path]
		if kind == "" {
			kind = site.path
		}
		return fmt.Errorf(
			"compiler: a %s reads personal variable(s) %v: %q — a variable declared personal is enciphered "+
				"before the engine sees it, so it cannot be compared, matched or routed on. Move the transform into "+
				"the worker that needs the result, where the plaintext exists for one call (ADR-0314, ADR-0047)",
			kind, hits, site.e.Source())
	}
	return nil
}

// expressionSites lists every engine-evaluated compiled expression in the process.
//
// Written out by hand rather than found by reflection, because reading unexported fields
// generically needs `unsafe`, and this repository uses that in exactly one production file
// — a Linux sandbox, where syscalls leave no choice. A hand-written list in a compiler that
// gates deployments is the cheaper trade.
//
// What keeps it from rotting is personal_visitor_test.go, which walks the *types* reachable
// from CompiledProcess and fails when a *expr.Compiled field is accounted for by neither
// list, and again when a path the lists claim is not actually read here. That guard paid for
// itself before it was committed: a hand scan of process.go found twenty expression-carrying
// types and missed the four nested TimerSchedule.Expr fields and the six map-valued
// connector fields.
func (p *CompiledProcess) expressionSites() []expressionSite {
	var out []expressionSite
	add := func(path string, e *expr.Compiled) {
		if e != nil {
			out = append(out, expressionSite{path: path, e: e})
		}
	}

	for i := range p.flows {
		add("flows.Condition", p.flows[i].Condition)
	}
	for i := range p.scriptTasks {
		add("scriptTasks.Expr", p.scriptTasks[i].Expr)
	}
	for i := range p.mockupTasks {
		add("mockupTasks.Expr", p.mockupTasks[i].Expr)
	}
	for i := range p.ioInputs {
		add("ioInputs.Source", p.ioInputs[i].Source)
	}
	for i := range p.ioOutputs {
		add("ioOutputs.Source", p.ioOutputs[i].Source)
	}
	for i := range p.dataInAssocs {
		add("dataInAssocs.Value", p.dataInAssocs[i].Value)
	}
	for i := range p.dataOutAssocs {
		for j := range p.dataOutAssocs[i].Writes {
			add("dataOutAssocs.Writes.Value", p.dataOutAssocs[i].Writes[j].Value)
		}
	}
	for i := range p.multiInstances {
		d := p.multiInstances[i]
		add("multiInstances.InputCollection", d.InputCollection)
		add("multiInstances.Cardinality", d.Cardinality)
		add("multiInstances.OutputElement", d.OutputElement)
		add("multiInstances.CompletionCondition", d.CompletionCondition)
		add("multiInstances.LoopCondition", d.LoopCondition)
	}
	for i := range p.userTasks {
		add("userTasks.AssigneeExpr", p.userTasks[i].AssigneeExpr)
		add("userTasks.CandidateGroupsExpr", p.userTasks[i].CandidateGroupsExpr)
	}
	for i := range p.conditionals {
		add("conditionals.Condition", p.conditionals[i].Condition)
	}
	for i := range p.adHocs {
		add("adHocs.CompletionCondition", p.adHocs[i].CompletionCondition)
		add("adHocs.ResultElement", p.adHocs[i].ResultElement)
	}
	for i := range p.businessRuleTasks {
		for j := range p.businessRuleTasks[i].InputMappings {
			add("businessRuleTasks.InputMappings.Source", p.businessRuleTasks[i].InputMappings[j].Source)
		}
	}
	for i := range p.boundaryEventDets {
		d := p.boundaryEventDets[i]
		add("boundaryEventDets.Condition", d.Condition)
		add("boundaryEventDets.CorrelationKey", d.CorrelationKey)
		add("boundaryEventDets.Schedule.Expr", d.Schedule.Expr)
	}
	for i := range p.eventSubProcesses {
		d := p.eventSubProcesses[i]
		add("eventSubProcesses.Condition", d.Condition)
		add("eventSubProcesses.CorrelationKey", d.CorrelationKey)
		add("eventSubProcesses.Schedule.Expr", d.Schedule.Expr)
	}
	for i := range p.messageCatches {
		add("messageCatches.CorrelationKey", p.messageCatches[i].CorrelationKey)
	}
	for i := range p.receiveTasks {
		add("receiveTasks.CorrelationKey", p.receiveTasks[i].CorrelationKey)
	}
	for i := range p.messageThrows {
		add("messageThrows.CorrelationKey", p.messageThrows[i].CorrelationKey)
	}
	for i := range p.messageStarts {
		add("messageStarts.CorrelationKey", p.messageStarts[i].CorrelationKey)
	}
	for i := range p.timerCatches {
		add("timerCatches.Schedule.Expr", p.timerCatches[i].Schedule.Expr)
	}
	for i := range p.timerStarts {
		add("timerStarts.Schedule.Expr", p.timerStarts[i].Schedule.Expr)
	}
	return out
}
