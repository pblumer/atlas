package s3

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// An S3 task resolved into plain values, and the function that performs it.
//
// This is ADR-0168's split applied to an object store, and it is what makes the S3 Worker
// Type an ordinary external worker rather than one the engine alone can run. Finding the
// task's detail in the compiled process and evaluating every authored value against the
// variables the task sees up its scope chain (ADR-0068/0174) needs the compiled process
// and the store, which only the engine has — so [Resolve] does it and produces plain
// values. The access key is never among them: what travels is the Worker's *name*, and
// [Run] looks that name up in the registry the caller was built with.
//
// An S3 worker can therefore hold a key the engine has never seen, and write into a bucket
// the engine cannot reach, without that identity ever entering the engine's process. It is
// also what keeps a document's bytes off the engine: on an offloaded installation a put's
// content goes from the worker to the store, and the engine only ever saw the variable it
// was composed from.

// Job is an S3 task with everything already evaluated: which Worker, which operation, and
// the operation's values.
//
// Every field here is model-authored or instance-derived. There is nowhere in a Job to put
// an access key, and that is a property of the type rather than of the code that fills it
// in.
type Job struct {
	// Connector names the S3 Worker the *worker* is configured for. A name and not a key,
	// for the reason the type doc gives.
	Connector string `json:"connector"`
	// Operation is one of [OpNames]; the compiler refused an unknown one at deploy.
	Operation string `json:"operation"`
	// Bucket and Key address the object. A listing has no key — it addresses Prefix.
	Bucket string `json:"bucket,omitempty"`
	Key    string `json:"key,omitempty"`
	// Content is the document a put writes, in the form Encoding names.
	Content string `json:"content,omitempty"`
	// ContentType is what the bytes are, on a put and on a presigned upload.
	ContentType string `json:"contentType,omitempty"`
	// Encoding is EncodingText or EncodingBase64; the compiler has already defaulted it.
	Encoding string `json:"encoding,omitempty"`
	// Prefix, Delimiter, StartAfter and MaxKeys are a listing's shape: what to match, how
	// to roll it up, where to resume and how much to answer with.
	Prefix     string `json:"prefix,omitempty"`
	Delimiter  string `json:"delimiter,omitempty"`
	StartAfter string `json:"startAfter,omitempty"`
	MaxKeys    int32  `json:"maxKeys,omitempty"`
	// SourceBucket and SourceKey are what a copy copies from.
	SourceBucket string `json:"sourceBucket,omitempty"`
	SourceKey    string `json:"sourceKey,omitempty"`
	// ExpiresIn is a presigned URL's lifetime in seconds; the compiler has already
	// defaulted it and refused a value past seven days.
	ExpiresIn int32 `json:"expiresIn,omitempty"`
	// Metadata are extra request headers keyed by name, each already resolved to text.
	Metadata map[string]string `json:"metadata,omitempty"`
	// ResultVariable names the process variable the store's answer is written to; empty
	// means the model discards it.
	ResultVariable string `json:"resultVariable,omitempty"`
}

// Resolve turns a compiled S3 task into a [Job]: the authored operation and every value it
// carries, evaluated against the variables the task sees. It is engine work by necessity —
// FEEL is compiled at deploy (ADR-0008/0015) and the scope lives in the store.
//
// It does not re-validate the operation. The compiler refused an unknown one at deploy and
// [Run]'s client refuses one it does not implement with the list of the ones it does; a
// third check would only be a third message for the same fault.
func Resolve(store state.Reader, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("s3: task has no detail")
	}
	// Read the variables the task sees once — up its scope chain, so its own input-mapped
	// locals shadow what it inherits (ADR-0068) — and evaluate every authored value
	// against that one snapshot.
	scopeVars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("s3: read variables for element %d: %w", elementInstanceKey, err)
	}
	piKey := ei.ProcessInstanceKey // binds the processInstanceKey builtin; not the read scope
	return Job{
		Connector:      cp.Intern(detail.Connector),
		Operation:      cp.Intern(detail.S3Op),
		Bucket:         resolveValue(detail.S3Bucket, piKey, scopeVars),
		Key:            resolveValue(detail.S3Key, piKey, scopeVars),
		Content:        resolveValue(detail.S3Content, piKey, scopeVars),
		ContentType:    resolveValue(detail.S3ContentType, piKey, scopeVars),
		Encoding:       cp.Intern(detail.S3Encoding),
		Prefix:         resolveValue(detail.S3Prefix, piKey, scopeVars),
		Delimiter:      resolveValue(detail.S3Delimiter, piKey, scopeVars),
		StartAfter:     resolveValue(detail.S3StartAfter, piKey, scopeVars),
		MaxKeys:        detail.S3MaxKeys,
		SourceBucket:   resolveValue(detail.S3SourceBucket, piKey, scopeVars),
		SourceKey:      resolveValue(detail.S3SourceKey, piKey, scopeVars),
		ExpiresIn:      detail.S3ExpiresIn,
		Metadata:       resolveMetadata(detail.S3Metadata, piKey, scopeVars),
		ResultVariable: cp.Intern(detail.ResultVar),
	}, nil
}

// Run performs a resolved job through the caller's own registry and answers with what the
// store returned (nil for a delete, which answers with nothing). It is the whole of the
// worker's half, and the in-process path calls it too, so there is one definition of what
// a resolved S3 task means rather than two that drift.
//
// The Worker lookup comes first: an unconfigured name is the more actionable of the
// failures a job can carry here, and reporting it ahead of anything the operation itself
// might be missing keeps the message an operator sees pointed at the fix (ADR-0158).
func Run(ctx context.Context, j Job, reg *Registry) (any, error) {
	client, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("s3", j.Connector)
	}
	return client.Do(ctx, Request{
		Operation:    j.Operation,
		Bucket:       j.Bucket,
		Key:          j.Key,
		Content:      j.Content,
		ContentType:  j.ContentType,
		Encoding:     j.Encoding,
		Prefix:       j.Prefix,
		Delimiter:    j.Delimiter,
		StartAfter:   j.StartAfter,
		MaxKeys:      j.MaxKeys,
		SourceBucket: j.SourceBucket,
		SourceKey:    j.SourceKey,
		ExpiresIn:    j.ExpiresIn,
		Metadata:     j.Metadata,
	})
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's own key
// (mirrors the engine/REST builtin), so an authored value can reference
// processInstanceKey — which is what puts a traceable back-reference into an object key or
// its metadata.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue turns an authored worker value into a string: a literal verbatim, or a FEEL
// expression evaluated over the scope's variables and coerced to its string form. A FEEL
// null — an absent variable or a failed evaluation — becomes the empty string, matching
// the engine's null-propagating contract (as the REST, SharePoint, Jira and Discord
// workers' values do).
func resolveValue(rv compiler.RestExpr, piKey uint64, scopeVars map[string]model.VariableValue) string {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(bindVars(piKey, scopeVars, rv.Expr.Inputs()))
	if err != nil {
		return ""
	}
	_, _, text := expr.Classify(v)
	return text
}

// resolveMetadata resolves the task's extra headers to text.
//
// Unlike Discord's body fields these are deliberately *not* shape-preserving: an HTTP
// header is a string, and a FEEL object resolved into one would arrive at the store as the
// canonical JSON of a context — which is a value that looks deliberate and is not. A model
// that wants structure in metadata composes the string it wants.
func resolveMetadata(kvs []compiler.RestKV, piKey uint64, scopeVars map[string]model.VariableValue) map[string]string {
	if len(kvs) == 0 {
		return nil
	}
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		out[kv.Name] = resolveValue(kv.Val, piKey, scopeVars)
	}
	return out
}

// bindVars turns the named variables the task sees into a FEEL binding. A name absent from
// the chain is left unbound (FEEL null); the reserved name processInstanceKey binds to the
// process instance's key as a string.
func bindVars(piKey uint64, scopeVars map[string]model.VariableValue, names []string) map[string]expr.Value {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]expr.Value, len(names))
	for _, n := range names {
		if n == builtinProcessInstanceKey {
			m[n] = expr.String(strconv.FormatUint(piKey, 10))
			continue
		}
		if v, ok := scopeVars[n]; ok {
			m[n] = expr.FromStored(toExprKind(v.Kind), v.Bool, v.Text)
		}
	}
	return m
}

// toExprKind maps a stored variable kind to the expr kind for binding it into an
// evaluation (mirrors the Discord worker's mapping so the two enums evolve independently).
func toExprKind(k model.VarKind) expr.ValueKind {
	switch k {
	case model.VarBool:
		return expr.KindBool
	case model.VarNumber:
		return expr.KindNumber
	case model.VarString:
		return expr.KindString
	case model.VarJSON:
		return expr.KindJSON
	default:
		return expr.KindNull
	}
}

// resultVariable turns what the store returned into the process variable named by the
// task's result variable. The value is canonicalized through the same expr path as any
// other variable (a scalar stays a scalar, an object or array becomes a structured
// VarJSON), so it round-trips on replay exactly like a REST response.
func resultVariable(name string, body any) model.VariableValue {
	kind, b, text := expr.Classify(expr.FromJSON(body))
	return model.VariableValue{Name: name, Kind: toVarKind(kind), Bool: b, Text: text}
}

// toVarKind maps an expr value kind to the stored variable kind (mirrors the Discord
// worker's mapping so the two enums evolve independently).
func toVarKind(k expr.ValueKind) model.VarKind {
	switch k {
	case expr.KindBool:
		return model.VarBool
	case expr.KindNumber:
		return model.VarNumber
	case expr.KindString:
		return model.VarString
	case expr.KindJSON:
		return model.VarJSON
	default:
		return model.VarNull
	}
}
