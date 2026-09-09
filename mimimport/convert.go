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

// Note is one item of a conversion Report: a produced node, or one piece of work
// inside it — a decoded row of a MIMWAL collection is its own item, because it is
// its own read or write to re-express.
type Note struct {
	NodeID   string // BPMN id of the produced element
	Activity string // XOML activity local name it came from
	Kind     string // BPMN element kind (userTask, serviceTask, exclusiveGateway, …)
	Status   Status
	Detail   string // what a reviewer should know (why preserved / what to check)
}

// Report is the migration worksheet of a conversion: every produced node and,
// inside a node, every decoded row of the MIMWAL collections it carries. The
// counts are therefore counts of *work*, not of BPMN elements — which is the
// number a migration is planned with.
type Report struct {
	ProcessID string
	Notes     []Note
	// Warnings are document-level observations that belong to no single node —
	// today, that the input had to be repaired before it would parse.
	Warnings []string
}

// Count returns how many items carry the given status.
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
// item) suitable for stderr or a CLI log.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "process %s: %d native, %d preserved, %d manual-review\n",
		r.ProcessID, r.Count(StatusNative), r.Count(StatusPreserved), r.Count(StatusManualReview))
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "  [warning      ] %s\n", w)
	}
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
//	*Unique*         → serviceTask    (preserved, type mim-uniquevalue)
//	Create/Update/Delete/Group/Resource → serviceTask (preserved, type mim-resource)
//	anything else    → task           (manual-review) with the XOML preserved
//
// A leaf carrying a MIMWAL ActivityExecutionCondition is additionally wrapped in
// an exclusive split/merge, because MIMWAL expresses conditionality per activity
// rather than as control flow — see emitGuard. One carrying a MIMWAL Iteration
// becomes a sequential multi-instance activity — see emitMultiInstance. The
// serialised .NET collections a MIMWAL activity hangs off itself are rendered as
// a table on its documentation — see mimTables.
//
// name, when non-empty, overrides the process name derived from the workflow.
func Convert(r io.Reader, name string) (Result, error) {
	root, warnings, err := parseXOML(r)
	if err != nil {
		return Result{}, err
	}

	wfName := name
	if wfName == "" {
		wfName = root.displayName()
	}

	b := newBuilder(wfName, namespaces(root))
	b.report.Warnings = warnings
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
	srcID   string          // preferred id, derived from the activity's x:Name
	def     string          // default outgoing flow id (gateways only)
	jobType string          // serviceTask job type
	doc     string          // documentation note
	iterate string          // MIM Iteration expression: the activity is multi-instance
	tables  []mimCollection // decoded MIMWAL collections, emitted as atlas:mimCollection
	rawName string          // originating XOML activity local name
	rawType string          // its fully qualified .NET type, when the namespace names one
	rawAsm  string          // the assembly that type lives in
	raw     string          // original XOML markup, preserved verbatim
}

type bflow struct {
	id, from, to, name, cond string
}

type builder struct {
	name string
	// ns holds the prefix bindings of the workflow root, so a preserved activity
	// keeps the namespace that names its .NET type and assembly (see nsTable).
	ns     *nsTable
	nodes  []bnode
	flows  []bflow
	report Report
	nNode  int
	nFlow  int
	nGate  int
	// used holds every id already handed out. BPMN ids are document-wide, and
	// they now come partly from the workflow's own x:Name values, so nothing can
	// assume a generated id is free.
	used map[string]bool
}

func newBuilder(name string, ns *nsTable) *builder {
	b := &builder{
		name: name, ns: ns,
		report: Report{ProcessID: sanitizeID(name, "mim_workflow")},
		used:   map[string]bool{},
	}
	b.claim(b.report.ProcessID) // an activity must not take the process's own id
	return b
}

// claim reserves id, or the first free "<id>_<n>" if something already holds it.
func (b *builder) claim(id string) string {
	if !b.used[id] {
		b.used[id] = true
		return id
	}
	for i := 2; ; i++ {
		c := fmt.Sprintf("%s_%d", id, i)
		if !b.used[c] {
			b.used[c] = true
			return c
		}
	}
}

// nextID reserves the first free "<prefix>_<n>", advancing the counter past any
// number an activity's own name already took.
func (b *builder) nextID(prefix string, n *int) string {
	for {
		*n++
		id := fmt.Sprintf("%s_%d", prefix, *n)
		if !b.used[id] {
			b.used[id] = true
			return id
		}
	}
}

// nodeID picks a node's id: the one derived from its x:Name when it has one, so
// the id survives an edit elsewhere in the workflow, and a counter otherwise.
func (b *builder) nodeID(srcID, prefix string, n *int) string {
	if srcID == "" {
		return b.nextID(prefix, n)
	}
	return b.claim(srcID)
}

// addNode appends a node, giving it an id unless the caller already reserved one.
func (b *builder) addNode(n bnode) string {
	if n.id == "" {
		switch n.kind {
		case "startEvent":
			n.id = b.claim("StartEvent_1")
		case "endEvent":
			n.id = b.claim("EndEvent_1")
		case "exclusiveGateway", "parallelGateway":
			n.id = b.nodeID(n.srcID, "Gateway", &b.nGate)
		default:
			n.id = b.nodeID(n.srcID, "Activity", &b.nNode)
		}
	}
	b.nodes = append(b.nodes, n)
	return n.id
}

func (b *builder) addFlow(from, to, name, cond string) string {
	id := b.nextID("Flow", &b.nFlow)
	b.flows = append(b.flows, bflow{id: id, from: from, to: to, name: name, cond: cond})
	return id
}

func (b *builder) note(n Note) { b.report.Notes = append(b.report.Notes, n) }

// preserve fills in the bnode fields that carry an activity's original identity:
// its local name, its .NET type and assembly, and its markup.
func (b *builder) preserve(n xnode) bnode {
	t, asm := clrType(n.XMLName.Space, n.local())
	return bnode{srcID: sourceID(n), rawName: n.local(), rawType: t, rawAsm: asm, raw: n.raw(b.ns)}
}

// sourceID derives a BPMN id from the activity's x:Name — the identifier the WF
// designer gives it, and the only one that survives an edit elsewhere in the
// workflow. Ids from a counter shift as soon as an activity is inserted above,
// which leaves a re-import of a barely changed workflow with a diff touching
// every node and every hand-made adjustment stranded.
func sourceID(n xnode) string {
	for _, a := range n.Attrs {
		if a.Name.Space == xamlNS && a.Name.Local == "Name" {
			if id := sanitizeID(a.Value, ""); id != "" {
				return id
			}
		}
	}
	if v, ok := n.attr("Name"); ok {
		return sanitizeID(v, "")
	}
	return ""
}

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
		return b.emitLeaf(n)
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
	splitNode := b.preserve(n)
	splitNode.kind, splitNode.name = "exclusiveGateway", n.displayName()
	split := b.addNode(splitNode)
	join := b.addNode(bnode{kind: "exclusiveGateway", srcID: split + "_join"})
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
	splitNode := b.preserve(n)
	splitNode.kind, splitNode.name = "parallelGateway", n.displayName()
	split := b.addNode(splitNode)
	join := b.addNode(bnode{kind: "parallelGateway", srcID: split + "_join"})
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
	decideNode := b.preserve(n)
	decideNode.kind, decideNode.name = "exclusiveGateway", n.displayName()
	decide := b.addNode(decideNode)
	out := b.addNode(bnode{kind: "exclusiveGateway", srcID: decide + "_exit"})
	be, bx := b.emitSequence(activityChildren(n))
	if be == "" { // empty loop body: keep a placeholder step so the loop is well-formed
		bodyNode := b.preserve(n)
		bodyNode.kind, bodyNode.name = "task", "Schleifenkörper"
		id := b.addNode(bodyNode)
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
// its status, returning the entry and exit of the produced subgraph. The
// original activity is preserved on every leaf.
//
// An activity that carries a guard is wrapped in a conditional split/merge —
// see emitGuard — so the entry and exit are the gateways rather than the task.
func (b *builder) emitLeaf(n xnode) (entry, exit string) {
	kind, jobType, status, detail := classifyLeaf(n.local())
	guard, guarded := executionCondition(n)
	iter, iterated := iteration(n)

	// The task's id is reserved first so the gateways that guard it can be named
	// after it, even though they are added before it.
	leafID := b.nodeID(sourceID(n), "Activity", &b.nNode)
	var split, merge string
	if guarded {
		split, merge = b.addGuardGateways(guard, leafID)
	}
	doc := detail
	if iterated {
		doc += " — MIM Iteration: " + iter
	}
	// One decode feeds all three renderings of a MIMWAL collection: the readable
	// table on the documentation, the addressable elements on the node, and the
	// per-row items of the worksheet.
	tables := mimCollections(n)
	if rendered := renderCollections(tables); rendered != "" {
		doc += "\n" + rendered
	}
	leaf := b.preserve(n)
	leaf.id = leafID
	leaf.kind, leaf.name, leaf.jobType, leaf.doc, leaf.iterate = kind, n.displayName(), jobType, doc, iter
	leaf.tables = tables
	id := b.addNode(leaf)
	b.note(Note{NodeID: id, Activity: n.local(), Kind: kind, Status: status, Detail: detail})
	b.noteCollections(id, n.local(), kind, tables)
	if iterated {
		b.note(Note{NodeID: id, Activity: n.local(), Kind: "multiInstanceLoopCharacteristics", Status: StatusManualReview,
			Detail: "MIM Iteration not translated to FEEL; the placeholder collection runs the activity once: " + iter})
	}
	if !guarded {
		return id, id
	}
	b.emitGuard(n, split, merge, id, guard)
	return split, merge
}

// noteCollections adds one worksheet item per decoded row of a MIMWAL
// collection, plus one per decoded finding.
//
// This is what makes the report a migration worksheet rather than a node
// inventory. An UpdateResources carrying five assignments and a named query is
// one node — "preserved", one line — and six pieces of work: each row is a read
// or a write that has to be re-expressed against a real target system, and the
// count a reviewer plans with has to say six. An item names its collection, its
// index and its cells; it says nothing about what a column means, because
// nothing here knows (see tables.go).
func (b *builder) noteCollections(nodeID, activity, kind string, cols []mimCollection) {
	for _, c := range cols {
		for _, r := range c.rows {
			b.note(Note{NodeID: nodeID, Activity: activity, Kind: kind, Status: StatusManualReview,
				Detail: c.label(r) + ": " + cellsOneLine(r)})
		}
		for _, n := range c.notes {
			b.note(Note{NodeID: nodeID, Activity: activity, Kind: kind, Status: StatusManualReview,
				Detail: c.property + ": " + n})
		}
	}
}

// iteration returns a MIMWAL activity's Iteration expression, if it carries one.
// MIMWAL runs such an activity once per value of the expression — SplitString of
// a delimited attribute, typically — which is a BPMN multi-instance activity, not
// the single step the element alone suggests.
func iteration(n xnode) (string, bool) {
	v, ok := n.attr("Iteration")
	if !ok {
		return "", false
	}
	v = strings.TrimSpace(v)
	return v, v != ""
}

// executionCondition returns an activity's guard, if it carries one. MIMWAL
// expresses conditionality per activity rather than as control flow: an
// UpdateResources or GenerateUniqueValue runs only when its
// ActivityExecutionCondition holds, and a workflow of twenty such activities has
// no IfElseActivity in it at all. Reading only the control-flow elements would
// therefore model such a workflow as an unconditional chain.
func executionCondition(n xnode) (string, bool) {
	v, ok := n.attr("ActivityExecutionCondition")
	if !ok {
		return "", false
	}
	v = strings.TrimSpace(v)
	return v, v != ""
}

// addGuardGateways reserves the split and merge of a guarded activity ahead of
// the task itself, so the generated markup reads in flow order. The split
// documents the guard it stands for, which is the only place a reader of the
// model sees the original expression.
func (b *builder) addGuardGateways(guard, taskID string) (split, merge string) {
	split = b.addNode(bnode{kind: "exclusiveGateway", srcID: taskID + "_gate",
		doc: "MIM ActivityExecutionCondition: " + guard})
	merge = b.addNode(bnode{kind: "exclusiveGateway", srcID: taskID + "_join"})
	return split, merge
}

// condAlways is the placeholder that keeps a guarded activity on the path it
// took before its guard was modelled. The guard itself is not translated — the
// MIM function library (ConvertToBoolean, ParametersContain, IsPresent,
// RegexMatch) has semantics this package cannot reproduce faithfully, and its
// data references ([//Target/x], [//WorkflowData/y]) have no agreed FEEL
// counterpart yet — so translating one would risk a model that looks right and
// is not.
const condAlways = "= true"

// emitGuard wires a guarded activity between its split and merge: the activity
// is entered on a condition, and a bypass carries the flow past it.
//
// The condition is the placeholder condAlways rather than the guard, so the
// generated process still runs every activity exactly as it did before guards
// were modelled; what changes is that the model now *shows* the activity is
// conditional and names the one flow whose expression has to be filled in. The
// bypass is the gateway default, which is where an untranslated guard belongs:
// once the real condition replaces the placeholder, an activity whose guard does
// not hold is skipped rather than silently run.
func (b *builder) emitGuard(n xnode, split, merge, id, guard string) {
	run := b.addFlow(split, id, "ausführen", condAlways)
	b.addFlow(id, merge, "", "")
	skip := b.addFlow(split, merge, "überspringen", "")
	b.setDefault(split, skip)
	b.note(Note{NodeID: run, Activity: n.local(), Kind: "conditionExpression", Status: StatusManualReview,
		Detail: "MIM ActivityExecutionCondition not translated to FEEL; the placeholder always runs the activity: " + guard})
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
	case strings.Contains(l, "unique"):
		// MIMWAL's GenerateUniqueValue: it evaluates value expressions in order and
		// publishes the first that no existing resource and no LDAP object claims.
		return "serviceTask", "mim-uniquevalue", StatusPreserved,
			"MIMWAL unique-value generation kept in atlas:mimSource; wire a worker for its value expressions and conflict filter"
	case strings.Contains(l, "resource"), strings.Contains(l, "create"), strings.Contains(l, "update"),
		strings.Contains(l, "delete"), strings.Contains(l, "group"), strings.Contains(l, "provision"):
		return "serviceTask", "mim-resource", StatusPreserved, "resource operation kept in atlas:mimSource; map to a target-system worker"
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
