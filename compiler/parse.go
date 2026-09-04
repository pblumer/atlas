package compiler

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/expr"
)

// defaultRetries is used when a service task's task definition omits retries.
const defaultRetries = 3

// defaultUserTaskPriority mirrors Camunda's default task priority (ADR-0091): a
// user task with no zeebe:priorityDefinition sorts as if priority 50.
const defaultUserTaskPriority = 50

// scriptJobTypes maps a polyglot script task's language (lower-cased) to the
// reserved job type its in-process worker subscribes to (ADR-0047). Adding a
// language is one entry here plus its worker; the compiler, node type, and
// recovery semantics are shared. The mapping is resolved at deploy time
// (invariant I5) so the runtime never inspects the language.
var scriptJobTypes = map[string]string{
	"powershell": PwshJobType,
	"python":     PythonJobType,
	"javascript": JsJobType,
}

// restMethods is the set of HTTP methods a REST task may use. The set
// is validated at deploy time (invariant I5) so the runtime worker never has to.
var restMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true,
}

// normalizeHTTPMethod upper-cases a REST worker's method (defaulting to GET
// when omitted) and rejects anything outside restMethods.
func normalizeHTTPMethod(m string) (string, error) {
	if m == "" {
		return "GET", nil
	}
	up := strings.ToUpper(strings.TrimSpace(m))
	if !restMethods[up] {
		return "", fmt.Errorf("unsupported HTTP method %q", m)
	}
	return up, nil
}

// httpKVMap turns a REST worker's header or query-parameter child elements into
// a {name:value} map, trimming names and skipping rows with an empty name (an
// incomplete row the author hasn't filled in). A duplicated name is an error, so a
// silent last-wins collision can't hide a modeling mistake. kind names the field
// for the error message ("header" / "query parameter").
func httpKVList(taskID, kind string, kvs []xmlHTTPKV) ([]RestKV, error) {
	if len(kvs) == 0 {
		return nil, nil
	}
	out := make([]RestKV, 0, len(kvs))
	seen := make(map[string]bool, len(kvs))
	for _, kv := range kvs {
		name := strings.TrimSpace(kv.Name)
		if name == "" {
			continue
		}
		if seen[name] {
			return nil, fmt.Errorf("compiler: rest task %q has a duplicate %s %q", taskID, kind, name)
		}
		seen[name] = true
		val, err := restValue(taskID, kind+" "+name, kv.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, RestKV{Name: name, Val: val})
	}
	return out, nil
}

// restValue turns a model field value into a literal or a compiled FEEL expression
// (the Camunda-style fx toggle, ADR-0067): a value with a leading '=' is a FEEL
// expression compiled once at deploy time (invariant I5) and evaluated over the
// instance's variables at call time; otherwise it is a literal used verbatim. what
// names the field for error messages.
func restValue(taskID, what, raw string) (RestExpr, error) {
	return connectorValue(taskID, "rest worker", what, raw)
}

// connectorValue is the shared literal-or-FEEL toggle for the HTTP-based workers
// (REST, SCIM): a value with a leading '=' is a FEEL expression compiled once at
// deploy time (invariant I5) and evaluated over the instance's variables at call
// time; otherwise it is a literal used verbatim. kind names the worker for
// diagnostics ("rest worker"/"scim worker") and what names the field.
func connectorValue(taskID, kind, what, raw string) (RestExpr, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "=") {
		return RestExpr{Literal: raw}, nil
	}
	text := strings.TrimSpace(trimmed[1:])
	if text == "" {
		return RestExpr{}, fmt.Errorf("compiler: %s task %q has an empty FEEL expression for %s", kind, taskID, what)
	}
	e, err := expr.CompileAuto(text)
	if err != nil {
		return RestExpr{}, fmt.Errorf("compiler: %s task %q: %s: %w", kind, taskID, what, err)
	}
	return RestExpr{Expr: e}, nil
}

// restAuth reads a REST worker's authentication config from its extension.
func restAuth(taskID string, c *xmlRestConnector) (RestAuth, error) {
	// oauth2 is REST-only: the client-credentials grant needs a token endpoint and a
	// client id, and only <atlas:restConnector> carries those attributes (ADR-0152).
	// Every other scheme is shared with the SCIM worker via connectorAuth.
	if strings.ToLower(strings.TrimSpace(c.AuthType)) == "oauth2" {
		return restOAuth2(taskID, c)
	}
	return connectorAuth(taskID, "rest worker", c.AuthType, c.AuthUsername, c.AuthApiKeyName, c.AuthSecret)
}

// restOAuth2 builds a REST task's client-credentials config (ADR-0152):
// the token endpoint and client id are model data; the client secret is a reference
// (secrets live server-side, ADR-0041). Scope is optional.
func restOAuth2(taskID string, c *xmlRestConnector) (RestAuth, error) {
	if strings.TrimSpace(c.AuthTokenURL) == "" {
		return RestAuth{}, fmt.Errorf("compiler: rest task %q uses oauth2 auth but names no tokenUrl", taskID)
	}
	if strings.TrimSpace(c.AuthClientID) == "" {
		return RestAuth{}, fmt.Errorf("compiler: rest task %q uses oauth2 auth but names no clientId", taskID)
	}
	if strings.TrimSpace(c.AuthSecret) == "" {
		return RestAuth{}, fmt.Errorf("compiler: rest task %q uses oauth2 auth but names no client secret reference", taskID)
	}
	return RestAuth{
		Type:      "oauth2",
		ClientID:  strings.TrimSpace(c.AuthClientID),
		SecretRef: strings.TrimSpace(c.AuthSecret),
		TokenURL:  strings.TrimSpace(c.AuthTokenURL),
		Scope:     strings.TrimSpace(c.AuthScope),
	}, nil
}

// connectorAuth builds an HTTP-based task's authentication config from its
// authType and credential-reference fields, shared by REST and SCIM. authType selects
// the scheme; an unknown scheme is rejected, and a scheme that needs a secret
// reference must name one (secrets live server-side, ADR-0041, so the model always
// references rather than carries them). kind names the worker for diagnostics.
func connectorAuth(taskID, kind, authType, username, apiKeyName, secret string) (RestAuth, error) {
	t := strings.ToLower(strings.TrimSpace(authType))
	switch t {
	case "", "none":
		return RestAuth{}, nil
	case "basic", "bearer", "apikey":
		if strings.TrimSpace(secret) == "" {
			return RestAuth{}, fmt.Errorf("compiler: %s task %q uses %s auth but names no secret reference", kind, taskID, t)
		}
		if t == "apikey" && strings.TrimSpace(apiKeyName) == "" {
			return RestAuth{}, fmt.Errorf("compiler: %s task %q uses apiKey auth but names no header", kind, taskID)
		}
		return RestAuth{
			Type:       t,
			Username:   strings.TrimSpace(username),
			ApiKeyName: strings.TrimSpace(apiKeyName),
			SecretRef:  strings.TrimSpace(secret),
		}, nil
	default:
		return RestAuth{}, fmt.Errorf("compiler: %s task %q has an unsupported auth type %q", kind, taskID, authType)
	}
}

// Parse reads a BPMN 2.0 XML model and compiles the first <process> into an
// immutable CompiledProcess keyed by key at the given version. It is the front
// end to the linearizer (compiler.md stages 1–2 and 6): it parses the XML,
// resolves string element ids to integer indices, and pours the result into the
// shared Builder. Validation beyond reference integrity (reachability, gateway
// coverage) is a later stage.
//
// Service-task job types come from the Zeebe task-definition extension element
// (<zeebe:taskDefinition type="..." retries="..."/>), the de-facto standard for
// executable BPMN.
func Parse(key uint64, version int32, r io.Reader) (*CompiledProcess, error) {
	defs, docs, err := decodeDefinitions(r)
	if err != nil {
		return nil, err
	}
	if len(defs.Processes) == 0 {
		return nil, fmt.Errorf("compiler: no <process> element in definitions")
	}
	return compileProcess(key, version, defs.Processes[0], buildMessageResolver(defs), buildSignalResolver(defs), buildErrorResolver(defs), buildEscalationResolver(defs), buildOperationResolver(defs), buildItemTypeResolver(defs), buildDataStoreResolver(defs), docs)
}

// Deployable is one executable process compiled from a model, plus the display
// metadata a collaboration provides. PoolName is the participant (pool) name that
// references the process — "" for a standalone <process> outside any
// <collaboration>; ProcessName is the process's own name attribute.
type Deployable struct {
	Process     *CompiledProcess
	PoolName    string
	ProcessName string
}

// ParseAll compiles every executable process in a model — the collaboration case,
// where a <collaboration> has several <participant> pools, each referencing a
// <process>. A process is executable (and thus returned) iff it has a start
// event; a participant whose process is a black box (no start event, or none) is
// skipped rather than erroring, since a message-flow counterpart pool is often
// left unmodeled. The i-th executable process (document order) is keyed baseKey+i,
// so a caller assigning keys sequentially advances its counter by len(result). It
// errors only if the model has no executable process at all.
func ParseAll(baseKey uint64, version int32, r io.Reader) ([]Deployable, error) {
	defs, docs, err := decodeDefinitions(r)
	if err != nil {
		return nil, err
	}
	resolve := buildMessageResolver(defs)
	resolveSig := buildSignalResolver(defs)
	resolveErr := buildErrorResolver(defs)
	resolveEsc := buildEscalationResolver(defs)
	resolveOp := buildOperationResolver(defs)
	resolveItem := buildItemTypeResolver(defs)
	resolveStore := buildDataStoreResolver(defs)
	poolName := participantNames(defs)

	var out []Deployable
	for _, proc := range defs.Processes {
		if len(proc.StartEvents) == 0 {
			continue // black-box pool: nothing to run
		}
		cp, err := compileProcess(baseKey+uint64(len(out)), version, proc, resolve, resolveSig, resolveErr, resolveEsc, resolveOp, resolveItem, resolveStore, docs)
		if err != nil {
			return nil, err
		}
		out = append(out, Deployable{Process: cp, PoolName: poolName[proc.Id], ProcessName: proc.Name})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("compiler: no executable <process> (a process needs a start event)")
	}
	return out, nil
}

// ParseNamed compiles the single process with the given BPMN process id — a
// stored deployment records which process (by id) within its (possibly
// collaboration) XML it represents, so it can be recompiled under its original
// key. It compiles gate and all: a model that fails graph-wide validation is
// refused, as at any deploy. Bringing a definition back from a deployment store
// is the one case that wants the same compile *without* the gate, and uses
// [ReloadNamed] for it (ADR-0177).
func ParseNamed(key uint64, version int32, r io.Reader, processId string) (*CompiledProcess, error) {
	cp, _, err := parseNamed(key, version, r, processId, true)
	return cp, err
}

// ReloadNamed is ParseNamed for a definition that is already deployed: it compiles
// the named process *without* the deploy-time validation gate, and returns what
// that gate would have said about it today alongside the process
// (ADR-0177).
//
// Validation is a gate on deploying a model, not a condition for running one (I5,
// see validation.go): the compiled process is identical either way. A definition
// in a deployment store passed the gate of the day it was deployed and has been
// running under it since, so re-applying today's rules to it on every restart
// means a rule added to help authors can take a server down instead — on a model
// nobody touched, with every other definition and every running instance
// unreachable behind it. The rule still does its job at deploy, where the author
// is watching and can fix the model.
//
// The returned Problems are what the gate raised — the full list, warnings
// included, exactly as [ValidationError] carries it — and are empty when today's
// rules do not refuse the model at all, so a caller can report drift on len() > 0.
// A model that cannot be compiled at all still returns an error: there is no
// definition to bring back.
func ReloadNamed(key uint64, version int32, r io.Reader, processId string) (*CompiledProcess, []Problem, error) {
	return parseNamed(key, version, r, processId, false)
}

// parseNamed is the shared body of ParseNamed and ReloadNamed. gated says whether
// the deploy-time validation gate applies: it does on the deploy path, and it does
// not on the reload path, where the definition was gated once already.
func parseNamed(key uint64, version int32, r io.Reader, processId string, gated bool) (*CompiledProcess, []Problem, error) {
	defs, docs, err := decodeDefinitions(r)
	if err != nil {
		return nil, nil, err
	}
	for _, proc := range defs.Processes {
		if proc.Id != processId {
			continue
		}
		cp, err := compileProcess(key, version, proc, buildMessageResolver(defs), buildSignalResolver(defs), buildErrorResolver(defs), buildEscalationResolver(defs), buildOperationResolver(defs), buildItemTypeResolver(defs), buildDataStoreResolver(defs), docs)
		if err == nil {
			return cp, nil, nil
		}
		// Only the gate refused it: stage 5 runs on a process that is already built,
		// so this is the same definition the deploy of its day produced. Everything
		// else stopped before there was a process, and is an error on both paths.
		var ve *ValidationError
		if !gated && errors.As(err, &ve) && ve.Process != nil {
			return ve.Process, ve.Problems, nil
		}
		return nil, nil, err
	}
	return nil, nil, fmt.Errorf("compiler: no <process> with id %q in model", processId)
}

// nsBPMN is the BPMN 2.0 semantic-model namespace. Only a <documentation> in it (or in
// no namespace, as minimal hand-written models are written) is an element's own prose;
// one in a foreign namespace belongs to whatever extension declared it.
const nsBPMN = "http://www.omg.org/spec/BPMN/20100524/MODEL"

// decodeDefinitions parses a model into the typed structs *and* indexes every element's
// documentation by id (elementDocumentation). The model is read into memory first
// because those are two passes over the same bytes; deploy-time only, and every caller
// already holds the whole model in memory anyway.
func decodeDefinitions(r io.Reader) (xmlDefinitions, map[string]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return xmlDefinitions{}, nil, fmt.Errorf("compiler: read BPMN: %w", err)
	}
	var defs xmlDefinitions
	if err := xml.Unmarshal(data, &defs); err != nil {
		return xmlDefinitions{}, nil, fmt.Errorf("compiler: parse BPMN: %w", err)
	}
	return defs, elementDocumentation(data), nil
}

// elementDocumentation indexes each element's <bpmn:documentation> text by the id of the
// element that carries it. It is a generic token walk rather than a field on each of the
// ~25 element structs, because documentation is the one property *every* BPMN element may
// carry (ADR-0025) — a walk covers the elements Atlas compiles today and the ones it will
// compile tomorrow, with no per-type wiring to forget. An element with several
// <documentation> children (BPMN allows one per text format) has them joined by a blank
// line, in document order.
//
// It is best-effort: decodeDefinitions has already rejected malformed XML through the
// strict decode, so a token error here just ends the walk with what was found so far
// rather than failing a deploy over metadata.
func elementDocumentation(data []byte) map[string]string {
	docs := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var owners []string // the id of each open element ("" when it declares none)
	for {
		tok, err := dec.Token()
		if err != nil {
			return docs
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "documentation" && (t.Name.Space == nsBPMN || t.Name.Space == "") {
				var text string
				if err := dec.DecodeElement(&text, &t); err != nil {
					return docs
				}
				// DecodeElement consumed this element whole, including its end tag, so
				// it never enters the owner stack: the owner is the element around it.
				if len(owners) == 0 {
					continue
				}
				owner := owners[len(owners)-1]
				text = strings.TrimSpace(text)
				if owner == "" || text == "" {
					continue
				}
				if prev := docs[owner]; prev != "" {
					text = prev + "\n\n" + text
				}
				docs[owner] = text
				continue
			}
			owners = append(owners, attrValue(t, "id"))
		case xml.EndElement:
			if len(owners) > 0 {
				owners = owners[:len(owners)-1]
			}
		}
	}
}

// attrValue returns a start element's attribute by local name, or "" if it has none.
func attrValue(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// participantNames maps each referenced process id to its participant (pool) name.
func participantNames(defs xmlDefinitions) map[string]string {
	if defs.Collaboration == nil {
		return nil
	}
	m := make(map[string]string, len(defs.Collaboration.Participants))
	for _, p := range defs.Collaboration.Participants {
		if p.ProcessRef != "" {
			m[p.ProcessRef] = p.Name
		}
	}
	return m
}

// buildMessageResolver indexes a model's top-level <message> declarations and
// returns a resolver from a messageRef to the message's name and its compiled
// correlation-key expression. An empty correlation key compiles to nil, which
// evaluates to "" — matching only publishes with an empty key.
func buildMessageResolver(defs xmlDefinitions) func(ownerId, messageRef string) (string, *expr.Compiled, error) {
	messages := make(map[string]xmlMessage, len(defs.Messages))
	for _, m := range defs.Messages {
		if m.Id != "" {
			messages[m.Id] = m
		}
	}
	return func(ownerId, messageRef string) (string, *expr.Compiled, error) {
		m, ok := messages[messageRef]
		if !ok {
			return "", nil, fmt.Errorf("compiler: message event %q references unknown message %q", ownerId, messageRef)
		}
		if m.Name == "" {
			return "", nil, fmt.Errorf("compiler: message %q referenced by %q has no name", messageRef, ownerId)
		}
		var keyExpr *expr.Compiled
		if text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m.Subscription.CorrelationKey), "=")); text != "" {
			ce, err := expr.CompileAuto(text)
			if err != nil {
				return "", nil, fmt.Errorf("compiler: message %q correlationKey: %w", messageRef, err)
			}
			keyExpr = ce
		}
		return m.Name, keyExpr, nil
	}
}

// buildOperationResolver indexes a model's <interface> operations by id and returns a
// resolver from a send task's operationRef to the id of the operation's <inMessageRef>
// message (ADR-0112) — the message that send publishes. An unknown operation, or one with no
// inMessageRef, is a deploy error. The operation's outMessageRef (a response) is ignored: an
// operationRef send is a fire-and-forget throw, exactly like a messageRef send.
func buildOperationResolver(defs xmlDefinitions) func(ownerId, operationRef string) (string, error) {
	ops := make(map[string]string, len(defs.Interfaces))
	for _, iface := range defs.Interfaces {
		for _, op := range iface.Operations {
			if op.Id != "" {
				ops[op.Id] = strings.TrimSpace(op.InMessageRef)
			}
		}
	}
	return func(ownerId, operationRef string) (string, error) {
		msg, ok := ops[operationRef]
		if !ok {
			return "", fmt.Errorf("compiler: send task %q references unknown operation %q", ownerId, operationRef)
		}
		if msg == "" {
			return "", fmt.Errorf("compiler: operation %q referenced by send task %q has no inMessageRef", operationRef, ownerId)
		}
		return msg, nil
	}
}

// buildSignalResolver indexes a model's top-level <bpmn:signal> declarations by id and
// returns a closure resolving a signalRef to the signal's name (ADR-0088). A signal is
// broadcast by name, so — unlike a message — there is no correlation key to compile.
func buildSignalResolver(defs xmlDefinitions) func(ownerId, signalRef string) (string, error) {
	signals := make(map[string]xmlSignal, len(defs.Signals))
	for _, s := range defs.Signals {
		if s.Id != "" {
			signals[s.Id] = s
		}
	}
	return func(ownerId, signalRef string) (string, error) {
		s, ok := signals[signalRef]
		if !ok {
			return "", fmt.Errorf("compiler: signal event %q references unknown signal %q", ownerId, signalRef)
		}
		if s.Name == "" {
			return "", fmt.Errorf("compiler: signal %q referenced by %q has no name", signalRef, ownerId)
		}
		return s.Name, nil
	}
}

// buildErrorResolver indexes a model's top-level <bpmn:error> declarations by id and
// returns a closure resolving an errorRef to the error's code (ADR-0089). Errors match by
// code, not id or name, so the resolver returns the code. A code-less error — or an
// errorEventDefinition with no errorRef at all — resolves to "": a catch-all on a
// boundary/handler, and an uncoded throw on an error end event. Unlike a message or
// signal, an empty code is therefore legal; only a non-empty errorRef that names no
// declared error is a deploy error.
func buildErrorResolver(defs xmlDefinitions) func(ownerId, errorRef string) (string, error) {
	errs := make(map[string]xmlError, len(defs.Errors))
	for _, e := range defs.Errors {
		if e.Id != "" {
			errs[e.Id] = e
		}
	}
	return func(ownerId, errorRef string) (string, error) {
		if errorRef == "" {
			return "", nil // a code-less catch-all, or an uncoded error throw
		}
		e, ok := errs[errorRef]
		if !ok {
			return "", fmt.Errorf("compiler: error event %q references unknown error %q", ownerId, errorRef)
		}
		return e.ErrorCode, nil
	}
}

// buildEscalationResolver indexes a model's top-level <bpmn:escalation> declarations by id
// and returns a closure resolving an escalationRef to the escalation's code (ADR-0125).
// Escalations match by code, mirroring errors (buildErrorResolver): a code-less escalation —
// or an escalationEventDefinition with no escalationRef at all — resolves to "": a catch-all
// on a boundary/handler, and an uncoded throw on an escalation throw/end event. An empty
// code is legal; only a non-empty escalationRef that names no declared escalation is a
// deploy error.
func buildEscalationResolver(defs xmlDefinitions) func(ownerId, escalationRef string) (string, error) {
	escs := make(map[string]xmlEscalation, len(defs.Escalations))
	for _, e := range defs.Escalations {
		if e.Id != "" {
			escs[e.Id] = e
		}
	}
	return func(ownerId, escalationRef string) (string, error) {
		if escalationRef == "" {
			return "", nil // a code-less catch-all, or an uncoded escalation throw
		}
		e, ok := escs[escalationRef]
		if !ok {
			return "", fmt.Errorf("compiler: escalation event %q references unknown escalation %q", ownerId, escalationRef)
		}
		return e.EscalationCode, nil
	}
}

// compileProcess linearizes one <process> into an immutable CompiledProcess,
// resolving message, signal, and error references through resolveMessage/resolveSignal/
// resolveError (shared across a collaboration's processes). docs is the model-wide
// element-id → <bpmn:documentation> index (elementDocumentation), likewise shared: it is
// keyed by BPMN element id, and this process only ever looks up its own.
func compileProcess(key uint64, version int32, proc xmlProcess, resolveMessage func(ownerId, messageRef string) (string, *expr.Compiled, error), resolveSignal func(ownerId, signalRef string) (string, error), resolveError func(ownerId, errorRef string) (string, error), resolveEscalation func(ownerId, escalationRef string) (string, error), resolveOperation func(ownerId, operationRef string) (string, error), resolveItemType func(string) string, resolveDataStore func(ref, ownName string) string, docs map[string]string) (*CompiledProcess, error) {
	b := NewBuilder(key, proc.Id, version)
	b.SetDocumentation(docs[proc.Id]) // the process's own prose; "" interns to -1 (ADR-0025)
	// isExecutable defaults to true when the attribute is absent (BPMN convention;
	// Atlas has always run every deployed process), so an existing model without it
	// keeps working. Only an explicit "false" marks a process non-executable.
	b.SetExecutable(proc.IsExecutable != "false")
	if proc.VersionTag != "" {
		b.SetVersionTag(proc.VersionTag)
	}
	// A per-definition instance TTL (ADR-0085): parse the ISO-8601 duration up front so
	// a malformed value fails the deploy rather than silently disabling the bound.
	if ttl := strings.TrimSpace(proc.InstanceTtl); ttl != "" {
		nanos, err := parseISO8601Duration(ttl)
		if err != nil {
			return nil, fmt.Errorf("compiler: process %q: invalid instanceTtl %q: %w", proc.Id, ttl, err)
		}
		if nanos <= 0 {
			return nil, fmt.Errorf("compiler: process %q: instanceTtl %q must be a positive duration", proc.Id, ttl)
		}
		b.SetInstanceTtl(nanos)
	}
	// A per-definition history TTL (ADR-0144): how long a finished instance of this
	// definition is kept before retention hard-deletes it. Validated up front for the
	// same reason as the instance TTL — a typo must fail the deploy, not silently leave
	// the history unbounded.
	if ttl := strings.TrimSpace(proc.HistoryTtl); ttl != "" {
		nanos, err := parseISO8601Duration(ttl)
		if err != nil {
			return nil, fmt.Errorf("compiler: process %q: invalid historyTtl %q: %w", proc.Id, ttl, err)
		}
		if nanos <= 0 {
			return nil, fmt.Errorf("compiler: process %q: historyTtl %q must be a positive duration", proc.Id, ttl)
		}
		b.SetHistoryTtl(nanos)
	}
	// The searchable variable names (ADR-0244): resolved here, at
	// deploy time, so the runtime never parses the attribute and the engine asks the
	// compiled process one question per variable write (I5). A declaration that cannot
	// mean anything — a nameless entry, or the same name twice — fails the deploy, for
	// the same reason a malformed TTL does: it would otherwise index nothing and look
	// like it was working.
	if raw := strings.TrimSpace(proc.Searchable); raw != "" {
		seen := make(map[string]bool)
		var names []string
		for _, part := range strings.Split(raw, ",") {
			name := strings.TrimSpace(part)
			if name == "" {
				return nil, fmt.Errorf("compiler: process %q: searchable %q has an empty variable name", proc.Id, proc.Searchable)
			}
			if seen[name] {
				return nil, fmt.Errorf("compiler: process %q: searchable names %q twice", proc.Id, name)
			}
			seen[name] = true
			names = append(names, name)
		}
		b.SetSearchableVariables(names)
	}
	ids := make(map[string]int32, len(proc.StartEvents)+len(proc.ServiceTasks)+len(proc.EndEvents))
	reg := &registrar{b: b, ids: ids, docs: docs}

	// Fold <transaction> subprocesses into SubProcesses (marked IsTransaction) before any
	// scope walk, so a transaction is registered, wired, and validated as the subprocess it
	// structurally is (ADR-0108).
	foldTransactions(&proc.xmlFlowContent)
	// Resolve each send task's operationRef to the message the operation sends, before
	// registerScope, so an operationRef send is compiled as the message kind (ADR-0112).
	if err := resolveSendTaskOperations(&proc.xmlFlowContent, resolveOperation); err != nil {
		return nil, err
	}

	// Register every flow node — the process root and, recursively, each embedded
	// subprocess scope — then require a root-scope start event before wiring flows.
	// Data objects and I/O mappings below stay process-scoped (ADR-0074).
	walkErr := registerScope(b, reg, resolveMessage, resolveSignal, resolveError, resolveEscalation, &proc.xmlFlowContent)
	// A rejected id is reported ahead of whatever else the walk found. Every step
	// after registration resolves elements through ids, so an error raised once an
	// id has been rejected is more likely a consequence of that rejection than an
	// independent problem — and the author has to fix the id either way.
	if reg.err != nil {
		return nil, reg.err
	}
	if walkErr != nil {
		return nil, walkErr
	}

	if !b.hasStartEvent() {
		return nil, fmt.Errorf("compiler: process %q has no start event", proc.Id)
	}

	if err := connectScope(b, ids, &proc.xmlFlowContent); err != nil {
		return nil, err
	}

	// Resolve compensation links now that every node is registered (ADR-0103): join each
	// compensation boundary to its handler via a BPMN <association>, and narrow each
	// compensation throw/end that names a single activity. Both endpoints resolve through
	// the flat, process-wide id map, so this is a post-pass over the whole scope tree.
	if err := resolveCompensation(b, ids, &proc.xmlFlowContent); err != nil {
		return nil, err
	}

	// Record each flow node's organizational lane (ADR-0121). Lanes are metadata with no
	// execution effect, resolved through the same process-wide id map as compensation.
	if err := resolveLanes(b, ids, &proc.xmlFlowContent); err != nil {
		return nil, err
	}

	// Data objects are not flow nodes (no token flows through them), so they are
	// added as a separate collection, not registered as flow nodes (ADR-0053). A
	// nameless data object falls back to its BPMN id so it stays addressable.
	//
	// A data object's identity is its name: several <dataObjectReference>s (and even
	// several <dataObject> elements) sharing a name are *views* of one logical object
	// — the BPMN way to place it near several activities without long arrows. So every
	// id resolves through objName for association wiring, but the object is seeded only
	// once per name (the first occurrence wins its item type / initial state), so the
	// engine sees one object, not several.
	objName := make(map[string]string, len(proc.DataObjects)) // BPMN id → data-object name
	seededObj := make(map[string]bool, len(proc.DataObjects))
	for _, d := range proc.DataObjects {
		name := d.Name
		if name == "" {
			name = d.Id
		}
		objName[d.Id] = name
		if seededObj[name] {
			continue // already have a logical object of this name; this is another view
		}
		seededObj[name] = true
		b.AddDataObject(name, resolveItemType(d.ItemSubjectRef), d.DataState.Name, d.IsCollection)
	}

	// A <dataObjectReference> whose dataObjectRef names no <dataObject> still names an
	// object. A data object's identity is its name, and the *reference* is what carries
	// the name — along with the label, the data state and the shape on the diagram. The
	// declaration carries only the type, so when a tool loses one it loses the half a
	// model can do without, and nothing about what the model means is in doubt.
	//
	// Refusing the deploy over it would strand a diagram that reads correctly to
	// everybody looking at it, over an element nobody draws and nobody can see is
	// missing. So the reference stands in for its own declaration, which is the same
	// fallback a <dataStoreReference> naming no root element already gets. The type is
	// the one thing that cannot be recovered here — it was on the lost declaration —
	// so the object is seeded without one rather than with a guess.
	for _, ref := range proc.DataObjectReferences {
		if _, ok := objName[ref.DataObjectRef]; ok {
			continue // the declaration is there; nothing to stand in for
		}
		name := ref.Name
		if name == "" {
			name = ref.Id
		}
		if seededObj[name] {
			continue
		}
		seededObj[name] = true
		b.AddDataObject(name, "", ref.DataState.Name, false)
	}

	// Data stores: where this process says its data lives beyond one instance. Two
	// references to one store are two views of it on the diagram, so the process
	// names it once — the same folding the data objects above get, and for the same
	// reason.
	seenStore := make(map[string]bool, len(proc.DataStoreReferences))
	for _, ref := range proc.DataStoreReferences {
		name := resolveDataStore(ref.DataStoreRef, ref.Name)
		if name == "" {
			name = ref.Id
		}
		if seenStore[name] {
			continue
		}
		seenStore[name] = true
		b.AddDataStore(name, ref.Id)
	}

	// Wire data-output associations now that every activity node is registered
	// (ADR-0058). A dataObjectReference resolves to its data object plus the target
	// data state; a targetRef may also name a data object directly (no state change).
	refs := make(map[string]xmlDataObjectReference, len(proc.DataObjectReferences))
	for _, ref := range proc.DataObjectReferences {
		refs[ref.Id] = ref
	}
	// resolveDataTarget maps an association's ref to the data-object name it reads or
	// writes and the data state it moves the object into. A reference whose declaration
	// is gone resolves to its own name, matching the object seeded for it above.
	resolveDataTarget := func(ownerId, ref string) (name, state string, err error) {
		if r, ok := refs[ref]; ok {
			if name, ok := objName[r.DataObjectRef]; ok {
				return name, r.DataState.Name, nil
			}
			name := r.Name
			if name == "" {
				name = r.Id
			}
			return name, r.DataState.Name, nil
		}
		if name, ok := objName[ref]; ok {
			return name, "", nil // names the object directly; no state change
		}
		return "", "", fmt.Errorf("compiler: data association on %q names %q, which is neither a data object nor a data object reference", ownerId, ref)
	}
	// The four wiring passes below each repeat one shape at every activity kind they
	// handle — around thirty call sites of "wire it, propagate the rejection". keepWire
	// holds the first rejection instead, so each call site is one statement and the
	// passes are checked once at the end.
	//
	// Like the registrar's, the passes carry on after a rejection: registration already
	// succeeded, so every remaining association still wires against a complete id table
	// and no later pass can report a second failure caused by the first. The kept one is
	// the first, and nothing between here and the check can return ahead of it.
	var wireErr error
	keepWire := func(err error) {
		if err != nil && wireErr == nil {
			wireErr = err
		}
	}
	wireDataOut := func(ownerId string, assocs []xmlDataOutputAssociation) {
		for _, a := range assocs {
			name, state, err := resolveDataTarget(ownerId, a.TargetRef)
			if err != nil {
				keepWire(fmt.Errorf("compiler: data output association on %q target: %w", ownerId, err))
				return
			}
			var valExpr *expr.Compiled
			if from := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(a.Assignment.From), "=")); from != "" {
				ce, err := expr.CompileAuto(from)
				if err != nil {
					keepWire(fmt.Errorf("compiler: data output association on %q assignment: %w", ownerId, err))
					return
				}
				valExpr = ce
			}
			b.AddDataOutputAssociation(ids[ownerId], name, valExpr, state, strings.TrimSpace(a.Assignment.To))
		}
	}
	// Every scope, recursively — like wireScopeIO below, and for the same reason: a
	// data association is drawn on the activity, and an activity may sit in any scope
	// the model nests. Walking only the process root's element lists dropped every
	// association inside a subprocess at compile time, with no error and no warning:
	// the model deployed, ran, and quietly wrote nothing.
	forEachDataAssociated(&proc.xmlFlowContent, func(ownerId string, out []xmlDataOutputAssociation, _ []xmlDataInputAssociation) {
		wireDataOut(ownerId, out)
	})

	// Wire data-input associations: a sourceRef names the data object read (resolved
	// like an output target, its state ignored on a read); a targetRef is the process
	// variable the read value is written into (ADR-0059).
	wireDataIn := func(ownerId string, assocs []xmlDataInputAssociation) {
		for _, a := range assocs {
			name, _, err := resolveDataTarget(ownerId, a.SourceRef)
			if err != nil {
				keepWire(fmt.Errorf("compiler: data input association on %q source: %w", ownerId, err))
				return
			}
			// The target variable is the assignment's <to> (a free string the Modeler
			// writes — a drawn association's own <targetRef> is a generated data-input
			// property id, not a variable name). A hand-authored <targetRef> is the
			// fallback when no <to> is given.
			variable := strings.TrimSpace(a.Assignment.To)
			if variable == "" {
				variable = strings.TrimSpace(a.TargetRef)
			}
			if variable == "" {
				keepWire(fmt.Errorf("compiler: data input association on %q has no target variable (set the assignment's <to>)", ownerId))
				return
			}
			var valExpr *expr.Compiled
			if from := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(a.Assignment.From), "=")); from != "" {
				ce, err := expr.CompileAuto(from)
				if err != nil {
					keepWire(fmt.Errorf("compiler: data input association on %q assignment: %w", ownerId, err))
					return
				}
				valExpr = ce
			}
			b.AddDataInputAssociation(ids[ownerId], name, variable, valExpr)
		}
	}
	forEachDataAssociated(&proc.xmlFlowContent, func(ownerId string, _ []xmlDataOutputAssociation, in []xmlDataInputAssociation) {
		wireDataIn(ownerId, in)
	})

	// Wire generic zeebe:ioMapping input/output mappings (ADR-0068). Each source is a
	// FEEL expression compiled once at deploy time (invariant I5); an empty target or
	// an uncompilable source fails the deploy, exactly like a bad script-task
	// expression. The compiler only records them here; the engine applies them.
	compileSource := func(ownerId, dir, target, source string) (*expr.Compiled, error) {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(source), "="))
		if text == "" {
			return nil, fmt.Errorf("compiler: task %q ioMapping %s for %q has no source expression", ownerId, dir, target)
		}
		e, err := expr.CompileAuto(text)
		if err != nil {
			return nil, fmt.Errorf("compiler: task %q ioMapping %s for %q: %w", ownerId, dir, target, err)
		}
		return e, nil
	}
	wireIO := func(ownerId string, iom xmlZeebeIOMapping) {
		for _, in := range iom.Inputs {
			target := strings.TrimSpace(in.Target)
			if target == "" {
				keepWire(fmt.Errorf("compiler: task %q has an ioMapping input with no target", ownerId))
				return
			}
			e, err := compileSource(ownerId, "input", target, in.Source)
			if err != nil {
				keepWire(err)
				return
			}
			b.AddInputMapping(ids[ownerId], target, e)
		}
		for _, out := range iom.Outputs {
			target := strings.TrimSpace(out.Target)
			if target == "" {
				keepWire(fmt.Errorf("compiler: task %q has an ioMapping output with no target", ownerId))
				return
			}
			e, err := compileSource(ownerId, "output", target, out.Source)
			if err != nil {
				keepWire(err)
				return
			}
			b.AddOutputMapping(ids[ownerId], target, e)
		}
	}
	// Wire I/O mappings for every scope, recursively: a subprocess's own ioMapping
	// (input mappings write its scope on entry, output mappings promote to the
	// parent on completion — the engine applies both generically) and the mappings
	// on the activities inside it (ADR-0074 Phase 4).
	var wireScopeIO func(c *xmlFlowContent)
	wireScopeIO = func(c *xmlFlowContent) {
		for _, st := range c.ServiceTasks {
			wireIO(st.Id, st.IOMapping)
		}
		for _, st := range c.ScriptTasks {
			wireIO(st.Id, st.IOMapping)
		}
		for _, ut := range c.UserTasks {
			wireIO(ut.Id, ut.IOMapping)
		}
		for _, ca := range c.CallActivities {
			wireIO(ca.Id, ca.IOMapping)
		}
		for _, rt := range c.ReceiveTasks {
			wireIO(rt.Id, rt.IOMapping)
		}
		for _, st := range c.SendTasks {
			if strings.TrimSpace(st.MessageRef) != "" {
				continue // a message-kind send task is a throw, not an activity (ADR-0112)
			}
			wireIO(st.Id, st.IOMapping)
		}
		for i := range c.SubProcesses {
			sub := &c.SubProcesses[i]
			wireIO(sub.Id, sub.IOMapping)
			wireScopeIO(&sub.xmlFlowContent)
		}
		// An ad-hoc subprocess is a scope too: recurse so its contained activities'
		// I/O mappings are wired like any other scope's (ADR-0138).
		for i := range c.AdHocSubProcesses {
			wireScopeIO(&c.AdHocSubProcesses[i].xmlFlowContent)
		}
	}
	wireScopeIO(&proc.xmlFlowContent)

	// Wire the loop characteristics of every activity in the scope tree, recursively,
	// mirroring wireScopeIO — both BPMN markers: multi-instance (ADR-0077) and standard
	// loop (ADR-0133). Each FEEL source compiles once at deploy (I5), and a loop with no
	// way to decide how many iterations to run is refused (a multi-instance with neither
	// an input collection nor a cardinality; a standard loop with neither a condition nor
	// a maximum). The compiler records the detail; the engine runs the iterations.
	compileMI := func(ownerId, what, source string) (*expr.Compiled, error) {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(source), "="))
		if text == "" {
			return nil, nil
		}
		e, err := expr.CompileAuto(text)
		if err != nil {
			return nil, fmt.Errorf("compiler: loop %s on %q: %w", what, ownerId, err)
		}
		return e, nil
	}
	// A standard loop is the other BPMN loop marker (ADR-0133): it repeats the activity
	// while its condition holds rather than once per collection element. It compiles into
	// the same loop table (the engine runs it on the multi-instance body/iteration
	// machinery), so an activity carries at most one of the two markers, and a loop with
	// neither a condition nor a maximum is refused — it could never end.
	wireStandardLoop := func(ownerId string, sl *xmlStandardLoop) error {
		if sl == nil {
			return nil
		}
		cond, err := compileMI(ownerId, "loopCondition", sl.LoopCondition)
		if err != nil {
			return err
		}
		var max int32
		if text := strings.TrimSpace(sl.LoopMaximum); text != "" {
			n, err := strconv.Atoi(text)
			if err != nil || n <= 0 {
				return fmt.Errorf("compiler: loop activity %q has an invalid loopMaximum %q (want a positive whole number)", ownerId, sl.LoopMaximum)
			}
			max = int32(n)
		}
		if cond == nil && max == 0 {
			return fmt.Errorf("compiler: loop activity %q needs a loopCondition or a loopMaximum", ownerId)
		}
		b.SetStandardLoop(ids[ownerId], strings.TrimSpace(sl.TestBefore) == "true", max, cond)
		return nil
	}
	wireMultiInstance := func(ownerId string, mi *xmlMultiInstance) error {
		if mi == nil {
			return nil
		}
		coll, err := compileMI(ownerId, "inputCollection", mi.Loop.InputCollection)
		if err != nil {
			return err
		}
		card, err := compileMI(ownerId, "loopCardinality", mi.LoopCardinality)
		if err != nil {
			return err
		}
		if coll == nil && card == nil {
			return fmt.Errorf("compiler: multi-instance activity %q needs an inputCollection or a loopCardinality", ownerId)
		}
		if coll != nil && card != nil {
			return fmt.Errorf("compiler: multi-instance activity %q has both an inputCollection and a loopCardinality (use one)", ownerId)
		}
		out, err := compileMI(ownerId, "outputElement", mi.Loop.OutputElement)
		if err != nil {
			return err
		}
		cond, err := compileMI(ownerId, "completionCondition", mi.CompletionCondition)
		if err != nil {
			return err
		}
		b.SetMultiInstance(ids[ownerId], mi.IsSequential == "true",
			strings.TrimSpace(mi.Loop.InputElement), strings.TrimSpace(mi.Loop.OutputCollection),
			coll, card, out, cond)
		return nil
	}
	// An activity carries at most one loop marker: BPMN draws them as different icons
	// (∥/≡ for a multi-instance, ↻ for a standard loop) and they mean different things,
	// so a model with both is refused rather than silently running one of them.
	wireLoop := func(ownerId string, mi *xmlMultiInstance, sl *xmlStandardLoop) {
		if mi != nil && sl != nil {
			keepWire(fmt.Errorf("compiler: activity %q has both a multiInstanceLoopCharacteristics and a standardLoopCharacteristics (use one)", ownerId))
			return
		}
		if err := wireMultiInstance(ownerId, mi); err != nil {
			keepWire(err)
			return
		}
		keepWire(wireStandardLoop(ownerId, sl))
	}
	var wireScopeMI func(c *xmlFlowContent)
	wireScopeMI = func(c *xmlFlowContent) {
		for _, st := range c.ServiceTasks {
			wireLoop(st.Id, st.MultiInstance, st.StandardLoop)
		}
		for _, st := range c.ScriptTasks {
			wireLoop(st.Id, st.MultiInstance, st.StandardLoop)
		}
		for _, ut := range c.UserTasks {
			wireLoop(ut.Id, ut.MultiInstance, ut.StandardLoop)
		}
		for _, ca := range c.CallActivities {
			wireLoop(ca.Id, ca.MultiInstance, ca.StandardLoop)
		}
		for _, rt := range c.ReceiveTasks {
			wireLoop(rt.Id, rt.MultiInstance, rt.StandardLoop)
		}
		for _, brt := range c.BusinessRuleTasks {
			wireLoop(brt.Id, brt.MultiInstance, brt.StandardLoop)
		}
		// An undefined task and a manual task have no implementation, but they are
		// activities: a loop marker on one repeats the (pass-through) step, which is what
		// the diagram says it does.
		for _, t := range c.Tasks {
			wireLoop(t.Id, t.MultiInstance, t.StandardLoop)
		}
		for _, t := range c.ManualTasks {
			wireLoop(t.Id, t.MultiInstance, t.StandardLoop)
		}
		for _, st := range c.SendTasks {
			if strings.TrimSpace(st.MessageRef) != "" {
				continue // a message-kind send task is a throw, not an activity (ADR-0112)
			}
			wireLoop(st.Id, st.MultiInstance, st.StandardLoop)
		}
		for i := range c.SubProcesses {
			sub := &c.SubProcesses[i]
			wireLoop(sub.Id, sub.MultiInstance, sub.StandardLoop)
			wireScopeMI(&sub.xmlFlowContent)
		}
		// Recurse into ad-hoc scopes so a contained activity's loop markers are wired
		// exactly as in any other scope (ADR-0138).
		for i := range c.AdHocSubProcesses {
			wireScopeMI(&c.AdHocSubProcesses[i].xmlFlowContent)
		}
	}
	wireScopeMI(&proc.xmlFlowContent)

	// The four wiring passes are done; report the first thing any of them rejected.
	// Nothing between the first pass and here returns an error of its own, so this is
	// the same error, at the same point in the deploy, that returning from the failing
	// call site used to produce.
	if wireErr != nil {
		return nil, wireErr
	}

	cp, err := b.Build()
	if err != nil {
		return nil, err
	}
	// Stage 5 (compiler.md): graph-wide validation. An error-severity Problem
	// refuses the deploy, preserving the compile-gate contract (invariant I5) — the
	// same fatal-at-deploy behavior structural parse errors already have, now for
	// graph-level faults (an unroutable gateway, a boundary event on a non-activity).
	// Warnings (e.g. unreachable dead code) are non-fatal and surface through the
	// future /validate dry-run (ADR-0026), not here.
	if problems := Validate(cp); HasErrors(problems) {
		return nil, &ValidationError{Problems: problems, Process: cp}
	}
	return cp, nil
}

// BPMN XML is matched by element/attribute local name, so namespace prefixes
// (bpmn:, zeebe:) are handled transparently by encoding/xml.

type xmlDefinitions struct {
	Processes       []xmlProcess        `xml:"process"`
	ItemDefinitions []xmlItemDefinition `xml:"itemDefinition"`
	DataStores      []xmlDataStore      `xml:"dataStore"`
	Messages        []xmlMessage        `xml:"message"`
	Signals         []xmlSignal         `xml:"signal"`
	Errors          []xmlError          `xml:"error"`
	Escalations     []xmlEscalation     `xml:"escalation"`
	Interfaces      []xmlInterface      `xml:"interface"`
	Collaboration   *xmlCollaboration   `xml:"collaboration"`
}

// A BPMN <itemDefinition> declares a data structure a data object can be typed
// with. Atlas reads two things off it: its id, which is what an itemSubjectRef
// references, and its structureRef, which names the structure — the class in the
// application's information model (ADR-0230).
//
// The indirection is worth carrying rather than reading the reference as the name
// directly, because the id is an XML id and the name is not: a class called "Line
// item" cannot be an id, and the shorthand `itemSubjectRef="Line item"` a
// hand-written model might use is not valid BPMN. The structureRef has no such
// constraint, so the definition is what makes an arbitrary class name expressible.
type xmlItemDefinition struct {
	Id           string              `xml:"id,attr"`
	StructureRef string              `xml:"structureRef,attr"`
	Properties   []xmlVendorProperty `xml:"extensionElements>property"`
}

// A vendor <property name="…" value="…"> inside an element's <extensionElements>.
//
// BPMN gives an <itemDefinition> no name of its own — a root element carries an id
// and nothing else — so structureRef is the only place the specification offers for
// the name of the thing being declared. A tool that does not use it has to invent
// somewhere, and MID Innovator (bpanda) invents here: its itemDefinitions are a bare
// GUID id with <bpanda:property name="Name" value="Incident"/> beside it. Reading
// the property is what keeps such a model from reporting a GUID as the declared
// type of every data object in it, against a class name nobody could then model.
type xmlVendorProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// typeName is the class name an <itemDefinition> declares: its structureRef, and
// when it declares none, a vendor property called "Name". Empty when the definition
// names nothing at all, which leaves its id as the only handle there is.
func (it xmlItemDefinition) typeName() string {
	if it.StructureRef != "" {
		return it.StructureRef
	}
	for _, p := range it.Properties {
		if strings.EqualFold(p.Name, "Name") && p.Value != "" {
			return p.Value
		}
	}
	return ""
}

// A BPMN <interface> groups <operation>s (the WSDL-style service-interface model). Atlas
// reads them only to resolve a send task's operationRef to the operation's inMessageRef
// (ADR-0112) — the message that send publishes.
type xmlInterface struct {
	Operations []xmlOperation `xml:"operation"`
}

// A BPMN <operation> inside an <interface>. Its <inMessageRef> names the message an
// operationRef send task publishes; its outMessageRef (a response) is not supported.
type xmlOperation struct {
	Id           string `xml:"id,attr"`
	InMessageRef string `xml:"inMessageRef"`
}

// buildDataStoreResolver maps a <dataStoreReference>'s dataStoreRef onto the store's
// own name. A reference naming no root element falls back to its own name, which is
// the shorthand a tool that only draws the box produces and still a usable statement
// about where data lives.
func buildDataStoreResolver(defs xmlDefinitions) func(ref, ownName string) string {
	byID := make(map[string]string, len(defs.DataStores))
	for _, st := range defs.DataStores {
		if st.Id == "" {
			continue
		}
		name := st.Name
		if name == "" {
			name = st.Id
		}
		byID[st.Id] = name
	}
	return func(ref, ownName string) string {
		if name, ok := byID[ref]; ok {
			return name
		}
		return ownName
	}
}

// buildItemTypeResolver maps a data object's itemSubjectRef onto the type name it
// means: the referenced <itemDefinition>'s structureRef, the name it carries in a
// vendor property when it declares no structureRef, its id when it names itself
// nowhere, and — when the reference names no itemDefinition at all — the reference
// itself.
//
// That last case is not a fallback for broken models; it is the shorthand every
// hand-written Atlas model and fixture uses, and it stays supported. What the
// indirection adds is the ability for a *tool* to write the reference the
// specification actually asks for, which is also the only form the bpmn-js moddle
// will round-trip: an unresolvable itemSubjectRef is dropped on export, so a model
// edited in the Modeler would silently lose its types without this.
func buildItemTypeResolver(defs xmlDefinitions) func(string) string {
	if len(defs.ItemDefinitions) == 0 {
		return func(ref string) string { return ref }
	}
	byID := make(map[string]string, len(defs.ItemDefinitions))
	for _, it := range defs.ItemDefinitions {
		if it.Id == "" {
			continue
		}
		if name := it.typeName(); name != "" {
			byID[it.Id] = name
			continue
		}
		byID[it.Id] = it.Id
	}
	return func(ref string) string {
		if name, ok := byID[ref]; ok {
			return name
		}
		return ref
	}
}

// A collaboration groups participant pools. Each participant references the
// <process> it contains; the participant carries the pool's display name (a
// process in a collaboration is often unnamed, the pool is what's labelled).
type xmlCollaboration struct {
	Participants []xmlParticipant `xml:"participant"`
}

type xmlParticipant struct {
	Id         string `xml:"id,attr"`
	Name       string `xml:"name,attr"`
	ProcessRef string `xml:"processRef,attr"`
}

// A top-level message declaration. Its Zeebe subscription carries the FEEL
// correlationKey expression shared by every catch/throw event that references it.
type xmlMessage struct {
	Id           string               `xml:"id,attr"`
	Name         string               `xml:"name,attr"`
	Subscription xmlZeebeSubscription `xml:"extensionElements>subscription"`
}

type xmlZeebeSubscription struct {
	CorrelationKey string `xml:"correlationKey,attr"`
}

type xmlMessageEventDefinition struct {
	MessageRef string `xml:"messageRef,attr"`
}

// A top-level signal declaration (ADR-0088). A signal is broadcast by name — it carries
// no correlation key and no code — so it needs only an id and a name.
type xmlSignal struct {
	Id   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type xmlSignalEventDefinition struct {
	SignalRef string `xml:"signalRef,attr"`
}

// A top-level error declaration (ADR-0089). An error is caught by its code — the
// nearest enclosing handler whose error code matches, or a code-less catch-all — so the
// errorCode, not the id or name, is what an error boundary/handler compares against. The
// id is what an errorRef points at; the name is human-facing only.
type xmlError struct {
	Id        string `xml:"id,attr"`
	ErrorCode string `xml:"errorCode,attr"`
	Name      string `xml:"name,attr"`
}

type xmlErrorEventDefinition struct {
	ErrorRef string `xml:"errorRef,attr"`
}

// A top-level escalation declaration (ADR-0125). Like an error, an escalation is caught by
// its code — the nearest enclosing escalation handler whose escalationCode matches, or a
// code-less catch-all — so the escalationCode, not the id or name, is what an escalation
// boundary/handler compares against. The id is what an escalationRef points at; the name is
// human-facing only.
type xmlEscalation struct {
	Id             string `xml:"id,attr"`
	EscalationCode string `xml:"escalationCode,attr"`
	Name           string `xml:"name,attr"`
}

type xmlEscalationEventDefinition struct {
	EscalationRef string `xml:"escalationRef,attr"`
}

type xmlProcess struct {
	Id           string `xml:"id,attr"`
	Name         string `xml:"name,attr"`
	IsExecutable string `xml:"isExecutable,attr"`
	VersionTag   string `xml:"versionTag,attr"`
	InstanceTtl  string `xml:"instanceTtl,attr"` // ISO-8601 duration; self-cleaning TTL (ADR-0085), empty = off
	HistoryTtl   string `xml:"historyTtl,attr"`  // ISO-8601 duration; finished-instance retention (ADR-0144), empty = off
	// Searchable is a comma-separated list of variable names this process wants to be
	// findable by. It is a declaration, not a hint: the value index is maintained only
	// for these names, because indexing every value would double the write path and
	// index JSON blobs. Empty = nothing indexed, and the process pays nothing.
	Searchable string `xml:"searchable,attr"`

	xmlFlowContent // the process root's flow nodes and sequence flows

	DataObjects          []xmlDataObject          `xml:"dataObject"`
	DataStoreReferences  []xmlDataStoreReference  `xml:"dataStoreReference"`
	DataObjectReferences []xmlDataObjectReference `xml:"dataObjectReference"`
}

// xmlFlowContent is the set of flow nodes and sequence flows of one scope — the
// process root or an embedded subprocess. Both xmlProcess and xmlSubProcess embed
// it, so a subprocess nests the very same element shapes (ADR-0074). encoding/xml
// promotes the embedded fields, so a <subProcess>'s own <startEvent>, <task>,
// <sequenceFlow>, … unmarshal exactly as they do at the process root.
type xmlFlowContent struct {
	StartEvents       []xmlStartEvent       `xml:"startEvent"`
	EndEvents         []xmlEndEvent         `xml:"endEvent"`
	ServiceTasks      []xmlServiceTask      `xml:"serviceTask"`
	ScriptTasks       []xmlScriptTask       `xml:"scriptTask"`
	BusinessRuleTasks []xmlBusinessRuleTask `xml:"businessRuleTask"`
	ExclusiveGateways []xmlExclusiveGateway `xml:"exclusiveGateway"`

	IntermediateCatchEvents []xmlIntermediateCatchEvent `xml:"intermediateCatchEvent"`
	IntermediateThrowEvents []xmlIntermediateThrowEvent `xml:"intermediateThrowEvent"`
	BoundaryEvents          []xmlBoundaryEvent          `xml:"boundaryEvent"`

	Flows []xmlSequenceFlow `xml:"sequenceFlow"`

	Tasks              []xmlPlainTask        `xml:"task"`
	ManualTasks        []xmlPlainTask        `xml:"manualTask"`
	ParallelGateways   []xmlNode             `xml:"parallelGateway"`
	InclusiveGateways  []xmlInclusiveGateway `xml:"inclusiveGateway"`
	EventBasedGateways []xmlNode             `xml:"eventBasedGateway"` // deferred choice; only its id matters (ADR-0110)

	UserTasks []xmlUserTask `xml:"userTask"`

	SubProcesses   []xmlSubProcess   `xml:"subProcess"`
	CallActivities []xmlCallActivity `xml:"callActivity"`

	// Transactions are <transaction> subprocesses — structurally an embedded subprocess with
	// one added outcome, cancellation (ADR-0108). They share xmlSubProcess's shape; foldTransactions
	// merges them into SubProcesses (marked IsTransaction) right after parse, so every scope walk
	// that already handles subprocesses handles transactions unchanged.
	Transactions []xmlSubProcess `xml:"transaction"`

	ReceiveTasks []xmlReceiveTask `xml:"receiveTask"`

	// A send task is a service task under a different BPMN label (ADR-0112): it creates a
	// job and waits, carrying the same taskDefinition, worker extensions, and activity
	// sub-elements. It parses into the very same shape, so xmlSendTask is an alias.
	SendTasks []xmlSendTask `xml:"sendTask"`

	// Captured only to give a clear "unsupported element" error (see registerScope) rather
	// than a confusing "unknown targetRef" when a flow points at one; not executable.
	ComplexGateways []xmlNode `xml:"complexGateway"`

	// AdHocSubProcesses are <adHocSubProcess> containers: a scope whose contained activities
	// run on demand, in any order, zero or more times, rather than being driven by sequence
	// flow from a start event (ADR-0138). They share xmlSubProcess's shape (they nest the same
	// flow content) plus the ad-hoc's own ordering / cancelRemainingInstances /
	// <completionCondition>.
	AdHocSubProcesses []xmlAdHocSubProcess `xml:"adHocSubProcess"`

	// Associations are BPMN <association> artifacts declared in this scope. Atlas reads
	// them only to link a compensation boundary event to its handler (ADR-0103).
	Associations []xmlAssociation `xml:"association"`

	// LaneSets partition this scope's flow nodes into organizational lanes (ADR-0121).
	// Lanes are metadata with no execution effect; resolveLanes records each node's lane.
	LaneSets []xmlLaneSet `xml:"laneSet"`
}

// forEachDataAssociated calls fn once for every activity in the scope tree rooted at c
// that may carry data associations (ADR-0058/0059), handing it the activity's BPMN id
// and its <dataOutputAssociation>/<dataInputAssociation> lists. It recurses into every
// nested scope — an embedded subprocess (a <transaction> among them, folded in before
// this runs), an event subprocess, an ad-hoc subprocess — because an association is
// drawn on the activity, and the activity may sit in any of them.
//
// It is the data-association twin of wireScopeIO's walk, and the two must stay in step:
// they once did not, and the shape of that failure is worth remembering. I/O mappings
// recursed and data associations did not, so an activity inside a subprocess kept its
// zeebe:ioMapping and silently lost its associations. Nothing rejected the model — it
// deployed, started, and ran to the end writing nothing into the data object.
//
// Only the eight activity kinds whose XML shape carries the two elements are visited;
// a subprocess and a call activity do not parse them today, so they are containers here
// and nothing else. A send task naming a message is skipped: it compiles to a throw
// event, not an activity (ADR-0112), so there is no activity node to wire onto.
func forEachDataAssociated(c *xmlFlowContent, fn func(ownerId string, out []xmlDataOutputAssociation, in []xmlDataInputAssociation)) {
	for _, st := range c.ServiceTasks {
		fn(st.Id, st.DataOut, st.DataIn)
	}
	for _, st := range c.ScriptTasks {
		fn(st.Id, st.DataOut, st.DataIn)
	}
	for _, brt := range c.BusinessRuleTasks {
		fn(brt.Id, brt.DataOut, brt.DataIn)
	}
	for _, ut := range c.UserTasks {
		fn(ut.Id, ut.DataOut, ut.DataIn)
	}
	for _, t := range c.Tasks {
		fn(t.Id, t.DataOut, t.DataIn)
	}
	for _, t := range c.ManualTasks {
		fn(t.Id, t.DataOut, t.DataIn)
	}
	for _, rt := range c.ReceiveTasks {
		fn(rt.Id, rt.DataOut, rt.DataIn)
	}
	for _, st := range c.SendTasks {
		if strings.TrimSpace(st.MessageRef) != "" {
			continue // a message-kind send task is a throw, not an activity (ADR-0112)
		}
		fn(st.Id, st.DataOut, st.DataIn)
	}
	for i := range c.SubProcesses {
		forEachDataAssociated(&c.SubProcesses[i].xmlFlowContent, fn)
	}
	for i := range c.AdHocSubProcesses {
		forEachDataAssociated(&c.AdHocSubProcesses[i].xmlFlowContent, fn)
	}
}

// xmlLaneSet is a <laneSet> — a set of sibling lanes partitioning a process or subprocess scope.
type xmlLaneSet struct {
	Lanes []xmlLane `xml:"lane"`
}

// xmlLane is a <lane> — an organizational partition naming the flow nodes it contains via
// <flowNodeRef> children, optionally subdivided into a nested <childLaneSet> (ADR-0121).
type xmlLane struct {
	Id           string      `xml:"id,attr"`
	Name         string      `xml:"name,attr"`
	FlowNodeRefs []string    `xml:"flowNodeRef"`
	ChildLaneSet *xmlLaneSet `xml:"childLaneSet"`
}

// laneLabel is a lane's name, or its id when unnamed — for error messages.
func laneLabel(l *xmlLane) string {
	if l.Name != "" {
		return l.Name
	}
	return l.Id
}

// resolveLanes records each flow node's organizational lane (ADR-0121). It walks every scope's
// laneSets — following nested childLaneSets and building the lane table with parent pointers — and
// resolves each <flowNodeRef> to a node through the process-wide id map, so a lane's assignment
// works regardless of scope. A flowNodeRef naming no registered node, or a node claimed by two
// lanes, is a deploy error. Lanes are pure metadata, so this runs after registration and changes
// no flow; a process with no lanes leaves every node's Lane at -1.
func resolveLanes(b *Builder, ids map[string]int32, fc *xmlFlowContent) error {
	assigned := map[int32]string{} // node id → the lane that already claimed it (process-wide)

	var walkLaneSet func(ls *xmlLaneSet, parent int32) error
	walkLaneSet = func(ls *xmlLaneSet, parent int32) error {
		for i := range ls.Lanes {
			lane := &ls.Lanes[i]
			idx := b.AddLane(lane.Name, parent)
			for _, ref := range lane.FlowNodeRefs {
				ref = strings.TrimSpace(ref)
				if ref == "" {
					continue
				}
				node, ok := ids[ref]
				if !ok {
					return fmt.Errorf("compiler: lane %q references unknown flow node %q", laneLabel(lane), ref)
				}
				if prev, dup := assigned[node]; dup {
					return fmt.Errorf("compiler: flow node %q is in two lanes (%q and %q); a node belongs to at most one lane", ref, prev, laneLabel(lane))
				}
				assigned[node] = laneLabel(lane)
				b.SetLane(node, idx)
			}
			if lane.ChildLaneSet != nil {
				if err := walkLaneSet(lane.ChildLaneSet, idx); err != nil {
					return err
				}
			}
		}
		return nil
	}

	var walkScope func(c *xmlFlowContent) error
	walkScope = func(c *xmlFlowContent) error {
		for i := range c.LaneSets {
			if err := walkLaneSet(&c.LaneSets[i], -1); err != nil {
				return err
			}
		}
		for i := range c.SubProcesses {
			if err := walkScope(&c.SubProcesses[i].xmlFlowContent); err != nil {
				return err
			}
		}
		for i := range c.AdHocSubProcesses {
			if err := walkScope(&c.AdHocSubProcesses[i].xmlFlowContent); err != nil {
				return err
			}
		}
		return nil
	}
	return walkScope(fc)
}

// xmlReceiveTask is a <receiveTask messageRef="…">: an activity that waits for the
// referenced message to correlate, then continues (ADR-0102). It carries the same activity
// sub-elements as a service task — I/O mappings, multi-instance, and data associations — so
// it is a first-class activity.
type xmlReceiveTask struct {
	Id            string                     `xml:"id,attr"`
	MessageRef    string                     `xml:"messageRef,attr"`
	IOMapping     xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop  *xmlStandardLoop           `xml:"standardLoopCharacteristics"`
	DataOut       []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn        []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

// xmlCallActivity is a <callActivity>: it starts a separate process (named by
// <zeebe:calledElement processId=…>) as a child instance and passes variables via
// its <zeebe:ioMapping> and the propagation flags (ADR-0076).
type xmlCallActivity struct {
	Id            string            `xml:"id,attr"`
	Name          string            `xml:"name,attr"`
	CalledElement xmlZeebeCalledEl  `xml:"extensionElements>calledElement"`
	IOMapping     xmlZeebeIOMapping `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop  *xmlStandardLoop  `xml:"standardLoopCharacteristics"`
}

// xmlZeebeCalledEl is the <zeebe:calledElement> of a call activity: the called
// process id, its version binding, and the two variable-propagation flags (each
// defaults to true when the attribute is absent, matching Zeebe).
type xmlZeebeCalledEl struct {
	ProcessId                   string `xml:"processId,attr"`
	BindingType                 string `xml:"bindingType,attr"`
	PropagateAllParentVariables string `xml:"propagateAllParentVariables,attr"`
	PropagateAllChildVariables  string `xml:"propagateAllChildVariables,attr"`
}

// xmlMultiInstance is a <multiInstanceLoopCharacteristics> on an activity (ADR-0077):
// the sequential/parallel flag, an optional <loopCardinality> and <completionCondition>,
// and its <zeebe:loopCharacteristics>. Zeebe nests the loop characteristics inside the
// marker's own <extensionElements>, so the path is
// multiInstanceLoopCharacteristics > extensionElements > loopCharacteristics.
type xmlMultiInstance struct {
	IsSequential        string            `xml:"isSequential,attr"`
	LoopCardinality     string            `xml:"loopCardinality"`
	CompletionCondition string            `xml:"completionCondition"`
	Loop                xmlZeebeLoopChars `xml:"extensionElements>loopCharacteristics"`
}

// xmlStandardLoop is a <standardLoopCharacteristics> on an activity (ADR-0133): the
// other BPMN loop marker — the circular-arrow icon — which repeats the activity while
// its <loopCondition> holds. TestBefore checks the condition before the first
// iteration (a while loop; absent means a repeat-until that always runs once) and
// LoopMaximum caps the iteration count. Both are plain BPMN, no Zeebe extension.
type xmlStandardLoop struct {
	TestBefore    string `xml:"testBefore,attr"`
	LoopMaximum   string `xml:"loopMaximum,attr"`
	LoopCondition string `xml:"loopCondition"`
}

// xmlZeebeLoopChars is the <zeebe:loopCharacteristics> of a multi-instance activity:
// the FEEL input collection, the per-iteration input element, and the output
// collection/element that assemble each iteration's result into a list (ADR-0077).
type xmlZeebeLoopChars struct {
	InputCollection  string `xml:"inputCollection,attr"`
	InputElement     string `xml:"inputElement,attr"`
	OutputCollection string `xml:"outputCollection,attr"`
	OutputElement    string `xml:"outputElement,attr"`
}

// xmlSubProcess is an embedded <subProcess>: a container whose own flow nodes and
// sequence flows compile into the flat node array, linked back to it only by their
// FlowScope (ADR-0074). It recurses — a subprocess may contain subprocesses.
type xmlSubProcess struct {
	Id        string            `xml:"id,attr"`
	Name      string            `xml:"name,attr"`
	IOMapping xmlZeebeIOMapping `xml:"extensionElements>ioMapping"`
	// TriggeredByEvent marks an event subprocess (ADR-0082): it is not entered by a
	// sequence flow but armed by its start event's event definition while the parent
	// scope runs. "true" makes it an event subprocess; empty/absent is an ordinary one.
	TriggeredByEvent string            `xml:"triggeredByEvent,attr"`
	MultiInstance    *xmlMultiInstance `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop     *xmlStandardLoop  `xml:"standardLoopCharacteristics"`
	// IsTransaction marks a subprocess that was parsed from a <transaction> element (never from
	// XML — set by foldTransactions). The compiler marks its compiled node so the runtime and
	// validation know it may host a cancel boundary and hold a cancel end event (ADR-0108).
	IsTransaction bool `xml:"-"`
	xmlFlowContent
}

// xmlAdHocSubProcess is an <adHocSubProcess>: a container whose contained activities are not
// sequenced from a start event but run on demand, in any order, zero or more times (ADR-0138).
// It nests the same flow content as an embedded subprocess — its own activities, sequence flows,
// boundary events and nested subprocesses compile into the flat node array scoped by it — and
// adds the ad-hoc's own configuration:
//
//   - Ordering "Sequential" runs one contained activity at a time; anything else (including the
//     absent default) is parallel: every entry activity is activated at once.
//   - CancelRemainingInstances "false" lets the still-running activities finish when the
//     completion condition holds; absent/"true" is the BPMN default (cancel them).
//   - CompletionCondition is an optional boolean FEEL expression re-evaluated after each
//     contained activity completes; absent means the ad-hoc completes when its scope drains.
type xmlAdHocSubProcess struct {
	Id                       string `xml:"id,attr"`
	Name                     string `xml:"name,attr"`
	Ordering                 string `xml:"ordering,attr"`
	CancelRemainingInstances string `xml:"cancelRemainingInstances,attr"`
	CompletionCondition      string `xml:"completionCondition"`
	xmlFlowContent
}

// foldTransactions merges each scope's <transaction> subprocesses into its SubProcesses
// slice, marked IsTransaction, recursively down the scope tree. A transaction is
// structurally an embedded subprocess with cancellation added (ADR-0108); folding it into
// SubProcesses means every existing walk (registration, flow wiring, compensation
// resolution, I/O and multi-instance) treats it as a subprocess with no special-casing, and
// only the two genuinely new sites — marking the compiled node, and dispatching a cancel
// end/boundary — need to look at IsTransaction / the cancel event definition.
func foldTransactions(fc *xmlFlowContent) {
	for i := range fc.Transactions {
		fc.Transactions[i].IsTransaction = true
	}
	fc.SubProcesses = append(fc.SubProcesses, fc.Transactions...)
	fc.Transactions = nil
	for i := range fc.SubProcesses {
		foldTransactions(&fc.SubProcesses[i].xmlFlowContent)
	}
	for i := range fc.AdHocSubProcesses {
		foldTransactions(&fc.AdHocSubProcesses[i].xmlFlowContent)
	}
}

// resolveSendTaskOperations rewrites each send task's operationRef to the message that
// operation sends (its inMessageRef), so the rest of the compiler treats an operationRef send
// exactly as a messageRef send — the message kind (ADR-0112). It walks every scope, like
// foldTransactions, and runs right after it (so send tasks inside a folded transaction are
// covered) and before registerScope. A send task carrying both a messageRef and an operationRef
// is a conflict (a deploy error).
func resolveSendTaskOperations(fc *xmlFlowContent, resolveOperation func(ownerId, operationRef string) (string, error)) error {
	for i := range fc.SendTasks {
		st := &fc.SendTasks[i]
		op := strings.TrimSpace(st.OperationRef)
		if op == "" {
			continue
		}
		if strings.TrimSpace(st.MessageRef) != "" {
			return fmt.Errorf("compiler: send task %q sets both messageRef and operationRef; use one", st.Id)
		}
		msg, err := resolveOperation(st.Id, op)
		if err != nil {
			return err
		}
		st.MessageRef = msg
	}
	for i := range fc.SubProcesses {
		if err := resolveSendTaskOperations(&fc.SubProcesses[i].xmlFlowContent, resolveOperation); err != nil {
			return err
		}
	}
	for i := range fc.AdHocSubProcesses {
		if err := resolveSendTaskOperations(&fc.AdHocSubProcesses[i].xmlFlowContent, resolveOperation); err != nil {
			return err
		}
	}
	return nil
}

// A BPMN data object. It is not a flow node — no token flows through it — so it
// carries no sequence flows, only its identity (name, id), an optional collection
// flag, an optional itemDefinition reference (its declared type), and an optional
// <dataState> child naming its initial data state (ADR-0053). dataState is
// spec-legal on any ItemAwareElement.
type xmlDataObject struct {
	Id             string       `xml:"id,attr"`
	Name           string       `xml:"name,attr"`
	IsCollection   bool         `xml:"isCollection,attr"`
	ItemSubjectRef string       `xml:"itemSubjectRef,attr"`
	DataState      xmlDataState `xml:"dataState"`
}

// A BPMN <dataStore> is a root element: a place data outlives the processes that
// touch it. Atlas reads its id, which is what a <dataStoreReference> points at, and
// its name, which is what the application's information model declares the store by.
type xmlDataStore struct {
	Id   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

// A BPMN <dataStoreReference> is the store as one process draws it. It may carry a
// name of its own for that diagram, but the store's name is the authoritative one —
// every process that says "Orders" means the same store.
type xmlDataStoreReference struct {
	Id           string `xml:"id,attr"`
	Name         string `xml:"name,attr"`
	DataStoreRef string `xml:"dataStoreRef,attr"`
}

// xmlDataState is a BPMN data state (the [received]/[approved] label on a data
// object or reference), carried by its name.
type xmlDataState struct {
	Name string `xml:"name,attr"`
}

// A BPMN data object reference: a pointer to a <dataObject> (dataObjectRef) that may
// carry its own <dataState> — so the same object can appear on the canvas in several
// states (order [received], order [approved]). A data-output association targets a
// reference to say which object it writes and what state it moves it into (ADR-0058).
//
// The name is read because it is what the reference shows on the canvas, and because
// it is what identifies the object when the <dataObject> the reference points at is
// not in the model — the case a tool leaves behind when it loses the declaration but
// keeps the shape.
type xmlDataObjectReference struct {
	Id            string       `xml:"id,attr"`
	Name          string       `xml:"name,attr"`
	DataObjectRef string       `xml:"dataObjectRef,attr"`
	DataState     xmlDataState `xml:"dataState"`
}

// xmlDataOutputAssociation is a <dataOutputAssociation> on an activity: targetRef
// names the data object (or a <dataObjectReference> to it) the activity writes, and
// the optional <assignment><from> is a FEEL expression, evaluated over the instance's
// variables at completion, that produces the written value (ADR-0058).
type xmlDataOutputAssociation struct {
	TargetRef  string        `xml:"targetRef"`
	Assignment xmlAssignment `xml:"assignment"`
}

// xmlAssignment is a data association's <assignment>: a <from> value expression and a
// <to> target. Atlas reads <from> as the FEEL value expression. On an output
// association <to> is an optional member path within the target data object (e.g.
// "name") — set, the write updates only that member (ADR-0060); empty, it writes the
// whole value. On an input association <to> is unused (its target is targetRef).
type xmlAssignment struct {
	From string `xml:"from"`
	To   string `xml:"to"`
}

// xmlDataInputAssociation is a <dataInputAssociation> on an activity: sourceRef
// names the data object (or a <dataObjectReference> to it) the activity reads,
// targetRef is the process-variable name the read value is written into, and the
// optional <assignment><from> is a FEEL transform evaluated over the instance's
// variables plus the source object bound under its name (ADR-0059).
type xmlDataInputAssociation struct {
	SourceRef  string        `xml:"sourceRef"`
	TargetRef  string        `xml:"targetRef"`
	Assignment xmlAssignment `xml:"assignment"`
}

// A data-based exclusive gateway; default names the flow taken when no outgoing
// condition matches.
type xmlExclusiveGateway struct {
	Id      string `xml:"id,attr"`
	Default string `xml:"default,attr"`
}

// A start event. A plain (none) start event is a manual entry point; one bearing
// a messageEventDefinition is a message start event, instantiated by a
// correlating message (ADR-0035). The definition is a pointer so an absent one
// is detected as nil.
type xmlStartEvent struct {
	Id      string                     `xml:"id,attr"`
	Name    string                     `xml:"name,attr"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
	// SingletonStart ("true") marks a message start event as one-per-correlation-key
	// (ADR-0094): while an instance started with a key is live, a further correlating
	// message starts no duplicate. A plain attribute (like versionTag on a process);
	// absent = the default start-per-message behavior (ADR-0035).
	SingletonStart string `xml:"singletonStart,attr"`
	// Timer, when present, makes this a timer start event: the process starts a
	// fresh instance on the schedule (duration/date/cycle/cron) the definition
	// carries, armed at deploy time (ADR-0051). A pointer so an absent one is nil.
	Timer *xmlTimerEventDefinition `xml:"timerEventDefinition"`
	// Signal, when present, makes this a signal start event: a broadcast signal of the
	// referenced name instantiates the process (ADR-0088). A pointer so an absent one is nil.
	Signal *xmlSignalEventDefinition `xml:"signalEventDefinition"`
	// Error, when present on an event-subprocess start event, makes it an error-triggered
	// event subprocess: it catches an error propagating in its scope whose code matches
	// (ADR-0089). A pointer so an absent one is nil.
	Error *xmlErrorEventDefinition `xml:"errorEventDefinition"`
	// Escalation, when present on an event-subprocess start event, makes it an
	// escalation-triggered event subprocess: it catches an escalation propagating in its
	// scope whose code matches (ADR-0125). May be interrupting or non-interrupting per
	// IsInterrupting. A pointer so an absent one is nil.
	Escalation *xmlEscalationEventDefinition `xml:"escalationEventDefinition"`
	// Conditional, when present on an event-subprocess start event, makes it a
	// conditional-triggered event subprocess: it fires while its scope runs when its boolean
	// FEEL condition becomes true (ADR-0137). May be interrupting or non-interrupting per
	// IsInterrupting. A pointer so an absent one is nil.
	Conditional *xmlConditionalEventDefinition `xml:"conditionalEventDefinition"`
	// IsInterrupting is the event-subprocess start event's cancel flag (ADR-0082):
	// absent or "true" interrupts the parent scope when the trigger fires, "false" runs
	// the handler alongside it. Empty for an ordinary start event.
	IsInterrupting string `xml:"isInterrupting,attr"`
	// Form binds a start form to a none start event (ADR-0028): the form the UI
	// shows before creating the instance, whose data becomes the start variables.
	// The engine never sees it — it is pre-start UI metadata.
	Form xmlFormDefinition `xml:"extensionElements>formDefinition"`
}

// A data-based inclusive gateway; default names the flow taken when no outgoing
// condition matches.
type xmlInclusiveGateway struct {
	Id      string `xml:"id,attr"`
	Default string `xml:"default,attr"`
}

// An intermediate catch event; the timer, message, signal, and link variants are executable.
// Each definition is a pointer so an absent one is detected as nil.
type xmlIntermediateCatchEvent struct {
	Id      string                     `xml:"id,attr"`
	Timer   *xmlTimerEventDefinition   `xml:"timerEventDefinition"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
	Signal  *xmlSignalEventDefinition  `xml:"signalEventDefinition"`
	// Link, when present, makes this a link catch event: the landing point of a link throw
	// with the same name in the same scope — an off-page worker / goto (ADR-0133). A
	// pointer so an absent one is nil.
	Link *xmlLinkEventDefinition `xml:"linkEventDefinition"`
	// Conditional, when present, makes this a conditional catch event: it waits until its
	// boolean FEEL condition over the process's variables becomes true, then flows on
	// (ADR-0137). A pointer so an absent one is nil.
	Conditional *xmlConditionalEventDefinition `xml:"conditionalEventDefinition"`
}

// xmlConditionalEventDefinition is a <conditionalEventDefinition> on an intermediate catch,
// boundary, or event-subprocess start event (ADR-0137). Its <condition> is a boolean FEEL
// expression over the process's variables; the event fires when it becomes true. Only the
// condition matters — a conditional event carries no ref, code, or name.
type xmlConditionalEventDefinition struct {
	Condition string `xml:"condition"`
}

// An intermediate throw event; the message, signal, compensation, and escalation variants
// are executable.
type xmlIntermediateThrowEvent struct {
	Id      string                     `xml:"id,attr"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
	Signal  *xmlSignalEventDefinition  `xml:"signalEventDefinition"`
	// Compensation, when present, makes this a compensation throw event: it triggers
	// compensation of completed compensable activities in its scope (or of the one named
	// by activityRef), then flows on (ADR-0103). A pointer so an absent one is nil.
	Compensation *xmlCompensateEventDefinition `xml:"compensateEventDefinition"`
	// Escalation, when present, makes this an escalation throw event: it raises the
	// referenced escalation, propagating up to the nearest matching handler, then continues
	// on its outgoing flow (ADR-0125). A pointer so an absent one is nil.
	Escalation *xmlEscalationEventDefinition `xml:"escalationEventDefinition"`
	// Link, when present, makes this a link throw event: a goto to the link catch of the same
	// name in the same scope — an off-page worker (ADR-0133). A pointer so an absent one is nil.
	Link *xmlLinkEventDefinition `xml:"linkEventDefinition"`
}

// xmlLinkEventDefinition is a <linkEventDefinition name="…"> on an intermediate throw or
// catch event (ADR-0133). A link is matched by Name within one flow scope: a throw jumps to
// the catch of the same name. Only the name matters — a link carries no ref, code, or payload.
type xmlLinkEventDefinition struct {
	Name string `xml:"name,attr"`
}

// xmlCompensateEventDefinition is a <compensateEventDefinition> on a throw, end, or
// boundary event. On a throw/end event, ActivityRef optionally names the single activity
// to compensate — empty compensates every completed compensable activity in the scope
// (ADR-0103). On a boundary event it has no attributes (the boundary just marks its host
// compensable and links to a handler via a BPMN <association>). waitForCompletion is
// accepted but not yet honored (compensation is synchronous).
type xmlCompensateEventDefinition struct {
	ActivityRef       string `xml:"activityRef,attr"`
	WaitForCompletion string `xml:"waitForCompletion,attr"`
}

// xmlAssociation is a BPMN <association>: an undirected artifact link. Atlas parses it
// only to join a compensation boundary event to its compensation handler activity — one
// endpoint is the boundary, the other the handler (ADR-0103). Non-compensation
// associations are ignored.
type xmlAssociation struct {
	Id        string `xml:"id,attr"`
	SourceRef string `xml:"sourceRef,attr"`
	TargetRef string `xml:"targetRef,attr"`
}

// An end event. A plain (none) end event just ends the instance; one bearing a
// messageEventDefinition is a message end event, which publishes the message
// then ends (ADR-0052); a signalEventDefinition is a signal end event, which
// broadcasts the signal then ends (ADR-0088). Each is a pointer so an absent one is nil.
type xmlEndEvent struct {
	Id      string                     `xml:"id,attr"`
	Message *xmlMessageEventDefinition `xml:"messageEventDefinition"`
	Signal  *xmlSignalEventDefinition  `xml:"signalEventDefinition"`
	// Error, when present, makes this an error end event: it throws the referenced error,
	// ending its enclosing scope abnormally and propagating up to the nearest matching
	// handler (ADR-0089). A pointer so an absent one is nil.
	Error *xmlErrorEventDefinition `xml:"errorEventDefinition"`
	// Escalation, when present, makes this an escalation end event: it raises the referenced
	// escalation, propagating up to the nearest matching handler, then ends its path
	// (ADR-0125). A pointer so an absent one is nil.
	Escalation *xmlEscalationEventDefinition `xml:"escalationEventDefinition"`
	// Terminate is present when the end event carries a <terminateEventDefinition>.
	// Atlas can't execute a terminate end yet, so it is rejected at compile time rather
	// than silently dropped to a plain end (which would abandon the terminate semantics).
	Terminate *xmlTerminateEventDefinition `xml:"terminateEventDefinition"`
	// Compensation, when present, makes this a compensation end event: it triggers
	// compensation, then ends its scope (ADR-0103); the trigger-and-stop counterpart of a
	// compensation throw. A pointer so an absent one is nil.
	Compensation *xmlCompensateEventDefinition `xml:"compensateEventDefinition"`
	// Cancel, when present, makes this a cancel end event: it cancels the enclosing
	// transaction — compensating its completed activities, then routing out the transaction's
	// cancel boundary (ADR-0108). Valid only inside a transaction. A pointer so an absent one is nil.
	Cancel *xmlCancelEventDefinition `xml:"cancelEventDefinition"`
}

// xmlTerminateEventDefinition is the empty <terminateEventDefinition> element; only its
// presence matters (a non-nil pointer once parsed).
type xmlTerminateEventDefinition struct{}

// xmlCancelEventDefinition is the empty <cancelEventDefinition> element; only its presence
// matters. On an end event it makes a cancel end event (cancels the enclosing transaction);
// on a boundary event it makes a cancel boundary (catches a transaction's cancellation),
// which may attach only to a transaction and is always interrupting (ADR-0108).
type xmlCancelEventDefinition struct{}

// A boundary event is attached to a host activity (AttachedToRef) and arms while
// it runs. CancelActivity mirrors BPMN's attribute: absent or "true" is
// interrupting (cancels the host on fire), "false" is non-interrupting. The timer
// and message variants are executable; each definition is a pointer so an absent
// one is detected as nil (ADR-0040).
type xmlBoundaryEvent struct {
	Id             string                     `xml:"id,attr"`
	AttachedToRef  string                     `xml:"attachedToRef,attr"`
	CancelActivity string                     `xml:"cancelActivity,attr"`
	Timer          *xmlTimerEventDefinition   `xml:"timerEventDefinition"`
	Message        *xmlMessageEventDefinition `xml:"messageEventDefinition"`
	Signal         *xmlSignalEventDefinition  `xml:"signalEventDefinition"`
	// Error, when present, makes this an error boundary event: it catches an error thrown
	// by the host activity (or propagated up to it) whose code matches, and is always
	// interrupting (ADR-0089). A pointer so an absent one is nil.
	Error *xmlErrorEventDefinition `xml:"errorEventDefinition"`
	// Escalation, when present, makes this an escalation boundary event: it catches an
	// escalation raised by the host activity (or propagated up to it) whose code matches.
	// Unlike an error boundary it honors CancelActivity — it may be interrupting or
	// non-interrupting (ADR-0125). A pointer so an absent one is nil.
	Escalation *xmlEscalationEventDefinition `xml:"escalationEventDefinition"`
	// Compensation, when present, makes this a compensation boundary event: it is inert
	// (never armed), marking its host activity compensable and linking — via a BPMN
	// <association> — to the compensation handler (ADR-0103). A pointer so an absent one is nil.
	Compensation *xmlCompensateEventDefinition `xml:"compensateEventDefinition"`
	// Cancel, when present, makes this a cancel boundary event: it catches its host
	// transaction's cancellation and routes the recovery flow. Valid only on a transaction,
	// and always interrupting (ADR-0108). A pointer so an absent one is nil.
	Cancel *xmlCancelEventDefinition `xml:"cancelEventDefinition"`
	// Conditional, when present, makes this a conditional boundary event: it fires while its
	// host activity runs when its boolean FEEL condition becomes true. Honors CancelActivity —
	// interrupting or non-interrupting (ADR-0137). A pointer so an absent one is nil.
	Conditional *xmlConditionalEventDefinition `xml:"conditionalEventDefinition"`
}

type xmlTimerEventDefinition struct {
	TimeDuration string `xml:"timeDuration"` // ISO-8601 duration, e.g. PT30S
	TimeDate     string `xml:"timeDate"`     // ISO-8601 instant, e.g. 2026-08-01T09:00:00Z (ADR-0051)
	TimeCycle    string `xml:"timeCycle"`    // ISO-8601 repeating interval (R3/PT1H) or cron ("0 * * * *") (ADR-0051)
}

type xmlNode struct {
	Id      string                     `xml:"id,attr"`
	DataOut []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn  []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

// xmlPlainTask is an undefined <task> or a <manualTask>: an activity with no
// execution semantics of its own (the engine runs it as a pass-through), but an
// activity all the same — so it carries the loop markers (ADR-0077, ADR-0133) as well
// as data associations. It is a separate shape from xmlNode precisely so those markers
// stay off the gateways that share xmlNode: a looping gateway is not a thing.
type xmlPlainTask struct {
	Id            string                     `xml:"id,attr"`
	DataOut       []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn        []xmlDataInputAssociation  `xml:"dataInputAssociation"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop  *xmlStandardLoop           `xml:"standardLoopCharacteristics"`
}

// A user task parks a token for human completion (ADR-0028). It optionally
// carries a zeebe:assignmentDefinition for assignee/candidateGroups.
type xmlUserTask struct {
	Id            string                     `xml:"id,attr"`
	Name          string                     `xml:"name,attr"`
	Assignment    xmlAssignmentDefinition    `xml:"extensionElements>assignmentDefinition"`
	Form          xmlFormDefinition          `xml:"extensionElements>formDefinition"`
	Priority      xmlPriorityDefinition      `xml:"extensionElements>priorityDefinition"`
	Schedule      xmlTaskSchedule            `xml:"extensionElements>taskSchedule"`
	IOMapping     xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop  *xmlStandardLoop           `xml:"standardLoopCharacteristics"`
	DataOut       []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn        []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

// xmlPriorityDefinition carries zeebe:priorityDefinition's static task priority
// (ADR-0091). An empty value means the task uses the default priority.
type xmlPriorityDefinition struct {
	Priority string `xml:"priority,attr"`
}

// xmlTaskSchedule carries zeebe:taskSchedule. Atlas reads dueDate as an ISO-8601
// duration relative to task creation (its timer convention, ADR-0040/0051);
// followUpDate is not yet executable.
type xmlTaskSchedule struct {
	DueDate      string `xml:"dueDate,attr"`
	FollowUpDate string `xml:"followUpDate,attr"`
}

// xmlFormDefinition binds a form to a user task by id (ADR-0028). formId
// references a form stored server-side (the form-js schema the Tasks app
// renders); an empty formId means the task has no form.
type xmlFormDefinition struct {
	FormId string `xml:"formId,attr"`
}

type xmlAssignmentDefinition struct {
	Assignee        string `xml:"assignee,attr"`
	CandidateGroups string `xml:"candidateGroups,attr"`
}

type xmlServiceTask struct {
	Id string `xml:"id,attr"`
	// MessageRef is read only for a send task (ADR-0112): a <sendTask messageRef> is the
	// message-kind send — a correlating throw in task form. A service task never carries one
	// (it dispatches on its taskDefinition/connector), so the field is inert there.
	MessageRef string `xml:"messageRef,attr"`
	// OperationRef is read only for a send task (ADR-0112): a <sendTask operationRef> names a
	// <bpmn:operation> whose <inMessageRef> is the message to send. It is resolved to that
	// message before compilation (resolveSendTaskOperations), so it is an alternate spelling of
	// the message kind — a fire-and-forget throw. The operation's outMessageRef (a response) is
	// not supported. Inert on a service task.
	OperationRef   string            `xml:"operationRef,attr"`
	TaskDefinition xmlTaskDefinition `xml:"extensionElements>taskDefinition"`
	// Form binds a *repair* form to this task (ADR-0169): the form an operator is shown
	// when a token parks here with an incident, naming the variables worth looking at.
	// It reuses zeebe:formDefinition, the same element a user task binds its work form
	// with — the tag says "this element has a form", and what the form is *for* follows
	// from the element that carries it. A service task never shows a form to a human in
	// the normal course of things, so there is no ambiguity to resolve.
	Form xmlFormDefinition `xml:"extensionElements>formDefinition"`
	// Clio, when present, marks this service task a clio task (ADR-0036).
	// The pointer is nil when the <atlas:clioConnector> extension is absent.
	Clio *xmlClioConnector `xml:"extensionElements>clioConnector"`
	// Rest, when present, marks this service task an HTTP-REST task
	// (ADR-0067). The pointer is nil when the <atlas:restConnector> extension is
	// absent.
	Rest *xmlRestConnector `xml:"extensionElements>restConnector"`
	// Mail, when present, marks this service task an outbound mail task
	// (ADR-0079). The pointer is nil when the <atlas:mailConnector> extension is
	// absent.
	Mail *xmlMailConnector `xml:"extensionElements>mailConnector"`
	// User, when present, marks this service task a user-provisioning task
	// (ADR-0123). The pointer is nil when the <atlas:userConnector> extension is absent.
	User *xmlUserConnector `xml:"extensionElements>userConnector"`
	// Csv, when present, marks this service task a CSV-to-JSON task
	// (ADR-0139). The pointer is nil when the <atlas:csvConnector> extension is absent.
	Csv *xmlCsvConnector `xml:"extensionElements>csvConnector"`
	// SharePoint, when present, marks this service task a SharePoint task
	// (ADR-0141). The pointer is nil when the <atlas:sharepointConnector> extension is
	// absent. Read it through sharePointConn, not directly — the Modeler writes the
	// tag with a capital P (see SharePointCamel).
	SharePoint *xmlSharePointConnector `xml:"extensionElements>sharepointConnector"`
	// SharePointCamel is the same extension under the spelling the Modeler produces.
	// bpmn-js derives an element's tag from its moddle type by lowercasing only the
	// first letter, so the type SharePointConnector serializes as
	// <atlas:sharePointConnector> — while hand-authored models (and every compiler
	// test) use the all-lowercase <atlas:sharepointConnector>. Go's XML matching is
	// case-sensitive, so a task authored in the Modeler was silently ignored: its
	// configuration sat in the XML and the task compiled as an unconfigured service
	// task. Accepting both spellings keeps hand-authored and Modeler-authored models
	// working; sharePointConn normalizes them.
	SharePointCamel *xmlSharePointConnector `xml:"extensionElements>sharePointConnector"`
	// Remedy, when present, marks this service task a BMC Remedy task
	// (ADR-0106). The pointer is nil when the <atlas:remedyConnector> extension is
	// absent.
	Remedy *xmlRemedyConnector `xml:"extensionElements>remedyConnector"`
	// WebScrape, when present, marks this service task a web-scraping task
	// (ADR-0118). The pointer is nil when the <atlas:webscrapeConnector> extension is
	// absent.
	WebScrape *xmlWebScrapeConnector `xml:"extensionElements>webscrapeConnector"`
	// Scim, when present, marks this service task a SCIM 2.0 task
	// (ADR-0153): it performs a resource operation against a model-authored SCIM
	// service provider through the job path.
	Scim *xmlScimConnector `xml:"extensionElements>scimConnector"`
	// Ldap, when present, marks this service task a generic LDAP task
	// (ADR-0154): it performs a directory operation against a model-authored LDAP
	// server through the job path.
	Ldap *xmlLdapConnector `xml:"extensionElements>ldapConnector"`
	// Soap, when present, marks this service task a SOAP / Web Services (WSDL) worker
	// task (ADR-0165): it invokes a SOAP operation against a model-authored web-service
	// endpoint through the job path.
	Soap *xmlSoapConnector `xml:"extensionElements>soapConnector"`
	// Ad, when present, marks this service task an Active Directory task
	// (ADR-0166): it performs an AD-specific provisioning operation against a
	// model-authored server through the job path.
	Ad *xmlAdConnector `xml:"extensionElements>adConnector"`
	// MsSql, MariaDB and Postgres each mark this service task a SQL task of
	// that product (ADR-0173): one statement against a database a *worker* is
	// configured for. They share a shape and differ only in the driver behind them,
	// which is what decides the placeholder syntax a statement must use. They are the
	// first kinds with no in-process handler at all.
	MsSql    *xmlSqlConnector `xml:"extensionElements>mssqlConnector"`
	MariaDB  *xmlSqlConnector `xml:"extensionElements>mariadbConnector"`
	Postgres *xmlSqlConnector `xml:"extensionElements>postgresConnector"`
	// Entra, when present, marks this service task a Microsoft Entra ID worker
	// task (ADR-0172): one directory lifecycle operation through Graph, against a
	// tenant a *worker* holds the app credential for.
	Entra *xmlEntraConnector `xml:"extensionElements>entraConnector"`
	// Ldif, when present, marks this service task a directory-file task
	// (ADR-0171): LDIF or DSML entries read from, or written to, a variable.
	Ldif *xmlLdifConnector `xml:"extensionElements>ldifConnector"`
	// Jira, when present, marks this service task a Jira task
	// (ADR-0201): one issue-tracker operation against a
	// server-registered Jira instance.
	Jira *xmlJiraConnector `xml:"extensionElements>jiraConnector"`
	// GoogleSheets, when present, marks this service task a Google Sheets task: one
	// spreadsheet operation against a Worker an operator configured.
	GoogleSheets *xmlGoogleSheetsConnector `xml:"extensionElements>googleSheetsConnector"`
	// Mockup, when present, marks this service task an engine-simulated mockup task
	// (ADR-0120). The pointer is nil when the <atlas:mockupConnector> extension is
	// absent.
	Mockup        *xmlMockupConnector        `xml:"extensionElements>mockupConnector"`
	IOMapping     xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop  *xmlStandardLoop           `xml:"standardLoopCharacteristics"`
	DataOut       []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn        []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

// sharePointConn returns the task's SharePoint worker extension under either
// spelling (see SharePointCamel), or nil when the task carries none. Every reader
// must go through it so both hand-authored and Modeler-authored models compile.
func (st xmlServiceTask) sharePointConn() *xmlSharePointConnector {
	if st.SharePoint != nil {
		return st.SharePoint
	}
	return st.SharePointCamel
}

// xmlSendTask is a <sendTask>: a job-creating activity identical in shape and execution to
// a service task (ADR-0112) — same taskDefinition, worker extensions, I/O mappings,
// multi-instance, and data associations. It is a type alias so both parse and every
// per-activity wiring loop treat the two identically; only the compiled node type differs.
type xmlSendTask = xmlServiceTask

// A clio task's parameters, carried on a service task as an
// <atlas:clioConnector connector="..." operation="..." .../> extension element.
// worker names a server-registered worker (its endpoint and credentials live
// in the server config, never in the model). operation is "write" (default),
// "query", or "read", selecting which of the remaining attributes apply:
//   - write: subject and eventType — the clio coordinates the appended event
//     (the instance's variables) lands under.
//   - query: resultVariable receives the result; either query (a run_query string)
//     or subject (with the optional reduceSpec projection, a get_state read).
//   - read: subject's events (up to limit; 0 = the worker's default) are read
//     into resultVariable as a JSON array.
type xmlClioConnector struct {
	Connector      string `xml:"connector,attr"`
	Operation      string `xml:"operation,attr"`
	Subject        string `xml:"subject,attr"`
	EventType      string `xml:"eventType,attr"`
	Query          string `xml:"query,attr"`
	ReduceSpec     string `xml:"reduceSpec,attr"`
	Limit          string `xml:"limit,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// An HTTP-REST task's parameters, carried on a service task as an
// <atlas:restConnector> extension element (ADR-0067). method is the HTTP method;
// url is the full request URL, authored in the model; resultVariable, if set, is
// the process variable the JSON response is written back into. Header and
// QueryParam child elements add request headers and query parameters. The auth*
// attributes describe authentication: authType is "basic"/"bearer"/"apiKey"/
// "oauth2"; authUsername (basic) and authApiKeyName (the apiKey header name) are
// model data; authSecret names a server-side secret (ADR-0041) — never the secret
// value. For oauth2 (client-credentials, ADR-0152) authTokenUrl/authClientId/
// authScope are model data and authSecret is the client secret reference.
type xmlRestConnector struct {
	Method         string      `xml:"method,attr"`
	Url            string      `xml:"url,attr"`
	ResultVariable string      `xml:"resultVariable,attr"`
	AuthType       string      `xml:"authType,attr"`
	AuthUsername   string      `xml:"authUsername,attr"`
	AuthApiKeyName string      `xml:"authApiKeyName,attr"`
	AuthSecret     string      `xml:"authSecret,attr"`
	AuthTokenURL   string      `xml:"authTokenUrl,attr"`
	AuthClientID   string      `xml:"authClientId,attr"`
	AuthScope      string      `xml:"authScope,attr"`
	Headers        []xmlHTTPKV `xml:"httpHeader"`
	QueryParams    []xmlHTTPKV `xml:"queryParam"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// xmlScimConnector is the <atlas:scimConnector> extension on a service task
// (ADR-0153). BaseUrl and Resource address the SCIM service provider and resource
// type ("Users"/"Groups"); Operation selects the SCIM call; ResourceId (get/replace/
// patch/delete) and Filter (search) carry literal-or-FEEL values; BodyVariable names
// the process variable holding the create/replace/patch payload (blank → the whole
// variable scope); ResultVariable receives the JSON response. Auth* name the scheme
// and a server-side secret reference (never the value, ADR-0041), mirroring
// xmlRestConnector.
type xmlScimConnector struct {
	BaseUrl        string `xml:"baseUrl,attr"`
	Resource       string `xml:"resource,attr"`
	Operation      string `xml:"operation,attr"`
	ResourceId     string `xml:"resourceId,attr"`
	Filter         string `xml:"filter,attr"`
	BodyVariable   string `xml:"bodyVariable,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	AuthType       string `xml:"authType,attr"`
	AuthUsername   string `xml:"authUsername,attr"`
	AuthApiKeyName string `xml:"authApiKeyName,attr"`
	AuthSecret     string `xml:"authSecret,attr"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// xmlSoapConnector is the <atlas:soapConnector> extension on a service task
// (ADR-0165). endpoint is the web-service URL (from the WSDL's soap:address) and
// operation the operation name; soapAction overrides the SOAPAction header (blank →
// operation); body is the XML payload placed inside the SOAP envelope's Body;
// soapVersion is the protocol version ("1.1"/"1.2", blank → 1.1); resultVariable
// receives the parsed response. Auth* name the scheme and a server-side secret
// reference (never the value, ADR-0041), mirroring xmlRestConnector. endpoint/
// soapAction/body carry literal-or-FEEL values.
type xmlSoapConnector struct {
	Endpoint       string `xml:"endpoint,attr"`
	Operation      string `xml:"operation,attr"`
	Action         string `xml:"soapAction,attr"`
	Body           string `xml:"body,attr"`
	Version        string `xml:"soapVersion,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	AuthType       string `xml:"authType,attr"`
	AuthUsername   string `xml:"authUsername,attr"`
	AuthApiKeyName string `xml:"authApiKeyName,attr"`
	AuthSecret     string `xml:"authSecret,attr"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// xmlAdConnector is the <atlas:adConnector> extension on a service task (ADR-0166).
// worker names a directory configured in the Console, which is how a task should
// address one (ADR-0206); the url/bindDN/bindSecret trio
// below is the older, model-authored form and still compiles.
// url is the server (ldaps://host:636 for a password set); bindDN/bindSecret
// authenticate the bind (bindSecret a reference, never a value, ADR-0041); startTLS
// upgrades a plain connection. operation selects the AD operation. dn is the target
// user or group entry; memberDN is the member added/removed for the group operations;
// entryVariable names the create-user attribute object; newPassword is the
// set-password value. url/bindDN/dn/memberDN/newPassword carry literal-or-FEEL values.
type xmlAdConnector struct {
	// Worker names a directory an operator configured in the Console, the way every
	// other credential-bearing kind is addressed (ADR-0206).
	// It replaces url/bindDN/bindSecret, which stay accepted so models written before
	// this keep compiling — a task carries one shape or the other, never both.
	Connector     string `xml:"connector,attr"`
	URL           string `xml:"url,attr"`
	BindDN        string `xml:"bindDN,attr"`
	BindSecret    string `xml:"bindSecret,attr"`
	StartTLS      string `xml:"startTLS,attr"`
	Operation     string `xml:"operation,attr"`
	DN            string `xml:"dn,attr"`
	MemberDN      string `xml:"memberDN,attr"`
	EntryVariable string `xml:"entryVariable,attr"`
	NewPassword   string `xml:"newPassword,attr"`
	NewDN         string `xml:"newDN,attr"`
	// The two reading operations share BaseDN (where the read starts), Filter (what it
	// narrows to), MaxEntries (what caps it) and ResultVariable (what receives it).
	//
	// search reads what is under the base *now* and additionally takes a Scope
	// ("base"/"one"/"sub", blank → sub). sync reads what changed since a cookie:
	// CookieVariable names the variable the cookie is read from *and written back to*,
	// ObjectSecurity selects the flag a non-privileged sync account needs, and there is
	// no scope to author because AD answers DirSync only for the whole subtree
	// (ADR-0166).
	BaseDN         string `xml:"baseDN,attr"`
	Filter         string `xml:"filter,attr"`
	Scope          string `xml:"scope,attr"`
	CookieVariable string `xml:"cookieVariable,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	MaxEntries     string `xml:"maxEntries,attr"`
	ObjectSecurity string `xml:"objectSecurity,attr"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// xmlSqlConnector is the extension a SQL task carries, under whichever of
// <atlas:mssqlConnector>, <atlas:mariadbConnector> or <atlas:postgresConnector> names
// its product (ADR-0173). The three share this shape exactly; only the element name,
// and so the driver, differs. worker names the database the worker holds the DSN
// for — there is deliberately no url and no credential attribute, because the
// connection string never enters the engine. operation is query / query-one / execute. statement is the
// SQL text and is *literal only*: it carries no fx toggle, so no process value can
// become part of it. parametersVariable names the variable bound to the statement's
// placeholders; resultVariable receives the result; maxRows caps a query's rows.
type xmlSqlConnector struct {
	Connector          string `xml:"connector,attr"`
	Operation          string `xml:"operation,attr"`
	Statement          string `xml:"statement,attr"`
	ParametersVariable string `xml:"parametersVariable,attr"`
	ResultVariable     string `xml:"resultVariable,attr"`
	MaxRows            string `xml:"maxRows,attr"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// xmlLdifConnector is the <atlas:ldifConnector> extension on a service task
// (ADR-0171). format is "ldif" or "dsml" — required, because guessing a directory
// file's format from its bytes is how a malformed file becomes a plausible-looking
// empty result. operation is "read" (the default) or "write"; source names the
// variable holding the file text or the entries, and resultVariable the one receiving
// the entries or the rendered file.
type xmlLdifConnector struct {
	Format         string `xml:"format,attr"`
	Operation      string `xml:"operation,attr"`
	Source         string `xml:"source,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// xmlEntraConnector is the <atlas:entraConnector> extension on a service task
// (ADR-0172). worker names the tenant the worker holds the app credential for —
// there is deliberately no tenantId, clientId or secret attribute, because none of
// them enters the engine. operation is the lifecycle step; userId and groupId address
// the objects (literal-or-FEEL); attributesVariable names the variable holding the
// directory properties for create-user and update-user; resultVariable receives what
// Graph returned.
//
// filter, select, pageSize, maxUsers, search and advancedQuery belong to list-users
// (ADR-0172, amended): filter is the OData $filter (literal-or-FEEL), select the
// $select projection, pageSize the $top per request, and maxUsers the cap on what may
// land in the result variable. search is the $search term (literal-or-FEEL) and
// advancedQuery asks for Graph's advanced query support — which a search requires and
// which endsWith, ne and not need too. All are refused on the operations that return
// one object or none.
type xmlEntraConnector struct {
	Connector          string `xml:"connector,attr"`
	Operation          string `xml:"operation,attr"`
	UserID             string `xml:"userId,attr"`
	GroupID            string `xml:"groupId,attr"`
	NewPassword        string `xml:"newPassword,attr"`
	Attributes         string `xml:"attributes,attr"`
	AttributesVariable string `xml:"attributesVariable,attr"`
	ResultVariable     string `xml:"resultVariable,attr"`
	Filter             string `xml:"filter,attr"`
	Select             string `xml:"select,attr"`
	PageSize           string `xml:"pageSize,attr"`
	MaxUsers           string `xml:"maxUsers,attr"`
	Search             string `xml:"search,attr"`
	AdvancedQuery      string `xml:"advancedQuery,attr"`
	DeltaLink          string `xml:"deltaLink,attr"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// xmlLdapConnector is the <atlas:ldapConnector> extension on a service task
// (ADR-0154). url is the server; bindDN/bindSecret authenticate the bind (bindSecret
// a reference, never a value, ADR-0041); startTLS upgrades a plain connection.
// operation selects the directory call. dn is the target entry; baseDN/filter/scope
// address a search; entryVariable names the add/modify attribute object; newPassword
// is the modify-password value. resultVariable receives a search's entries. url/
// bindDN/dn/baseDN/filter/newPassword carry literal-or-FEEL values.
type xmlLdapConnector struct {
	URL            string `xml:"url,attr"`
	BindDN         string `xml:"bindDN,attr"`
	BindSecret     string `xml:"bindSecret,attr"`
	StartTLS       string `xml:"startTLS,attr"`
	Operation      string `xml:"operation,attr"`
	DN             string `xml:"dn,attr"`
	BaseDN         string `xml:"baseDN,attr"`
	Filter         string `xml:"filter,attr"`
	Scope          string `xml:"scope,attr"`
	EntryVariable  string `xml:"entryVariable,attr"`
	NewPassword    string `xml:"newPassword,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	// PageSize and MaxEntries bound a search (ADR-0154, amended). PageSize drives the
	// simple paged-results control so a directory's admin size limit does not truncate
	// or refuse the search; MaxEntries caps how much may land in a process variable.
	// Both are absent-means-default; "0" is the authored way to say unbounded.
	PageSize   string `xml:"pageSize,attr"`
	MaxEntries string `xml:"maxEntries,attr"`
	// ClientCertSecret names the server-side secret holding a PEM bundle (certificate
	// plus private key) for a TLS client-certificate bind. Like bindSecret it is a
	// reference, never a value (ADR-0041).
	ClientCertSecret string `xml:"clientCertSecret,attr"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// xmlHTTPKV is one name/value pair in a REST worker's headers or query
// parameters (an <atlas:httpHeader> or <atlas:queryParam> child element).
type xmlHTTPKV struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// An outbound mail task's parameters, carried on a service task as an
// <atlas:mailConnector connector="..." to="..." .../> extension element (ADR-0079).
// worker names a server-registered mail provider (its host and credentials live
// on the server, never in the model). to (required) is a comma-separated recipient
// list; cc, bcc and from are optional; subject and body are the message. Every field
// value is literal or, with a leading '=', a FEEL expression evaluated over the
// instance's variables at send time (the fx toggle, ADR-0067).
type xmlMailConnector struct {
	Connector string `xml:"connector,attr"`
	To        string `xml:"to,attr"`
	Cc        string `xml:"cc,attr"`
	Bcc       string `xml:"bcc,attr"`
	From      string `xml:"from,attr"`
	Subject   string `xml:"subject,attr"`
	Body      string `xml:"body,attr"`
	// BodyHtml is the optional HTML body (ADR-0079, amended). Authored beside Body,
	// which stays the plain-text alternative; blank means the message is text-only,
	// exactly as before the field existed.
	BodyHtml string `xml:"bodyHtml,attr"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// xmlUserConnector is the <atlas:userConnector> extension of a user-provisioning
// task (ADR-0123). Operation selects the action; the remaining
// attributes are literal-or-FEEL values, like the mail worker's fields.
type xmlUserConnector struct {
	Operation   string `xml:"operation,attr"`
	Username    string `xml:"username,attr"`
	Email       string `xml:"email,attr"`
	DisplayName string `xml:"displayName,attr"`
	Roles       string `xml:"roles,attr"`
	Password    string `xml:"password,attr"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// A CSV-to-JSON task's parameters, carried on a service task as an
// <atlas:csvConnector source="..." delimiter="," .../> extension element (ADR-0139).
// source names the process variable holding the raw CSV text (default "csvText");
// delimiter is the single-character field separator (default ","); hasHeader is
// "true"/"false" (default true) — whether the first row is a header; columns is an
// optional comma-separated list of field names (omit to derive them from the header
// row); resultVariable names the variable the parsed rows are written to (default
// "rows"). The layout lives in the model, so nothing but the file arrives at runtime.
type xmlCsvConnector struct {
	Source         string `xml:"source,attr"`
	Delimiter      string `xml:"delimiter,attr"`
	HasHeader      string `xml:"hasHeader,attr"`
	Columns        string `xml:"columns,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	// Format is the file format ("csv" | "fixed-width" | "avp"; blank is csv) and
	// Operation the direction ("read" | "write"; blank is read) — ADR-0139, amended.
	Format    string `xml:"format,attr"`
	Operation string `xml:"operation,attr"`
	Retries   string `xml:"retries,attr"`
}

// The file formats and directions a csvConnector can author. They are spelled here
// as well as in connector/csvimport because the compiler cannot import the worker
// (the dependency runs the other way); TestCsvFormatsMatchTheConnector guards the seam.
const (
	csvimportFormatCSV        = "csv"
	csvimportFormatFixedWidth = "fixed-width"
	csvimportFormatAVP        = "avp"
	csvimportOperationRead    = "read"
	csvimportOperationWrite   = "write"
)

// csvFormatAndOperation reads the authored format and direction, applying the
// defaults that keep every model written before either existed meaning exactly what
// it meant then.
func csvFormatAndOperation(taskID, rawFormat, rawOp string) (string, string, error) {
	format := strings.ToLower(strings.TrimSpace(rawFormat))
	if format == "" {
		format = csvimportFormatCSV
	}
	switch format {
	case csvimportFormatCSV, csvimportFormatFixedWidth, csvimportFormatAVP:
	default:
		return "", "", fmt.Errorf("compiler: csv task %q has an unknown format %q (want %s, %s, or %s)",
			taskID, rawFormat, csvimportFormatAVP, csvimportFormatCSV, csvimportFormatFixedWidth)
	}
	op := strings.ToLower(strings.TrimSpace(rawOp))
	if op == "" {
		op = csvimportOperationRead
	}
	if op != csvimportOperationRead && op != csvimportOperationWrite {
		return "", "", fmt.Errorf("compiler: csv task %q has an unknown operation %q (want %s or %s)",
			taskID, rawOp, csvimportOperationRead, csvimportOperationWrite)
	}
	return format, op, nil
}

// splitCSVColumns turns a csvConnector's comma-separated columns attribute into a
// trimmed list of field names and, for a fixed-width file, their character widths.
//
// An entry is "name" or "name:width". The width is required for fixed-width, where a
// field is found by position and there is nothing else to find it by, and rejected
// for the other formats — an authored width the worker would ignore is an author
// believing something untrue. An empty result means "derive the columns" (ADR-0139).
func splitCSVColumns(taskID, format, s string) ([]string, []int32, error) {
	if strings.TrimSpace(s) == "" {
		if format == csvimportFormatFixedWidth {
			return nil, nil, fmt.Errorf("compiler: csv task %q reads a fixed-width file, so it must list its columns as name:width", taskID)
		}
		return nil, nil, nil
	}
	var (
		names  []string
		widths []int32
	)
	for _, p := range strings.Split(s, ",") {
		entry := strings.TrimSpace(p)
		if entry == "" {
			continue // a trailing comma names no column
		}
		name, rawWidth, hasWidth := strings.Cut(entry, ":")
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var width int32
		switch {
		case hasWidth && format != csvimportFormatFixedWidth:
			return nil, nil, fmt.Errorf("compiler: csv task %q gives column %q a width, which only a fixed-width file uses", taskID, name)
		case hasWidth:
			n, err := strconv.Atoi(strings.TrimSpace(rawWidth))
			if err != nil {
				return nil, nil, fmt.Errorf("compiler: csv task %q has a non-numeric width for column %q", taskID, name)
			}
			if n <= 0 {
				return nil, nil, fmt.Errorf("compiler: csv task %q gives column %q a width of %d; a fixed-width column occupies at least one character", taskID, name, n)
			}
			width = int32(n)
		case format == csvimportFormatFixedWidth:
			return nil, nil, fmt.Errorf("compiler: csv task %q reads a fixed-width file, so column %q needs a width (name:width)", taskID, name)
		}
		names = append(names, name)
		widths = append(widths, width)
	}
	if format != csvimportFormatFixedWidth {
		widths = nil
	}
	return names, widths, nil
}

// csvHasHeader interprets a csvConnector's hasHeader attribute, defaulting to true
// (a header row is present) when the attribute is absent or blank — matching the
// CSV parser's own default (ADR-0084/0090).
func csvHasHeader(attr string) bool {
	s := strings.TrimSpace(attr)
	return s == "" || strings.EqualFold(s, "true")
}

// A SharePoint task's parameters, carried on a service task as an
// <atlas:sharepointConnector connector="..." site="..." list="..."> extension
// element (ADR-0141). worker names a server-registered SharePoint provider (its
// Graph base and OAuth credential live on the server, never in the model). site
// (required) addresses the SharePoint site ("host,/sites/path" or a site id); list
// (required) is the list name or id the item is created in; resultVariable, if set,
// receives the created item's JSON. Each ItemField child is one column value of the
// created item. Every value is literal or, with a leading '=', a FEEL expression
// evaluated over the instance's variables at call time (the fx toggle, ADR-0067).
type xmlSharePointConnector struct {
	Connector      string      `xml:"connector,attr"`
	Site           string      `xml:"site,attr"`
	List           string      `xml:"list,attr"`
	ResultVariable string      `xml:"resultVariable,attr"`
	Fields         []xmlHTTPKV `xml:"itemField"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// A BMC Remedy task's parameters, carried on a service task as an
// <atlas:remedyConnector connector="..." form="..." resultVariable="..."> extension
// element with <atlas:remedyField name="..." value="..."/> children (ADR-0106).
// worker names a server-registered Remedy instance (its base URL and credentials
// live on the server, never in the model). form is the Remedy form the entry is
// created in (e.g. "HPD:IncidentInterface_Create"); each field is one entry value;
// resultVariable, if set, receives the created entry's id. form and every field value
// is literal or, with a leading '=', a FEEL expression over the instance's variables
// at call time (the fx toggle, ADR-0067).
// A Jira task's parameters, carried on a service task as an
// <atlas:jiraConnector connector="..." operation="..." .../> extension element
// (ADR-0201). worker names a server-registered Jira instance (its
// base URL and credential live on the server, never in the model) and operation is the
// issue-tracker operation the task performs.
//
// Which of the remaining attributes apply is decided by the operation, and only by it:
// issueKey addresses one issue (everything but create-issue and search); project,
// issueType, summary and description are what an issue is created with (summary and
// description also carry an update); transition names the workflow step to perform;
// comment is a comment body (its own operation, and optionally alongside a
// transition); assignee is the account an issue is handed to; jql and maxResults are an
// issue search; query is an account search's term, which maxResults caps too and which
// project — the one place project appears without issueType — restricts to the accounts
// that project can assign. jiraField children set any further issue field, including a
// custom field, by its Jira field id or name. Every value is literal or, with a leading
// '=', a FEEL expression evaluated over the variables the task sees at call time.
type xmlJiraConnector struct {
	Connector      string      `xml:"connector,attr"`
	Operation      string      `xml:"operation,attr"`
	IssueKey       string      `xml:"issueKey,attr"`
	Project        string      `xml:"project,attr"`
	IssueType      string      `xml:"issueType,attr"`
	Summary        string      `xml:"summary,attr"`
	Description    string      `xml:"description,attr"`
	Transition     string      `xml:"transition,attr"`
	Comment        string      `xml:"comment,attr"`
	Assignee       string      `xml:"assignee,attr"`
	JQL            string      `xml:"jql,attr"`
	Query          string      `xml:"query,attr"`
	MaxResults     string      `xml:"maxResults,attr"`
	ResultVariable string      `xml:"resultVariable,attr"`
	Fields         []xmlHTTPKV `xml:"jiraField"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// A Google Sheets task's parameters, carried on a service task as an
// <atlas:googleSheetsConnector connector="..." operation="..." .../> extension element
// (ADR-0235). The connector attribute names the Worker (whose
// credential lives on the server, never in the model) and operation is the spreadsheet
// operation the task performs. Element and attribute keep the pre-ADR-0203 spelling
// their siblings carry: both are authored in deployed models.
//
// Which of the remaining attributes apply is decided by the operation, and only by it:
// spreadsheet addresses an existing file by id or by its URL (everything but
// create-spreadsheet); sheet is a tab title (added, deleted, or a new file's first
// tab); range is A1 notation for the four cell-level operations and may name its own
// sheet; title and folder are what a new spreadsheet is called and where it is put;
// values is what write-range and append-row write; columns names the fields and the
// order a list of objects is projected through; valueInput chooses whether a written
// value is interpreted as typed ("user", the default) or stored verbatim ("raw");
// header makes a read answer with objects keyed by the range's first row.
//
// spreadsheet, sheet, range, title, folder and values are each literal or, with a
// leading '=', a FEEL expression evaluated over the variables the task sees at call
// time (the fx toggle, ADR-0067). columns, valueInput and header are not: each decides
// the shape of the call rather than its content, and a shape that could differ on
// every token is not a shape.
type xmlGoogleSheetsConnector struct {
	Connector      string `xml:"connector,attr"`
	Operation      string `xml:"operation,attr"`
	Spreadsheet    string `xml:"spreadsheet,attr"`
	Sheet          string `xml:"sheet,attr"`
	Range          string `xml:"range,attr"`
	Title          string `xml:"title,attr"`
	Folder         string `xml:"folder,attr"`
	Values         string `xml:"values,attr"`
	Columns        string `xml:"columns,attr"`
	ValueInput     string `xml:"valueInput,attr"`
	Header         string `xml:"header,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	// Retries is the connector task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

type xmlRemedyConnector struct {
	Connector      string      `xml:"connector,attr"`
	Form           string      `xml:"form,attr"`
	ResultVariable string      `xml:"resultVariable,attr"`
	Fields         []xmlHTTPKV `xml:"remedyField"`
	// Retries is the task's own retry budget (ADR-0135), overriding a
	// <zeebe:taskDefinition retries> on the same task; blank means the default.
	Retries string `xml:"retries,attr"`
}

// A web-scraping task's parameters, carried on a service task as an
// <atlas:webscrapeConnector> extension (ADR-0118/0190,
// ADR-0231). url and resultVariable are always
// required. format is a structural literal (html by default, rss, or atom); maxItems
// is an optional non-negative structural bound. HTML requires selector and may name
// attribute; RSS/Atom prohibit both. url and the HTML selector may be
// literal-or-FEEL; everything else is compiled structure.
//
// Fields are the optional <atlas:scrapeField> children. With any, selector picks
// *items* and each match becomes an object of these fields rather than a string —
// which is why they are structure and not a runtime value: they are the result's
// shape. absoluteLinks (HTML) resolves href/src reads against the fetched document's
// final URL; plainText (feeds) strips markup from an entry's description.
type xmlWebScrapeConnector struct {
	Url            string           `xml:"url,attr"`
	Selector       string           `xml:"selector,attr"`
	Attribute      string           `xml:"attribute,attr"`
	Format         string           `xml:"format,attr"`
	MaxItems       string           `xml:"maxItems,attr"`
	AbsoluteLinks  string           `xml:"absoluteLinks,attr"`
	PlainText      string           `xml:"plainText,attr"`
	Fields         []xmlScrapeField `xml:"scrapeField"`
	ResultVariable string           `xml:"resultVariable,attr"`
	Retries        string           `xml:"retries,attr"`
}

// One <atlas:scrapeField> child of a web-scraping task: the object key its
// value lands under, the optional CSS selector evaluated within the matched item
// (empty = the item element itself), and the optional attribute read from it (empty =
// that element's text).
type xmlScrapeField struct {
	Name      string `xml:"name,attr"`
	Selector  string `xml:"selector,attr"`
	Attribute string `xml:"attribute,attr"`
}

type xmlTaskDefinition struct {
	Type    string `xml:"type,attr"`
	Retries string `xml:"retries,attr"`
}

// A mockup service task's parameters, carried on a service task as an
// <atlas:mockupConnector minDuration="PT1S" maxDuration="PT5S" .../> extension
// element (ADR-0120): the engine simulates the task itself. minDuration/maxDuration
// are ISO-8601 durations bounding the random simulated execution time (a single
// fixed duration is minDuration == maxDuration). resultExpression, when set, is a
// FEEL expression (a leading '=' is optional and stripped) evaluated over the
// instance's variables and written into resultVariable — the input→output script,
// e.g. a simulated REST response. failRate is the failure probability in [0,1].
// When errorCode is set, a simulated failure throws a BPMN error with that code
// (caught by a matching error boundary/event subprocess); otherwise it raises an
// incident with failMessage.
type xmlMockupConnector struct {
	MinDuration      string `xml:"minDuration,attr"`
	MaxDuration      string `xml:"maxDuration,attr"`
	ResultVariable   string `xml:"resultVariable,attr"`
	ResultExpression string `xml:"resultExpression,attr"`
	FailRate         string `xml:"failRate,attr"`
	FailMessage      string `xml:"failMessage,attr"`
	ErrorCode        string `xml:"errorCode,attr"`
}

// Zeebe script tasks carry the FEEL expression and its result variable in a
// <zeebe:script> extension element. A script task authored in a general-purpose
// language instead carries an <atlas:jobScript> extension (JobScript); the
// distinct local name keeps it from colliding with <zeebe:script> in the XML
// decoder, which matches by local name (ADR-0047).
type xmlScriptTask struct {
	Id     string         `xml:"id,attr"`
	Script xmlZeebeScript `xml:"extensionElements>script"`
	// JobScript, when present, marks this a polyglot job script (PowerShell, …),
	// run via the job path rather than inline as FEEL. The pointer is nil when the
	// <atlas:jobScript> extension is absent.
	JobScript     *xmlAtlasScript            `xml:"extensionElements>jobScript"`
	IOMapping     xmlZeebeIOMapping          `xml:"extensionElements>ioMapping"`
	MultiInstance *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop  *xmlStandardLoop           `xml:"standardLoopCharacteristics"`
	DataOut       []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn        []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

type xmlZeebeScript struct {
	Expression     string `xml:"expression,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
}

// An <atlas:jobScript language="..." resultVariable="...">body</atlas:jobScript>
// extension: a script task in a general-purpose language (ADR-0047). language
// selects the interpreter/worker (and thus the reserved job type), resultVariable
// is the process variable the script's result is written back into, and the
// element text is the script source. retries is the script job's retry budget
// (ADR-0135) — a script fails like any other job — blank meaning the default.
type xmlAtlasScript struct {
	Language       string `xml:"language,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	Retries        string `xml:"retries,attr"`
	Source         string `xml:",chardata"`
}

// A business rule task references a DMN decision via the Zeebe calledDecision
// extension (<zeebe:calledDecision decisionId="..." resultVariable="..."/>). Its
// input context comes from two layers merged at evaluation time:
//
//   - Variable-driven inputs — the real wiring — are Zeebe io-mapping inputs
//     (<zeebe:ioMapping><zeebe:input source="=order.total" target="Amount"/>), a
//     FEEL source evaluated over the instance's variables bound to a decision
//     input name.
//   - Static inputs are constant Atlas decisionInput elements
//     (<atlas:decisionInput name="Season" value="Winter"/>); each value is parsed
//     as JSON when it parses, else kept as a string, so numbers and booleans reach
//     the decision with their FEEL types. They are a constant base a mapping of the
//     same name overrides.
//
// The decision's result is written back into the resultVariable process variable.
type xmlBusinessRuleTask struct {
	Id             string            `xml:"id,attr"`
	CalledDecision xmlCalledDecision `xml:"extensionElements>calledDecision"`
	// Form binds a repair form to this task, as on a service task (ADR-0169).
	Form          xmlFormDefinition    `xml:"extensionElements>formDefinition"`
	Inputs        []xmlDecisionInput   `xml:"extensionElements>decisionInput"`
	InputMappings []xmlZeebeIOMapInput `xml:"extensionElements>ioMapping>input"`
	// TemisConnector, when present, marks this a central (worker) decision
	// evaluated by a remote temis service rather than the embedded library
	// (ADR-0050). The pointer is nil when the <atlas:temisConnector> extension is
	// absent, i.e. the decision is evaluated locally.
	TemisConnector *xmlTemisConnector         `xml:"extensionElements>temisConnector"`
	MultiInstance  *xmlMultiInstance          `xml:"multiInstanceLoopCharacteristics"`
	StandardLoop   *xmlStandardLoop           `xml:"standardLoopCharacteristics"`
	DataOut        []xmlDataOutputAssociation `xml:"dataOutputAssociation"`
	DataIn         []xmlDataInputAssociation  `xml:"dataInputAssociation"`
}

// xmlTemisConnector is the <atlas:temisConnector connector="..."/> extension that
// routes a business rule task to a server-registered temis worker for central
// evaluation (ADR-0050).
type xmlTemisConnector struct {
	Connector string `xml:"connector,attr"`
}

type xmlCalledDecision struct {
	DecisionId     string `xml:"decisionId,attr"`
	ResultVariable string `xml:"resultVariable,attr"`
	Retries        string `xml:"retries,attr"`
	BindingType    string `xml:"bindingType,attr"`
}

// decisionBinding maps a zeebe:calledDecision bindingType to a compiled binding
// (ADR-0063). "deployment" pins to the process's own snapshot; anything else —
// including the empty default and the not-yet-supported "versionTag" — resolves to
// the latest deployed version, Camunda's default.
func decisionBinding(bindingType string) DecisionBinding {
	if bindingType == "deployment" {
		return BindingDeployment
	}
	return BindingLatest
}

type xmlDecisionInput struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// xmlZeebeIOMapInput is a Zeebe io-mapping input: a FEEL source expression bound
// to a target name. For a business rule task the target is the DMN decision input
// name the source's value feeds; for the generic activity ioMapping (ADR-0068) it
// is the activity-local variable the source's value is written to.
type xmlZeebeIOMapInput struct {
	Source string `xml:"source,attr"`
	Target string `xml:"target,attr"`
}

// xmlZeebeIOMapOutput is a Zeebe io-mapping output: a FEEL source expression
// (evaluated over the activity-local scope) promoted into the parent scope under
// target (ADR-0068). It has the same shape as an input; the two are kept distinct
// so the direction reads clearly in the task structs and helpers.
type xmlZeebeIOMapOutput struct {
	Source string `xml:"source,attr"`
	Target string `xml:"target,attr"`
}

// xmlZeebeIOMapping is a generic <zeebe:ioMapping> on an activity: input mappings
// applied on activation and output mappings applied on completion (ADR-0068). It is
// distinct from a business rule task's ioMapping inputs, which have decision-input
// semantics; here both directions are plain variable mappings.
type xmlZeebeIOMapping struct {
	Inputs  []xmlZeebeIOMapInput  `xml:"input"`
	Outputs []xmlZeebeIOMapOutput `xml:"output"`
}

// decisionInputs turns parsed <decisionInput> elements into a name→value map,
// parsing each value as JSON when possible so numbers and booleans keep their
// types (a plain string that is not valid JSON is used verbatim).
func decisionInputs(in []xmlDecisionInput) (map[string]any, error) {
	if len(in) == 0 {
		return nil, nil
	}
	m := make(map[string]any, len(in))
	for _, di := range in {
		if di.Name == "" {
			return nil, fmt.Errorf("decisionInput with empty name")
		}
		if _, dup := m[di.Name]; dup {
			return nil, fmt.Errorf("duplicate decisionInput name %q", di.Name)
		}
		var v any
		if err := json.Unmarshal([]byte(di.Value), &v); err != nil {
			v = di.Value // not JSON: treat as a literal string
		}
		m[di.Name] = v
	}
	return m, nil
}

// decisionInputMappings compiles a business rule task's io-mapping inputs into
// variable-driven decision inputs. Each source is a FEEL expression (compiled
// once at deploy time, invariant I5) evaluated over the instance's variables at
// evaluation time; target names the decision input it feeds. A leading '=' (the
// Zeebe expression marker) is trimmed. An empty target or an uncompilable source
// fails the deploy, exactly like a bad script-task expression.
func decisionInputMappings(taskID string, in []xmlZeebeIOMapInput) ([]DecisionInputMapping, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]DecisionInputMapping, 0, len(in))
	for _, im := range in {
		if im.Target == "" {
			return nil, fmt.Errorf("compiler: business rule task %q has an input mapping with no target", taskID)
		}
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(im.Source), "="))
		if text == "" {
			return nil, fmt.Errorf("compiler: business rule task %q input mapping for %q has no source expression", taskID, im.Target)
		}
		e, err := expr.CompileAuto(text)
		if err != nil {
			return nil, fmt.Errorf("compiler: business rule task %q input mapping for %q: %w", taskID, im.Target, err)
		}
		out = append(out, DecisionInputMapping{Target: im.Target, Source: e})
	}
	return out, nil
}

type xmlSequenceFlow struct {
	Id        string `xml:"id,attr"`
	SourceRef string `xml:"sourceRef,attr"`
	TargetRef string `xml:"targetRef,attr"`
	// Condition is the FEEL guard text from a <conditionExpression> child, if any.
	Condition string `xml:"conditionExpression"`
}
