package panorama

import (
	"fmt"
	"strings"
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
	MaxQueryBytes    int `json:"maxQueryBytes"`
	MaxTokens        int `json:"maxTokens"`
	MaxPathDepth     int `json:"maxPathDepth"`
	MaxVisitedEdges  int `json:"maxVisitedEdges"`
	MaxMatches       int `json:"maxMatches"`
	MaxReturnedNodes int `json:"maxReturnedNodes"`
	MaxReturnedEdges int `json:"maxReturnedEdges"`
	DeadlineMillis   int `json:"deadlineMillis"`
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

// QueryLimitHit identifies the server-owned bound that stopped evaluation. v1
// returns a query error on a hit; the field is reserved in the success envelope so
// a later explicitly-partial mode cannot silently change the wire contract.
type QueryLimitHit struct {
	Name string `json:"name"`
}

// QueryResponse is a renderable Starmap projection plus the contract needed to
// distinguish a complete answer from a future explicitly-partial one.
type QueryResponse struct {
	Profile  string         `json:"profile"`
	Graph    Graph          `json:"graph"`
	Complete bool           `json:"complete"`
	Limit    *QueryLimitHit `json:"limit,omitempty"`
	Limits   QueryLimits    `json:"limits"`
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
	Profile           string                  `json:"profile"`
	NodeKinds         []QueryNodeKind         `json:"nodeKinds"`
	RelationshipKinds []QueryRelationshipKind `json:"relationshipKinds"`
	Clauses           []string                `json:"clauses"`
	Operators         []string                `json:"operators"`
	Limits            QueryLimits             `json:"limits"`
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
	KindRestricted: []QueryProperty{{Name: "id", Type: "string"}, {Name: "kind", Type: "string"}},
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
		RelationshipKinds: []QueryRelationshipKind{
			{Name: EdgeContains}, {Name: EdgeCalls}, {Name: EdgeUses},
		},
		Clauses:   []string{"MATCH", "WHERE", "RETURN", "ORDER BY", "LIMIT"},
		Operators: []string{"=", "!=", "<>", "<", "<=", ">", ">=", "AND", "OR", "NOT"},
		Limits:    DefaultQueryLimits(),
	}
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
