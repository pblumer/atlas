package panorama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/pblumer/atlas/api/httpapi"
)

// QueryProfileV1 is the deliberately small, read-only graph-query surface defined
// by ADR-draft-panorama-read-only-graph-queries. Its syntax is Cypher/GQL-inspired;
// the name intentionally does not claim Cypher, openCypher, or ISO GQL conformance.
const QueryProfileV1 = "panorama-graph-query-v1"

// QueryRequest is one graph query plus values transported separately from its text.
type QueryRequest struct {
	Query      string         `json:"query"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

// QueryLimits are server-owned resource bounds. A client may see them but cannot
// raise them. Exceeding one is an error rather than silent truncation.
type QueryLimits struct {
	MaxQueryBytes     int `json:"maxQueryBytes"`
	MaxTokens         int `json:"maxTokens"`
	MaxPathDepth      int `json:"maxPathDepth"`
	MaxVisitedEdges   int `json:"maxVisitedEdges"`
	MaxMatches        int `json:"maxMatches"`
	MaxReturnedNodes  int `json:"maxReturnedNodes"`
	MaxReturnedEdges  int `json:"maxReturnedEdges"`
	DeadlineMillis    int `json:"deadlineMillis"`
}

// DefaultQueryLimits returns conservative interactive limits for one Starmap query.
func DefaultQueryLimits() QueryLimits {
	return QueryLimits{
		MaxQueryBytes:    16 << 10,
		MaxTokens:        512,
		MaxPathDepth:     8,
		MaxVisitedEdges:  50_000,
		MaxMatches:       5_000,
		MaxReturnedNodes: 2_000,
		MaxReturnedEdges: 4_000,
		DeadlineMillis:   2_000,
	}
}

// QueryResponse is a renderable Starmap projection plus the contract needed to
// tell a complete answer from a bounded one.
type QueryResponse struct {
	Profile  string      `json:"profile"`
	Graph    Graph       `json:"graph"`
	Complete bool        `json:"complete"`
	Limits   QueryLimits `json:"limits"`
}

// QueryProperty describes one safe field exposed by the query schema.
type QueryProperty struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// QueryNodeKind is one node kind and the public properties the language exposes.
type QueryNodeKind struct {
	Name       string          `json:"name"`
	Properties []QueryProperty `json:"properties"`
}

// QueryRelationshipKind is one directed relationship kind.
type QueryRelationshipKind struct {
	Name string `json:"name"`
}

// QuerySchema is the server-owned authoring contract used by Panorama for help and
// semantic validation. It deliberately exposes no endpoint, credential, or store
// fields because those are not part of the landscape contract either.
type QuerySchema struct {
	Profile             string                  `json:"profile"`
	NodeKinds           []QueryNodeKind         `json:"nodeKinds"`
	RelationshipKinds   []QueryRelationshipKind `json:"relationshipKinds"`
	Clauses             []string                `json:"clauses"`
	Operators           []string                `json:"operators"`
	Limits              QueryLimits             `json:"limits"`
}

var commonQueryProperties = []QueryProperty{
	{Name: "id", Type: "string"},
	{Name: "kind", Type: "string"},
	{Name: "name", Type: "string"},
	{Name: "provenance", Type: "string"},
	{Name: "application", Type: "string"},
	{Name: "state", Type: "string"},
	{Name: "severity", Type: "string"},
	{Name: "reason", Type: "string"},
}

var queryPropertiesByKind = map[string][]QueryProperty{
	KindApplication: appendQueryProperties(commonQueryProperties,
		QueryProperty{Name: "incidents", Type: "number"},
		QueryProperty{Name: "children", Type: "number"}),
	KindProcess: appendQueryProperties(commonQueryProperties,
		QueryProperty{Name: "processId", Type: "string"},
		QueryProperty{Name: "version", Type: "number"},
		QueryProperty{Name: "incidents", Type: "number"}),
	KindWorker: appendQueryProperties(commonQueryProperties,
		QueryProperty{Name: "workerType", Type: "string"}),
	KindDecision:   appendQueryProperties(commonQueryProperties),
	KindDraft:      appendQueryProperties(commonQueryProperties, QueryProperty{Name: "processId", Type: "string"}),
	KindRestricted: {{Name: "id", Type: "string"}, {Name: "kind", Type: "string"}},
	KindUnresolved: appendQueryProperties(commonQueryProperties),
	KindTarget:     appendQueryProperties(commonQueryProperties),
}

func appendQueryProperties(base []QueryProperty, extra ...QueryProperty) []QueryProperty {
	out := make([]QueryProperty, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}

// GraphQuerySchema returns the canonical v1 language/schema description.
func GraphQuerySchema() QuerySchema {
	kinds := []string{KindApplication, KindProcess, KindWorker, KindDecision, KindDraft, KindRestricted, KindUnresolved, KindTarget}
	nodes := make([]QueryNodeKind, 0, len(kinds))
	for _, kind := range kinds {
		props := append([]QueryProperty(nil), queryPropertiesByKind[kind]...)
		nodes = append(nodes, QueryNodeKind{Name: kind, Properties: props})
	}
	return QuerySchema{
		Profile: QueryProfileV1,
		NodeKinds: nodes,
		RelationshipKinds: []QueryRelationshipKind{{Name: EdgeContains}, {Name: EdgeCalls}, {Name: EdgeUses}},
		Clauses:   []string{"MATCH", "WHERE", "RETURN", "ORDER BY", "LIMIT"},
		Operators: []string{"=", "!=", "<>", "<", "<=", ">", ">=", "AND", "OR", "NOT"},
		Limits:    DefaultQueryLimits(),
	}
}

// HandleQuerySchema serves the exact vocabulary accepted by HandleQuery.
func (m *Mesh) HandleQuerySchema(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, GraphQuerySchema())
}

// HandleQuery evaluates a read-only query against exactly the same already-
// authorized landscape projection HandleGraph serves. It never sees the full stores
// and never post-filters an unrestricted match set.
func (m *Mesh) HandleQuery(w http.ResponseWriter, r *http.Request) {
	limits := DefaultQueryLimits()
	r.Body = http.MaxBytesReader(w, r.Body, int64(limits.MaxQueryBytes*2))
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req QueryRequest
	if err := dec.Decode(&req); err != nil {
		writeQueryError(w, http.StatusBadRequest, newQueryError("invalid_request", err.Error(), 0, req.Query))
		return
	}
	if err := ensureJSONEOF(dec); err != nil {
		writeQueryError(w, http.StatusBadRequest, newQueryError("invalid_request", err.Error(), 0, req.Query))
		return
	}
	if len(req.Query) > limits.MaxQueryBytes {
		writeQueryError(w, http.StatusBadRequest, newQueryError("query_too_large", "query exceeds max query bytes", 0, req.Query))
		return
	}

	graph, ok := m.derive(w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	if limits.DeadlineMillis > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(limits.DeadlineMillis)*time.Millisecond)
		defer cancel()
	}
	result, err := executeGraphQueryContext(ctx, graph, req, limits)
	if err != nil {
		var qe *QueryError
		if errors.As(err, &qe) {
			writeQueryError(w, http.StatusBadRequest, qe)
			return
		}
		writeQueryError(w, http.StatusBadRequest, newQueryError("invalid_query", err.Error(), 0, req.Query))
		return
	}
	httpapi.JSON(w, http.StatusOK, result)
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return errors.New("request contains more than one JSON value")
	} else if !errors.Is(err, context.Canceled) && err.Error() != "EOF" {
		return err
	}
	return nil
}

// QueryError is a source-positioned parse, semantic, or resource-limit diagnostic.
type QueryError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Offset  int    `json:"offset"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
}

func (e *QueryError) Error() string { return e.Message }

func newQueryError(code, message string, offset int, source string) *QueryError {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	line, column := 1, 1
	for i := 0; i < offset; i++ {
		if source[i] == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	return &QueryError{Code: code, Message: message, Offset: offset, Line: line, Column: column}
}

func writeQueryError(w http.ResponseWriter, status int, err *QueryError) {
	httpapi.JSON(w, status, map[string]any{"error": err})
}

// executeGraphQuery is the deterministic evaluator entry point used by tests and
// by the HTTP path through executeGraphQueryContext.
func executeGraphQuery(graph Graph, req QueryRequest, limits QueryLimits) (QueryResponse, error) {
	return executeGraphQueryContext(context.Background(), graph, req, limits)
}

func executeGraphQueryContext(ctx context.Context, graph Graph, req QueryRequest, limits QueryLimits) (QueryResponse, error) {
	if len(req.Query) > limits.MaxQueryBytes {
		return QueryResponse{}, newQueryError("query_too_large", "query exceeds max query bytes", 0, req.Query)
	}
	plan, err := parseGraphQuery(req.Query, limits)
	if err != nil {
		return QueryResponse{}, err
	}
	if err := validatePlan(plan, req.Parameters, req.Query, limits); err != nil {
		return QueryResponse{}, err
	}
	matches, err := matchPlan(ctx, graph, plan, req.Parameters, req.Query, limits)
	if err != nil {
		return QueryResponse{}, err
	}
	if plan.order != nil {
		sort.SliceStable(matches, func(i, j int) bool {
			a, _ := valueForRef(graph, matches[i], plan.order.ref)
			b, _ := valueForRef(graph, matches[j], plan.order.ref)
			cmp := compareForSort(a, b)
			if plan.order.desc {
				return cmp > 0
			}
			return cmp < 0
		})
	}
	if plan.limit >= 0 && len(matches) > plan.limit {
		matches = matches[:plan.limit]
	}
	result, err := projectMatches(graph, plan, matches, req.Query, limits)
	if err != nil {
		return QueryResponse{}, err
	}
	return QueryResponse{Profile: QueryProfileV1, Graph: result, Complete: true, Limits: limits}, nil
}

type queryPlan struct {
	pathAlias string
	start     nodePattern
	edge      *edgePattern
	end       *nodePattern
	where     expr
	returns   []string
	order     *orderSpec
	limit     int
}

type nodePattern struct {
	alias string
	kind  string
	pos   int
}

type edgePattern struct {
	alias string
	kind  string
	min   int
	max   int
	pos   int
}

type orderSpec struct {
	ref  propertyRef
	desc bool
}

type propertyRef struct {
	alias    string
	property string
	pos      int
}

type match struct {
	aliases map[string]int
	nodes   []int
	edges   []int
}

func validatePlan(p queryPlan, params map[string]any, source string, limits QueryLimits) error {
	aliases := map[string]string{p.start.alias: p.start.kind}
	if p.end != nil {
		if _, exists := aliases[p.end.alias]; exists {
			return newQueryError("duplicate_alias", "duplicate node alias "+p.end.alias, p.end.pos, source)
		}
		aliases[p.end.alias] = p.end.kind
	}
	if p.pathAlias != "" {
		if _, exists := aliases[p.pathAlias]; exists {
			return newQueryError("duplicate_alias", "path alias conflicts with node alias "+p.pathAlias, 0, source)
		}
	}
	for alias, kind := range aliases {
		if alias == "" {
			return newQueryError("missing_alias", "every node in Panorama Graph Query v1 needs an alias", 0, source)
		}
		if kind != "" && !knownNodeKind(kind) {
			return newQueryError("unknown_node_kind", fmt.Sprintf("unknown node kind %q", kind), 0, source)
		}
	}
	if p.edge != nil {
		if p.edge.kind != "" && !knownEdgeKind(p.edge.kind) {
			return newQueryError("unknown_relationship_kind", fmt.Sprintf("unknown relationship kind %q", p.edge.kind), p.edge.pos, source)
		}
		if p.edge.min < 1 || p.edge.max < p.edge.min || p.edge.max > limits.MaxPathDepth {
			return newQueryError("path_depth", fmt.Sprintf("path depth must be bounded between 1 and %d", limits.MaxPathDepth), p.edge.pos, source)
		}
	}
	if p.where != nil {
		if err := p.where.validate(aliases, params, source); err != nil {
			return err
		}
	}
	validReturn := func(v string) bool {
		if v == p.pathAlias && p.pathAlias != "" {
			return true
		}
		_, ok := aliases[v]
		return ok
	}
	for _, v := range p.returns {
		if !validReturn(v) {
			return newQueryError("unknown_return", fmt.Sprintf("RETURN references unknown variable %q", v), 0, source)
		}
	}
	if p.order != nil {
		if err := validatePropertyRef(p.order.ref, aliases, source); err != nil {
			return err
		}
	}
	return nil
}

func knownNodeKind(kind string) bool {
	_, ok := queryPropertiesByKind[kind]
	return ok
}

func knownEdgeKind(kind string) bool {
	switch kind {
	case EdgeContains, EdgeCalls, EdgeUses:
		return true
	default:
		return false
	}
}

func validatePropertyRef(ref propertyRef, aliases map[string]string, source string) error {
	kind, ok := aliases[ref.alias]
	if !ok {
		return newQueryError("unknown_alias", fmt.Sprintf("unknown variable %q", ref.alias), ref.pos, source)
	}
	if kind != "" {
		for _, p := range queryPropertiesByKind[kind] {
			if p.Name == ref.property {
				return nil
			}
		}
		return newQueryError("unknown_property", fmt.Sprintf("property %q is not queryable on %s", ref.property, kind), ref.pos, source)
	}
	for _, props := range queryPropertiesByKind {
		for _, p := range props {
			if p.Name == ref.property {
				return nil
			}
		}
	}
	return newQueryError("unknown_property", fmt.Sprintf("unknown queryable property %q", ref.property), ref.pos, source)
}

func matchPlan(ctx context.Context, graph Graph, plan queryPlan, params map[string]any, source string, limits QueryLimits) ([]match, error) {
	matches := make([]match, 0)
	if plan.edge == nil {
		for i := range graph.Nodes {
			if err := ctx.Err(); err != nil {
				return nil, newQueryError("deadline", "query evaluation deadline exceeded", 0, source)
			}
			if !nodeMatches(graph.Nodes[i], plan.start) {
				continue
			}
			m := match{aliases: map[string]int{plan.start.alias: i}, nodes: []int{i}}
			ok, err := evalWhere(plan.where, graph, m, params, source)
			if err != nil {
				return nil, err
			}
			if ok {
				matches = append(matches, m)
				if len(matches) > limits.MaxMatches {
					return nil, newQueryError("match_limit", "query exceeded maximum matches", 0, source)
				}
			}
		}
		return matches, nil
	}

	byFrom := make(map[string][]int, len(graph.Nodes))
	for i := range graph.Edges {
		byFrom[graph.Edges[i].From] = append(byFrom[graph.Edges[i].From], i)
	}
	indexByID := make(map[string]int, len(graph.Nodes))
	for i := range graph.Nodes {
		indexByID[graph.Nodes[i].ID] = i
	}
	visitedEdges := 0
	for startIndex := range graph.Nodes {
		if !nodeMatches(graph.Nodes[startIndex], plan.start) || graph.Nodes[startIndex].Kind == KindRestricted {
			continue
		}
		pathNodes := []int{startIndex}
		pathEdges := make([]int, 0, plan.edge.max)
		used := make(map[int]bool, plan.edge.max)
		var walk func(int, int) error
		walk = func(current, depth int) error {
			if err := ctx.Err(); err != nil {
				return newQueryError("deadline", "query evaluation deadline exceeded", 0, source)
			}
			if depth >= plan.edge.min && depth <= plan.edge.max && nodeMatches(graph.Nodes[current], *plan.end) {
				m := match{
					aliases: map[string]int{plan.start.alias: startIndex, plan.end.alias: current},
					nodes:   append([]int(nil), pathNodes...),
					edges:   append([]int(nil), pathEdges...),
				}
				ok, err := evalWhere(plan.where, graph, m, params, source)
				if err != nil {
					return err
				}
				if ok {
					matches = append(matches, m)
					if len(matches) > limits.MaxMatches {
						return newQueryError("match_limit", "query exceeded maximum matches", 0, source)
					}
				}
			}
			if depth == plan.edge.max || graph.Nodes[current].Kind == KindRestricted {
				return nil
			}
			for _, edgeIndex := range byFrom[graph.Nodes[current].ID] {
				visitedEdges++
				if visitedEdges > limits.MaxVisitedEdges {
					return newQueryError("visited_edge_limit", "query exceeded maximum visited edges", 0, source)
				}
				if used[edgeIndex] {
					continue
				}
				edge := graph.Edges[edgeIndex]
				if plan.edge.kind != "" && edge.Kind != plan.edge.kind {
					continue
				}
				next, ok := indexByID[edge.To]
				if !ok {
					continue
				}
				used[edgeIndex] = true
				pathEdges = append(pathEdges, edgeIndex)
				pathNodes = append(pathNodes, next)
				if err := walk(next, depth+1); err != nil {
					return err
				}
				pathNodes = pathNodes[:len(pathNodes)-1]
				pathEdges = pathEdges[:len(pathEdges)-1]
				delete(used, edgeIndex)
			}
			return nil
		}
		if err := walk(startIndex, 0); err != nil {
			return nil, err
		}
	}
	return matches, nil
}

func nodeMatches(n Node, p nodePattern) bool { return p.kind == "" || n.Kind == p.kind }

func projectMatches(graph Graph, plan queryPlan, matches []match, source string, limits QueryLimits) (Graph, error) {
	selectedNodes := make(map[int]bool)
	selectedEdges := make(map[int]bool)
	returnPath := false
	for _, name := range plan.returns {
		if name == plan.pathAlias && plan.pathAlias != "" {
			returnPath = true
		}
	}
	for _, m := range matches {
		if returnPath {
			for _, i := range m.nodes {
				selectedNodes[i] = true
			}
			for _, i := range m.edges {
				selectedEdges[i] = true
			}
		}
		for _, name := range plan.returns {
			if name == plan.pathAlias {
				continue
			}
			if i, ok := m.aliases[name]; ok {
				selectedNodes[i] = true
			}
		}
	}
	if len(selectedNodes) > limits.MaxReturnedNodes {
		return Graph{}, newQueryError("returned_node_limit", "query exceeded maximum returned nodes", 0, source)
	}
	if len(selectedEdges) > limits.MaxReturnedEdges {
		return Graph{}, newQueryError("returned_edge_limit", "query exceeded maximum returned edges", 0, source)
	}
	out := Graph{ObservedAt: graph.ObservedAt}
	for i, n := range graph.Nodes {
		if selectedNodes[i] {
			out.Nodes = append(out.Nodes, n)
			if n.Kind == KindRestricted {
				out.Restricted++
			}
		}
	}
	for i, e := range graph.Edges {
		if selectedEdges[i] {
			out.Edges = append(out.Edges, e)
		}
	}
	return out, nil
}

func valueForRef(graph Graph, m match, ref propertyRef) (any, error) {
	i, ok := m.aliases[ref.alias]
	if !ok || i < 0 || i >= len(graph.Nodes) {
		return nil, fmt.Errorf("unknown alias %q", ref.alias)
	}
	return nodeProperty(graph.Nodes[i], ref.property)
}

func nodeProperty(n Node, property string) (any, error) {
	switch property {
	case "id":
		return n.ID, nil
	case "kind":
		return n.Kind, nil
	case "name":
		return n.Name, nil
	case "provenance":
		return n.Provenance, nil
	case "application":
		return n.Application, nil
	case "processId":
		return n.ProcessID, nil
	case "version":
		return float64(n.Version), nil
	case "workerType":
		return n.WorkerType, nil
	case "state":
		return n.State, nil
	case "severity":
		return n.Severity, nil
	case "reason":
		return n.Reason, nil
	case "incidents":
		return float64(n.Incidents), nil
	case "children":
		return float64(n.Children), nil
	default:
		return nil, fmt.Errorf("unknown property %q", property)
	}
}

func compareForSort(a, b any) int {
	switch av := a.(type) {
	case string:
		bv, _ := b.(string)
		return strings.Compare(av, bv)
	case float64:
		bv, _ := b.(float64)
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		default:
			return 0
		}
	default:
		return 0
	}
}

// --- WHERE expressions -------------------------------------------------------

type expr interface {
	eval(Graph, match, map[string]any, string) (bool, error)
	validate(map[string]string, map[string]any, string) error
}

type boolExpr struct {
	op          string
	left, right expr
	pos         int
}

func (e boolExpr) eval(g Graph, m match, p map[string]any, s string) (bool, error) {
	left, err := e.left.eval(g, m, p, s)
	if err != nil {
		return false, err
	}
	if e.op == "AND" && !left {
		return false, nil
	}
	if e.op == "OR" && left {
		return true, nil
	}
	right, err := e.right.eval(g, m, p, s)
	if err != nil {
		return false, err
	}
	if e.op == "AND" {
		return left && right, nil
	}
	return left || right, nil
}

func (e boolExpr) validate(a map[string]string, p map[string]any, s string) error {
	if err := e.left.validate(a, p, s); err != nil {
		return err
	}
	return e.right.validate(a, p, s)
}

type notExpr struct {
	inner expr
}

func (e notExpr) eval(g Graph, m match, p map[string]any, s string) (bool, error) {
	v, err := e.inner.eval(g, m, p, s)
	return !v, err
}
func (e notExpr) validate(a map[string]string, p map[string]any, s string) error {
	return e.inner.validate(a, p, s)
}

type compareExpr struct {
	ref   propertyRef
	op    string
	value queryValue
	pos   int
}

type queryValue struct {
	literal any
	param   string
	pos     int
}

func (e compareExpr) validate(aliases map[string]string, params map[string]any, source string) error {
	if err := validatePropertyRef(e.ref, aliases, source); err != nil {
		return err
	}
	if e.value.param != "" {
		if _, ok := params[e.value.param]; !ok {
			return newQueryError("missing_parameter", fmt.Sprintf("missing parameter $%s", e.value.param), e.value.pos, source)
		}
	}
	return nil
}

func (e compareExpr) eval(g Graph, m match, params map[string]any, source string) (bool, error) {
	left, err := valueForRef(g, m, e.ref)
	if err != nil {
		return false, newQueryError("evaluation", err.Error(), e.pos, source)
	}
	right := e.value.literal
	if e.value.param != "" {
		right = params[e.value.param]
	}
	cmp, comparable := compareValues(left, right)
	if !comparable {
		return false, newQueryError("type_mismatch", fmt.Sprintf("cannot compare %T with %T", left, right), e.pos, source)
	}
	switch e.op {
	case "=":
		return cmp == 0, nil
	case "!=", "<>":
		return cmp != 0, nil
	case "<":
		return cmp < 0, nil
	case "<=":
		return cmp <= 0, nil
	case ">":
		return cmp > 0, nil
	case ">=":
		return cmp >= 0, nil
	default:
		return false, newQueryError("operator", "unsupported comparison operator "+e.op, e.pos, source)
	}
}

func compareValues(a, b any) (int, bool) {
	if af, ok := numberValue(a); ok {
		bf, ok := numberValue(b)
		if !ok {
			return 0, false
		}
		switch {
		case af < bf:
			return -1, true
		case af > bf:
			return 1, true
		default:
			return 0, true
		}
	}
	as, ok := a.(string)
	if ok {
		bs, ok := b.(string)
		if !ok {
			return 0, false
		}
		return strings.Compare(as, bs), true
	}
	ab, ok := a.(bool)
	if ok {
		bb, ok := b.(bool)
		if !ok {
			return 0, false
		}
		if ab == bb {
			return 0, true
		}
		if !ab {
			return -1, true
		}
		return 1, true
	}
	return 0, false
}

func numberValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func evalWhere(e expr, g Graph, m match, params map[string]any, source string) (bool, error) {
	if e == nil {
		return true, nil
	}
	return e.eval(g, m, params, source)
}

// --- lexer and parser --------------------------------------------------------

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString
	tokNumber
	tokParam
	tokLParen
	tokRParen
	tokLBracket
	tokRBracket
	tokColon
	tokComma
	tokDot
	tokStar
	tokDash
	tokArrow
	tokRange
	tokEq
	tokNE
	tokLT
	tokLE
	tokGT
	tokGE
)

type token struct {
	kind tokenKind
	text string
	pos  int
}

func lexGraphQuery(source string, max int) ([]token, error) {
	out := make([]token, 0, 64)
	for i := 0; i < len(source); {
		if unicode.IsSpace(rune(source[i])) {
			i++
			continue
		}
		pos := i
		switch c := source[i]; c {
		case '(':
			out = append(out, token{kind: tokLParen, text: "(", pos: i}); i++
		case ')':
			out = append(out, token{kind: tokRParen, text: ")", pos: i}); i++
		case '[':
			out = append(out, token{kind: tokLBracket, text: "[", pos: i}); i++
		case ']':
			out = append(out, token{kind: tokRBracket, text: "]", pos: i}); i++
		case ':':
			out = append(out, token{kind: tokColon, text: ":", pos: i}); i++
		case ',':
			out = append(out, token{kind: tokComma, text: ",", pos: i}); i++
		case '*':
			out = append(out, token{kind: tokStar, text: "*", pos: i}); i++
		case '-':
			if i+1 < len(source) && source[i+1] == '>' {
				out = append(out, token{kind: tokArrow, text: "->", pos: i}); i += 2
			} else {
				out = append(out, token{kind: tokDash, text: "-", pos: i}); i++
			}
		case '.':
			if i+1 < len(source) && source[i+1] == '.' {
				out = append(out, token{kind: tokRange, text: "..", pos: i}); i += 2
			} else {
				out = append(out, token{kind: tokDot, text: ".", pos: i}); i++
			}
		case '=':
			out = append(out, token{kind: tokEq, text: "=", pos: i}); i++
		case '!':
			if i+1 < len(source) && source[i+1] == '=' {
				out = append(out, token{kind: tokNE, text: "!=", pos: i}); i += 2
			} else {
				return nil, newQueryError("syntax", "expected !=", pos, source)
			}
		case '<':
			if i+1 < len(source) && source[i+1] == '=' {
				out = append(out, token{kind: tokLE, text: "<=", pos: i}); i += 2
			} else if i+1 < len(source) && source[i+1] == '>' {
				out = append(out, token{kind: tokNE, text: "<>", pos: i}); i += 2
			} else {
				out = append(out, token{kind: tokLT, text: "<", pos: i}); i++
			}
		case '>':
			if i+1 < len(source) && source[i+1] == '=' {
				out = append(out, token{kind: tokGE, text: ">=", pos: i}); i += 2
			} else {
				out = append(out, token{kind: tokGT, text: ">", pos: i}); i++
			}
		case '$':
			i++
			start := i
			for i < len(source) && isIdentPart(source[i]) { i++ }
			if start == i {
				return nil, newQueryError("syntax", "parameter needs a name", pos, source)
			}
			out = append(out, token{kind: tokParam, text: source[start:i], pos: pos})
		case '\'', '"':
			quote := c
			i++
			var b strings.Builder
			closed := false
			for i < len(source) {
				if source[i] == quote {
					i++; closed = true; break
				}
				if source[i] == '\\' && i+1 < len(source) {
					i++
					switch source[i] {
					case 'n': b.WriteByte('\n')
					case 't': b.WriteByte('\t')
					default: b.WriteByte(source[i])
					}
					i++
					continue
				}
				b.WriteByte(source[i]); i++
			}
			if !closed { return nil, newQueryError("syntax", "unterminated string", pos, source) }
			out = append(out, token{kind: tokString, text: b.String(), pos: pos})
		default:
			if isIdentStart(c) {
				i++
				for i < len(source) && isIdentPart(source[i]) { i++ }
				out = append(out, token{kind: tokIdent, text: source[pos:i], pos: pos})
			} else if c >= '0' && c <= '9' {
				i++
				for i < len(source) && ((source[i] >= '0' && source[i] <= '9') || source[i] == '.') { i++ }
				out = append(out, token{kind: tokNumber, text: source[pos:i], pos: pos})
			} else {
				return nil, newQueryError("syntax", fmt.Sprintf("unexpected character %q", c), pos, source)
			}
		}
		if len(out) > max {
			return nil, newQueryError("token_limit", "query exceeds maximum token count", pos, source)
		}
	}
	out = append(out, token{kind: tokEOF, pos: len(source)})
	return out, nil
}

func isIdentStart(c byte) bool { return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }
func isIdentPart(c byte) bool  { return isIdentStart(c) || c >= '0' && c <= '9' }

type queryParser struct {
	source string
	tokens []token
	i      int
}

func parseGraphQuery(source string, limits QueryLimits) (queryPlan, error) {
	if strings.TrimSpace(source) == "" {
		return queryPlan{}, newQueryError("syntax", "query is empty", 0, source)
	}
	upper := strings.ToUpper(source)
	for _, forbidden := range []string{" CREATE ", " INSERT ", " MERGE ", " DELETE ", " SET ", " REMOVE ", " CALL ", "LOAD CSV", " TRANSACTION ", " DROP ", " ALTER ", " COUNT("} {
		padded := " " + upper + " "
		if idx := strings.Index(padded, forbidden); idx >= 0 {
			pos := idx
			if pos > 0 { pos-- }
			return queryPlan{}, newQueryError("unsupported", "unsupported or mutating construct", pos, source)
		}
	}
	tokens, err := lexGraphQuery(source, limits.MaxTokens)
	if err != nil {
		return queryPlan{}, err
	}
	p := queryParser{source: source, tokens: tokens}
	return p.parse()
}

func (p *queryParser) parse() (queryPlan, error) {
	plan := queryPlan{limit: -1}
	if err := p.keyword("MATCH"); err != nil { return plan, err }
	if p.peek().kind == tokIdent && p.peekN(1).kind == tokEq {
		plan.pathAlias = p.next().text
		p.next()
	}
	start, err := p.parseNode()
	if err != nil { return plan, err }
	plan.start = start
	if p.peek().kind == tokDash {
		edge, end, err := p.parseEdgeAndNode()
		if err != nil { return plan, err }
		plan.edge, plan.end = &edge, &end
	}
	if p.isKeyword("WHERE") {
		p.next()
		plan.where, err = p.parseOr()
		if err != nil { return plan, err }
	}
	if err := p.keyword("RETURN"); err != nil { return plan, err }
	for {
		t := p.next()
		if t.kind != tokIdent {
			return plan, newQueryError("syntax", "RETURN expects a variable", t.pos, p.source)
		}
		// v1 returns graph variables, never scalar expressions or aggregations.
		if p.peek().kind == tokDot || p.peek().kind == tokLParen {
			return plan, newQueryError("unsupported", "v1 RETURN accepts graph variables, not scalar projections", p.peek().pos, p.source)
		}
		plan.returns = append(plan.returns, t.text)
		if p.peek().kind != tokComma { break }
		p.next()
	}
	if p.isKeyword("ORDER") {
		p.next()
		if err := p.keyword("BY"); err != nil { return plan, err }
		ref, err := p.parsePropertyRef()
		if err != nil { return plan, err }
		plan.order = &orderSpec{ref: ref}
		if p.isKeyword("ASC") { p.next() } else if p.isKeyword("DESC") { p.next(); plan.order.desc = true }
	}
	if p.isKeyword("LIMIT") {
		p.next()
		t := p.next()
		if t.kind != tokNumber || strings.Contains(t.text, ".") {
			return plan, newQueryError("syntax", "LIMIT expects a non-negative integer", t.pos, p.source)
		}
		n, err := strconv.Atoi(t.text)
		if err != nil || n < 0 { return plan, newQueryError("syntax", "invalid LIMIT", t.pos, p.source) }
		plan.limit = n
	}
	if p.peek().kind != tokEOF {
		return plan, newQueryError("syntax", "unexpected token "+p.peek().text, p.peek().pos, p.source)
	}
	if len(plan.returns) == 0 {
		return plan, newQueryError("syntax", "RETURN needs at least one graph variable", p.peek().pos, p.source)
	}
	return plan, nil
}

func (p *queryParser) parseNode() (nodePattern, error) {
	open := p.next()
	if open.kind != tokLParen { return nodePattern{}, newQueryError("syntax", "expected (", open.pos, p.source) }
	alias := p.next()
	if alias.kind != tokIdent { return nodePattern{}, newQueryError("syntax", "node needs an alias", alias.pos, p.source) }
	n := nodePattern{alias: alias.text, pos: alias.pos}
	if p.peek().kind == tokColon {
		p.next()
		kind := p.next()
		if kind.kind != tokIdent { return n, newQueryError("syntax", "expected node kind", kind.pos, p.source) }
		n.kind = strings.ToLower(kind.text)
	}
	close := p.next()
	if close.kind != tokRParen { return n, newQueryError("syntax", "expected )", close.pos, p.source) }
	return n, nil
}

func (p *queryParser) parseEdgeAndNode() (edgePattern, nodePattern, error) {
	dash := p.next()
	e := edgePattern{min: 1, max: 1, pos: dash.pos}
	if p.peek().kind == tokLBracket {
		p.next()
		if p.peek().kind == tokIdent { e.alias = p.next().text }
		if p.peek().kind == tokColon {
			p.next(); t := p.next()
			if t.kind != tokIdent { return e, nodePattern{}, newQueryError("syntax", "expected relationship kind", t.pos, p.source) }
			e.kind = strings.ToLower(t.text)
		}
		if p.peek().kind == tokStar {
			star := p.next()
			if p.peek().kind != tokNumber {
				return e, nodePattern{}, newQueryError("path_depth", "unbounded variable-length paths are not supported; use *min..max", star.pos, p.source)
			}
			minTok := p.next()
			min, err := strconv.Atoi(minTok.text)
			if err != nil || strings.Contains(minTok.text, ".") { return e, nodePattern{}, newQueryError("syntax", "invalid path minimum", minTok.pos, p.source) }
			if p.peek().kind != tokRange {
				return e, nodePattern{}, newQueryError("path_depth", "variable-length paths need an explicit min..max bound", star.pos, p.source)
			}
			p.next()
			maxTok := p.next()
			if maxTok.kind != tokNumber || strings.Contains(maxTok.text, ".") { return e, nodePattern{}, newQueryError("syntax", "invalid path maximum", maxTok.pos, p.source) }
			max, err := strconv.Atoi(maxTok.text)
			if err != nil { return e, nodePattern{}, newQueryError("syntax", "invalid path maximum", maxTok.pos, p.source) }
			e.min, e.max = min, max
		}
		close := p.next()
		if close.kind != tokRBracket { return e, nodePattern{}, newQueryError("syntax", "expected ]", close.pos, p.source) }
	}
	arrow := p.next()
	if arrow.kind != tokArrow { return e, nodePattern{}, newQueryError("syntax", "only directed -> relationships are supported in v1", arrow.pos, p.source) }
	end, err := p.parseNode()
	return e, end, err
}

func (p *queryParser) parseOr() (expr, error) {
	left, err := p.parseAnd()
	if err != nil { return nil, err }
	for p.isKeyword("OR") {
		t := p.next(); right, err := p.parseAnd(); if err != nil { return nil, err }
		left = boolExpr{op: "OR", left: left, right: right, pos: t.pos}
	}
	return left, nil
}

func (p *queryParser) parseAnd() (expr, error) {
	left, err := p.parseUnary()
	if err != nil { return nil, err }
	for p.isKeyword("AND") {
		t := p.next(); right, err := p.parseUnary(); if err != nil { return nil, err }
		left = boolExpr{op: "AND", left: left, right: right, pos: t.pos}
	}
	return left, nil
}

func (p *queryParser) parseUnary() (expr, error) {
	if p.isKeyword("NOT") {
		p.next(); inner, err := p.parseUnary(); if err != nil { return nil, err }; return notExpr{inner: inner}, nil
	}
	if p.peek().kind == tokLParen {
		p.next(); inner, err := p.parseOr(); if err != nil { return nil, err }
		if t := p.next(); t.kind != tokRParen { return nil, newQueryError("syntax", "expected ) in WHERE", t.pos, p.source) }
		return inner, nil
	}
	return p.parseComparison()
}

func (p *queryParser) parseComparison() (expr, error) {
	ref, err := p.parsePropertyRef()
	if err != nil { return nil, err }
	op := p.next()
	switch op.kind {
	case tokEq, tokNE, tokLT, tokLE, tokGT, tokGE:
	default:
		return nil, newQueryError("syntax", "expected comparison operator", op.pos, p.source)
	}
	v := p.next()
	qv := queryValue{pos: v.pos}
	switch v.kind {
	case tokParam:
		qv.param = v.text
	case tokString:
		qv.literal = v.text
	case tokNumber:
		n, err := strconv.ParseFloat(v.text, 64); if err != nil { return nil, newQueryError("syntax", "invalid number", v.pos, p.source) }; qv.literal = n
	case tokIdent:
		switch strings.ToUpper(v.text) { case "TRUE": qv.literal = true; case "FALSE": qv.literal = false; default: return nil, newQueryError("syntax", "expected literal or $parameter", v.pos, p.source) }
	default:
		return nil, newQueryError("syntax", "expected literal or $parameter", v.pos, p.source)
	}
	return compareExpr{ref: ref, op: op.text, value: qv, pos: op.pos}, nil
}

func (p *queryParser) parsePropertyRef() (propertyRef, error) {
	a := p.next()
	if a.kind != tokIdent { return propertyRef{}, newQueryError("syntax", "expected variable", a.pos, p.source) }
	if d := p.next(); d.kind != tokDot { return propertyRef{}, newQueryError("syntax", "expected . after variable", d.pos, p.source) }
	prop := p.next()
	if prop.kind != tokIdent { return propertyRef{}, newQueryError("syntax", "expected property name", prop.pos, p.source) }
	return propertyRef{alias: a.text, property: prop.text, pos: a.pos}, nil
}

func (p *queryParser) keyword(want string) error {
	t := p.next()
	if t.kind != tokIdent || !strings.EqualFold(t.text, want) {
		return newQueryError("syntax", "expected "+want, t.pos, p.source)
	}
	return nil
}
func (p *queryParser) isKeyword(want string) bool { t := p.peek(); return t.kind == tokIdent && strings.EqualFold(t.text, want) }
func (p *queryParser) peek() token { return p.tokens[p.i] }
func (p *queryParser) peekN(n int) token { if p.i+n >= len(p.tokens) { return token{kind: tokEOF, pos: len(p.source)} }; return p.tokens[p.i+n] }
func (p *queryParser) next() token { t := p.peek(); if p.i < len(p.tokens)-1 { p.i++ }; return t }
