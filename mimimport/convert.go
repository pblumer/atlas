package mimimport

import (
	"fmt"
	"io"
	"strings"
)

// Status classifies how faithfully one produced BPMN node reflects its XOML
// source, so the lossy points of a conversion are explicit.
type Status string

const (
	// StatusNative marks a node whose BPMN meaning matches the WF construct:
	// control-flow gateways, approvals (user task), notifications.
	StatusNative Status = "native"
	// StatusPreserved marks a node mapped to a typed task shell whose inner logic
	// (a FEEL body, a PowerShell script, a resource operation) was not translated
	// but is kept verbatim in <atlas:mimSource> for a developer to wire up.
	StatusPreserved Status = "preserved"
	// StatusManualReview marks a node that needs a human: an unrecognised activity
	// kept as a plain-task placeholder, or a branch/loop whose condition could not
	// be translated to FEEL and was replaced with a safe placeholder.
	StatusManualReview Status = "manual-review"
)

// Note is one entry in a conversion Report.
type Note struct {
	NodeID   string // BPMN id of the produced element
	Activity string // XOML activity local name it came from
	Kind     string // BPMN element kind (userTask, serviceTask, exclusiveGateway, …)
	Status   Status
	Detail   string // what a reviewer should know (why preserved / what to check)
}

// Report is the per-node ledger of a conversion.
type Report struct {
	ProcessID string
	Notes     []Note
}

// Count returns how many notes carry the given status.
func (r Report) Count(s Status) int {
	n := 0
	for _, note := range r.Notes {
		if note.Status == s {
			n++
		}
	}
	return n
}

// String renders the report as a stable, human-readable summary (one line per
// node) suitable for stderr or a CLI log.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "process %s: %d native, %d preserved, %d manual-review\n",
		r.ProcessID, r.Count(StatusNative), r.Count(StatusPreserved), r.Count(StatusManualReview))
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "  [%-13s] %-16s %-18s %s", n.Status, n.NodeID, n.Kind, n.Activity)
		if n.Detail != "" {
			fmt.Fprintf(&b, " — %s", n.Detail)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Result is the output of a conversion: the BPMN document and its report.
type Result struct {
	BPMN   []byte
	Report Report
}

// Convert reads a MIM/FIM XOML workflow (or an Export-FIMConfig wrapper that
// embeds one) and returns Atlas-deployable BPMN 2.0.
//
// Control flow is mapped natively:
//
//	SequentialWorkflow / Sequence  → a chain of flow nodes
//	IfElseActivity (+ branches)    → an exclusive gateway split/join
//	ParallelActivity (+ branches)  → a parallel gateway split/join
//	WhileActivity                  → an exclusive-gateway loop
//
// Leaf activities are mapped by intent:
//
//	Approval*        → userTask       (native)
//	Notification/Email → serviceTask  (native, type mim-notification)
//	FunctionEvaluator → serviceTask   (preserved, type mim-function)
//	PowerShell*      → serviceTask    (preserved, type mim-powershell)
//	Create/Update/Delete/Group/Resource → serviceTask (preserved, type mim-resource)
//	anything else    → task           (manual-review) with the XOML preserved
//
// name, when non-empty, overrides the process name derived from the workflow.
func Convert(r io.Reader, name string) (Result, error) {
	root, err := parseXOML(r)
	if err != nil {
		return Result{}, err
	}

	wfName := name
	if wfName == "" {
		wfName = root.displayName()
	}

	b := newBuilder(wfName)
	body := workflowBody(root)
	entry, exit := b.emitSequence(body)

	start := b.addNode(bnode{kind: "startEvent", name: "Start"})
	end := b.addNode(bnode{kind: "endEvent", name: "Ende"})
	if entry == "" { // empty workflow: still a valid, deployable process
		b.addFlow(start, end, "", "")
	} else {
		b.addFlow(start, entry, "", "")
		b.addFlow(exit, end, "", "")
	}

	bpmn := b.emitBPMN(root)
	return Result{BPMN: bpmn, Report: b.report}, nil
}

// workflowBody returns the ordered activities that make up a workflow root. A
// *Workflow* container contributes its children; a bare activity is its own
// single-element body.
func workflowBody(root xnode) []xnode {
	l := strings.ToLower(root.local())
	if strings.Contains(l, "workflow") {
		return activityChildren(root)
	}
	if strings.HasSuffix(l, "activity") {
		return []xnode{root}
	}
	return activityChildren(root)
}

// activityChildren filters a node's children to the ones that are activities,
// skipping the WF metadata elements (conditions, parameter bindings) that live
// as siblings but are not steps in the flow.
func activityChildren(n xnode) []xnode {
	var out []xnode
	for _, k := range n.Kids {
		if isMetadata(k.local()) {
			continue
		}
		out = append(out, k)
	}
	return out
}

func isMetadata(local string) bool {
	switch strings.ToLower(local) {
	case "condition", "codecondition", "ruleconditionreference",
		"workflowparameterbindingcollection", "workflowparameterbinding", "bind":
		return true
	}
	return false
}

// ---- builder ---------------------------------------------------------------

type bnode struct {
	id      string
	kind    string // startEvent,endEvent,userTask,serviceTask,task,exclusiveGateway,parallelGateway
	name    string
	def     string // default outgoing flow id (gateways only)
	jobType string // serviceTask job type
	doc     string // documentation note
	rawName string // originating XOML activity local name
	raw     string // original XOML markup, preserved verbatim
}

type bflow struct {
	id, from, to, name, cond string
}

type builder struct {
	name   string
	nodes  []bnode
	flows  []bflow
	report Report
	nNode  int
	nFlow  int
	nGate  int
}

func newBuilder(name string) *builder {
	return &builder{name: name, report: Report{ProcessID: sanitizeID(name, "mim_workflow")}}
}

func (b *builder) addNode(n bnode) string {
	if n.id == "" {
		switch n.kind {
		case "startEvent":
			n.id = "StartEvent_1"
		case "endEvent":
			n.id = "EndEvent_1"
		case "exclusiveGateway", "parallelGateway":
			b.nGate++
			n.id = fmt.Sprintf("Gateway_%d", b.nGate)
		default:
			b.nNode++
			n.id = fmt.Sprintf("Activity_%d", b.nNode)
		}
	}
	b.nodes = append(b.nodes, n)
	return n.id
}

func (b *builder) addFlow(from, to, name, cond string) string {
	b.nFlow++
	id := fmt.Sprintf("Flow_%d", b.nFlow)
	b.flows = append(b.flows, bflow{id: id, from: from, to: to, name: name, cond: cond})
	return id
}

func (b *builder) note(n Note) { b.report.Notes = append(b.report.Notes, n) }

// emit dispatches on the WF construct type and returns the entry/exit node ids
// of the produced subgraph ("" for both when nothing was produced).
func (b *builder) emit(n xnode) (entry, exit string) {
	switch strings.ToLower(n.local()) {
	case "sequentialworkflow", "sequentialworkflowactivity", "sequenceactivity", "sequence":
		return b.emitSequence(activityChildren(n))
	case "ifelseactivity", "ifelse":
		return b.emitIfElse(n)
	case "parallelactivity", "parallel":
		return b.emitParallel(n)
	case "whileactivity", "while", "whileloopactivity":
		return b.emitWhile(n)
	case "conditionedactivitygroup":
		return b.emitSequence(activityChildren(n))
	default:
		id := b.emitLeaf(n)
		return id, id
	}
}

// emitSequence chains child activities entry→exit and returns the ends of the
// whole chain.
func (b *builder) emitSequence(kids []xnode) (entry, exit string) {
	for _, k := range kids {
		e, x := b.emit(k)
		if e == "" {
			continue
		}
		if entry == "" {
			entry, exit = e, x
			continue
		}
		b.addFlow(exit, e, "", "")
		exit = x
	}
	return entry, exit
}

// emitIfElse turns an IfElseActivity into an exclusive split/join. The last
// branch (or the first one without a condition) becomes the gateway default;
// every other branch gets a placeholder FEEL condition, with the original WF
// condition preserved for review.
func (b *builder) emitIfElse(n xnode) (string, string) {
	split := b.addNode(bnode{kind: "exclusiveGateway", name: n.displayName(), rawName: n.local(), raw: n.raw()})
	join := b.addNode(bnode{kind: "exclusiveGateway", name: ""})
	branches := activityChildren(n)
	b.note(Note{NodeID: split, Activity: n.local(), Kind: "exclusiveGateway", Status: StatusNative,
		Detail: fmt.Sprintf("if/else split, %d branch(es)", len(branches))})

	for i, br := range branches {
		be, bx := b.emitSequence(activityChildren(br))
		isDefault := i == len(branches)-1
		var flowID string
		if be == "" { // empty branch: connect split straight to join
			flowID = b.addFlow(split, join, branchName(br, isDefault), placeholderCond(isDefault))
		} else {
			flowID = b.addFlow(split, be, branchName(br, isDefault), placeholderCond(isDefault))
			b.addFlow(bx, join, "", "")
		}
		if isDefault {
			b.setDefault(split, flowID)
		} else if cond, ok := branchCondition(br); ok {
			b.note(Note{NodeID: flowID, Activity: br.local(), Kind: "conditionExpression", Status: StatusManualReview,
				Detail: "WF condition not translated to FEEL: " + cond})
		}
	}
	return split, join
}

// emitParallel turns a ParallelActivity into a parallel split/join. Each child
// is a branch (usually a Sequence).
func (b *builder) emitParallel(n xnode) (string, string) {
	split := b.addNode(bnode{kind: "parallelGateway", name: n.displayName(), rawName: n.local(), raw: n.raw()})
	join := b.addNode(bnode{kind: "parallelGateway", name: ""})
	branches := activityChildren(n)
	b.note(Note{NodeID: split, Activity: n.local(), Kind: "parallelGateway", Status: StatusNative,
		Detail: fmt.Sprintf("parallel split, %d branch(es)", len(branches))})
	for _, br := range branches {
		be, bx := b.emit(br)
		if be == "" {
			b.addFlow(split, join, "", "")
			continue
		}
		b.addFlow(split, be, "", "")
		b.addFlow(bx, join, "", "")
	}
	return split, join
}

// emitWhile turns a WhileActivity into an exclusive-gateway loop: a decision
// gateway enters the body while its (placeholder) condition holds and otherwise
// leaves through a merge gateway; the body loops back to the decision.
func (b *builder) emitWhile(n xnode) (string, string) {
	decide := b.addNode(bnode{kind: "exclusiveGateway", name: n.displayName(), rawName: n.local(), raw: n.raw()})
	out := b.addNode(bnode{kind: "exclusiveGateway", name: ""})
	be, bx := b.emitSequence(activityChildren(n))
	if be == "" { // empty loop body: keep a placeholder step so the loop is well-formed
		id := b.addNode(bnode{kind: "task", name: "Schleifenkörper", rawName: n.local(), raw: n.raw()})
		b.note(Note{NodeID: id, Activity: n.local(), Kind: "task", Status: StatusManualReview, Detail: "empty while body"})
		be, bx = id, id
	}
	b.addFlow(decide, be, "wiederholen", placeholderCond(false)) // enter body while condition holds
	b.addFlow(bx, decide, "", "")                                // loop back to re-check
	exit := b.addFlow(decide, out, "fertig", "")                 // leave when it no longer holds
	b.setDefault(decide, exit)
	detail := "loop condition not translated to FEEL"
	if cond, ok := branchCondition(n); ok {
		detail += ": " + cond
	}
	b.note(Note{NodeID: decide, Activity: n.local(), Kind: "exclusiveGateway", Status: StatusManualReview, Detail: detail})
	return decide, out
}

func (b *builder) setDefault(gwID, flowID string) {
	for i := range b.nodes {
		if b.nodes[i].id == gwID {
			b.nodes[i].def = flowID
			return
		}
	}
}

// emitLeaf maps a single (non-control-flow) activity to a BPMN task and records
// its status. The original activity is preserved on every leaf.
//
// A MIMWAL activity may carry an ActivityExecutionCondition — a gate that, at
// MIM runtime, decides whether the step runs at all. BPMN has no per-task
// execution guard, so the condition cannot be translated in a first pass; it is
// surfaced on the task's documentation and flagged for manual review rather
// than left buried in the preserved source.
func (b *builder) emitLeaf(n xnode) string {
	kind, jobType, status, detail := classifyLeaf(n.local())
	doc := detail
	cond, hasCond := executionCondition(n)
	if hasCond {
		doc += " · Ausführungsbedingung (Original, nicht nach FEEL übersetzt): " + cond
	}
	id := b.addNode(bnode{
		kind:    kind,
		name:    n.displayName(),
		jobType: jobType,
		rawName: n.local(),
		raw:     n.raw(),
		doc:     doc,
	})
	b.note(Note{NodeID: id, Activity: n.local(), Kind: kind, Status: status, Detail: detail})
	if hasCond {
		b.note(Note{NodeID: id, Activity: n.local(), Kind: kind, Status: StatusManualReview,
			Detail: "ActivityExecutionCondition not translated to FEEL: " + cond})
	}
	return id
}

// executionCondition returns a MIMWAL activity's ActivityExecutionCondition, as
// an attribute or as a WF property element (<…​.ActivityExecutionCondition>),
// if one is present and non-empty.
func executionCondition(n xnode) (string, bool) {
	if v, ok := n.attr("ActivityExecutionCondition"); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v), true
	}
	for _, k := range n.Kids {
		if strings.HasSuffix(strings.ToLower(k.local()), "activityexecutioncondition") {
			if s := strings.TrimSpace(k.Inner); s != "" {
				return s, true
			}
		}
	}
	return "", false
}

// classifyLeaf maps an activity's local name to a BPMN task kind, an optional
// service-task job type, a status and a reviewer note.
func classifyLeaf(local string) (kind, jobType string, status Status, detail string) {
	l := strings.ToLower(local)
	switch {
	case strings.Contains(l, "approval"):
		return "userTask", "", StatusNative, "MIM approval → user task; map approvers to assignees"
	case strings.Contains(l, "notification"), strings.Contains(l, "email"), strings.Contains(l, "sendmail"):
		return "serviceTask", "mim-notification", StatusNative, "MIM notification → notification worker"
	case strings.Contains(l, "function") && strings.Contains(l, "eval"), strings.Contains(l, "functionevaluator"):
		return "serviceTask", "mim-function", StatusPreserved, "FunctionEvaluator expression kept in atlas:mimSource; re-express as FEEL"
	case strings.Contains(l, "powershell"):
		return "serviceTask", "mim-powershell", StatusPreserved, "PowerShell activity kept in atlas:mimSource; wire a PowerShell worker"
	case strings.Contains(l, "resource"), strings.Contains(l, "create"), strings.Contains(l, "update"),
		strings.Contains(l, "delete"), strings.Contains(l, "group"), strings.Contains(l, "provision"):
		return "serviceTask", "mim-resource", StatusPreserved, "resource operation kept in atlas:mimSource; map to a target-system connector"
	case strings.Contains(l, "code"):
		return "serviceTask", "mim-code", StatusPreserved, "WF CodeActivity kept in atlas:mimSource; reimplement as a worker"
	default:
		return "task", "", StatusManualReview, "unrecognised activity kept as a placeholder; original in atlas:mimSource"
	}
}

// branchCondition returns the human-readable WF condition of an IfElse branch or
// While activity, if one is present.
func branchCondition(n xnode) (string, bool) {
	if v, ok := n.attr("Condition"); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v), true
	}
	for _, k := range n.Kids {
		if strings.EqualFold(k.local(), "Condition") {
			if s := strings.TrimSpace(k.Inner); s != "" {
				return s, true
			}
		}
	}
	return "", false
}

func branchName(br xnode, isDefault bool) string {
	if isDefault {
		return "sonst"
	}
	if n := strings.TrimSpace(br.displayName()); n != "" && !strings.EqualFold(n, br.local()) {
		return n
	}
	return ""
}

// placeholderCond returns a compile-valid FEEL condition. Non-default branches
// get "= false" (graph-reachable but inert) so the generated model always
// deploys; the real condition is preserved for manual translation.
func placeholderCond(isDefault bool) string {
	if isDefault {
		return ""
	}
	return "= false"
}

// sanitizeID turns an arbitrary label into a BPMN-safe NCName, falling back to
// def when nothing usable remains.
func sanitizeID(s, def string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return def
	}
	if c := out[0]; c >= '0' && c <= '9' {
		out = "mim_" + out
	}
	return out
}
