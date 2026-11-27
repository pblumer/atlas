package mcptool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// CodeToolError is the BPMN error an MCP task throws when the tool ran and reported a
// failure of its own (isError). It is thrown only when the process catches it; a model
// that does not gets a failed job, as for any other failure.
const CodeToolError = "MCP_TOOL_ERROR"

// Job is a resolved MCP task as it travels to a worker (ADR-draft-mcp-worker): which
// configured server, which tool, and the arguments with their FEEL already evaluated.
// There is nowhere in it to put a credential or an endpoint — both are the worker's.
type Job struct {
	// Connector names the configured MCP Worker: the server.
	Connector string `json:"connector"`
	// Tool is the one tool to call, by the name the server gives it.
	Tool string `json:"tool"`
	// Arguments are the tool's arguments.
	Arguments map[string]any `json:"arguments,omitempty"`
	// ResultVariable is where the result lands; empty when the task wants none.
	ResultVariable string `json:"resultVariable,omitempty"`
	// TimeoutMs bounds the call in milliseconds; 0 leaves the worker's own bound.
	TimeoutMs int64 `json:"timeoutMs,omitempty"`
	// ErrorCodes are the BPMN error codes the process catches, which is what decides
	// whether a tool's own failure is thrown or fails the job.
	ErrorCodes []string `json:"errorCodes,omitempty"`
	// Refused, when set, is why this call must not be made: the Worker's list does not
	// permit the tool. The server states it when it resolves the job, from the Worker's
	// record, and a worker that honours it makes no call at all.
	Refused string `json:"refused,omitempty"`
}

// VarStore is the part of the store a task is resolved from.
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// Resolve turns a compiled MCP task into a Job: the Worker's name and the arguments,
// evaluated against the variables the task sees. It is engine work by necessity — FEEL
// is compiled at deploy (I5) and the scope lives in the store — and it is the whole of
// what the engine contributes (ADR-0168).
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil || cp == nil {
		return Job{}, fmt.Errorf("mcp: task has no detail")
	}
	vars, err := state.VisibleVariablesMap(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("mcp: read variables: %w", err)
	}
	piKey := uint64(0)
	if ei, ok, err := store.GetElementInstance(elementInstanceKey); err == nil && ok {
		piKey = ei.ProcessInstanceKey
	}
	j := Job{
		Connector:      cp.Intern(detail.Connector),
		Tool:           cp.Intern(detail.McpTool),
		ResultVariable: cp.Intern(detail.ResultVar),
		TimeoutMs:      time.Duration(detail.McpTimeoutNanos).Milliseconds(),
		ErrorCodes:     cp.CaughtErrorCodes(),
	}
	if e := detail.McpConnector.Expr; e != nil {
		v, err := e.Eval(bindVars(piKey, vars, e.Inputs()))
		if err != nil {
			return Job{}, fmt.Errorf("mcp: evaluate the worker expression: %w", err)
		}
		_, _, name := expr.Classify(v)
		if j.Connector = strings.TrimSpace(name); j.Connector == "" {
			return Job{}, fmt.Errorf("mcp: the worker expression evaluated to an empty name; set the variable it reads before this task")
		}
	}
	if e := detail.McpInput.Expr; e != nil {
		v, err := e.Eval(bindVars(piKey, vars, e.Inputs()))
		if err != nil {
			return Job{}, fmt.Errorf("mcp: evaluate the arguments: %w", err)
		}
		kind, _, text := expr.Classify(v)
		if kind != expr.KindJSON {
			return Job{}, fmt.Errorf("mcp: the arguments evaluated to a %v, not an object", kind)
		}
		dec := json.NewDecoder(bytes.NewReader([]byte(text)))
		dec.UseNumber() // a number stays exactly what the process holds
		if err := dec.Decode(&j.Arguments); err != nil || j.Arguments == nil {
			return Job{}, fmt.Errorf("mcp: the arguments are not a JSON object")
		}
	}
	return j, nil
}

// bindVars turns the variables an expression names into a FEEL binding; the reserved
// name processInstanceKey binds to the instance's key, as in every other Worker Type.
func bindVars(piKey uint64, scopeVars map[string]model.VariableValue, names []string) map[string]expr.Value {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]expr.Value, len(names))
	for _, n := range names {
		if n == "processInstanceKey" {
			m[n] = expr.String(strconv.FormatUint(piKey, 10))
			continue
		}
		if v, ok := scopeVars[n]; ok {
			m[n] = expr.FromStored(exprKind(v.Kind), v.Bool, v.Text)
		}
	}
	return m
}

func exprKind(k model.VarKind) expr.ValueKind {
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

// Registry holds a worker's configured MCP servers by the name a task states.
type Registry struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

func NewRegistry() *Registry { return &Registry{clients: map[string]*Client{}} }

func (r *Registry) Register(name string, c *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[name] = c
}

func (r *Registry) Get(name string) (*Client, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.clients[name]
	return c, ok
}

func (r *Registry) names() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.clients))
	for n := range r.clients {
		names = append(names, strconv.Quote(n))
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// CheckEndpoint says whether an endpoint is one a client can be built for: an absolute
// http or https URL. It is what the server checks before it hands a Worker to a
// supervised worker, and what the worker checks when it starts.
func CheckEndpoint(endpoint string) error {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("the endpoint %q is not an http or https URL", endpoint)
	}
	return nil
}

// NewClient builds the client for one configured server. credentials is what the vault
// holds for it: nothing, a token, or a bundle {"token": "…"}. A token is presented as a
// bearer token — an arrangement between an operator and a server the protocol permits
// and does not define.
func NewClient(endpoint, credentials string) (*Client, error) {
	if err := CheckEndpoint(endpoint); err != nil {
		return nil, err
	}
	token := strings.TrimSpace(credentials)
	if strings.HasPrefix(token, "{") {
		var bundle struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(token), &bundle); err != nil {
			return nil, fmt.Errorf("the credential bundle is not JSON: %v", err)
		}
		token = strings.TrimSpace(bundle.Token)
	}
	c := &Client{Endpoint: strings.TrimSpace(endpoint)}
	if token != "" {
		c.Authorize = func(h http.Header) error {
			h.Set("Authorization", "Bearer "+token)
			return nil
		}
	}
	return c, nil
}

// Run makes the call a resolved task stands for and returns the variables it completes
// with.
//
// Three endings, and which one it is matters to the process. The call succeeded: the
// result is the variable. The tool ran and said it failed: a BPMN error when the process
// catches CodeToolError, a failed job otherwise — the tool's own account of what went
// wrong either way. Anything else — the server unreachable, the protocol refused, the
// Worker's list not permitting the tool — is a failed job.
func Run(ctx context.Context, j Job, reg *Registry) (map[string]any, error) {
	if j.Refused != "" {
		return nil, fmt.Errorf("mcp: %s", j.Refused)
	}
	if strings.TrimSpace(j.Tool) == "" {
		return nil, fmt.Errorf("mcp: the task names no tool")
	}
	client, ok := reg.Get(j.Connector)
	if !ok {
		return nil, fmt.Errorf("mcp: this worker holds no MCP server named %q; it holds %s", j.Connector, reg.names())
	}
	if j.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(j.TimeoutMs)*time.Millisecond)
		defer cancel()
	}
	res, err := client.CallTool(ctx, j.Tool, j.Arguments)
	if err != nil {
		return nil, err
	}
	if res.IsError {
		msg := strings.TrimSpace(res.Text)
		if msg == "" {
			msg = "the tool reported a failure and said nothing about it"
		}
		if slices.Contains(j.ErrorCodes, CodeToolError) {
			return nil, &job.BPMNError{Code: CodeToolError, Message: msg}
		}
		return nil, fmt.Errorf("mcp: the tool %q failed: %s", j.Tool, msg)
	}
	if j.ResultVariable == "" {
		return nil, nil
	}
	if res.Structured != nil {
		dec := json.NewDecoder(bytes.NewReader(res.Structured))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("mcp: the tool's structured result is not JSON: %v", err)
		}
		return map[string]any{j.ResultVariable: v}, nil
	}
	return map[string]any{j.ResultVariable: res.Text}, nil
}
