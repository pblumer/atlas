package panorama

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type match struct {
	aliases map[string]int
	nodes   []int
	edges   []int
}

func executeGraphQuery(graph Graph, req QueryRequest, limits QueryLimits) (QueryResponse, error) {
	return executeGraphQueryContext(context.Background(), graph, req, limits)
}

func executeGraphQueryContext(ctx context.Context, graph Graph, req QueryRequest, limits QueryLimits) (QueryResponse, error) {
	if limits.MaxQueryBytes > 0 && len(req.Query) > limits.MaxQueryBytes {
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
	return QueryResponse{
		Profile: QueryProfileV1, Graph: result, Complete: true, Limits: limits,
	}, nil
}

func matchPlan(ctx context.Context, graph Graph, plan queryPlan, params map[string]any, source string, limits QueryLimits) ([]match, error) {
	matches := make([]match, 0)
	if plan.edge == nil {
		for i := range graph.Nodes {
			if err := checkQueryContext(ctx, source); err != nil {
				return nil, err
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
				if limits.MaxMatches > 0 && len(matches) > limits.MaxMatches {
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
		usedEdges := make(map[int]bool, plan.edge.max)
		var walk func(int, int) error
		walk = func(current, depth int) error {
			if err := checkQueryContext(ctx, source); err != nil {
				return err
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
					if limits.MaxMatches > 0 && len(matches) > limits.MaxMatches {
						return newQueryError("match_limit", "query exceeded maximum matches", 0, source)
					}
				}
			}
			// Restricted nodes are deliberately terminal even if a malformed future
			// graph contains outgoing edges. This makes authorization a property of the
			// evaluator as well as of the current derivation.
			if depth == plan.edge.max || graph.Nodes[current].Kind == KindRestricted {
				return nil
			}
			for _, edgeIndex := range byFrom[graph.Nodes[current].ID] {
				visitedEdges++
				if limits.MaxVisitedEdges > 0 && visitedEdges > limits.MaxVisitedEdges {
					return newQueryError("visited_edge_limit", "query exceeded maximum visited edges", 0, source)
				}
				if usedEdges[edgeIndex] {
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
				usedEdges[edgeIndex] = true
				pathEdges = append(pathEdges, edgeIndex)
				pathNodes = append(pathNodes, next)
				if err := walk(next, depth+1); err != nil {
					return err
				}
				pathNodes = pathNodes[:len(pathNodes)-1]
				pathEdges = pathEdges[:len(pathEdges)-1]
				delete(usedEdges, edgeIndex)
			}
			return nil
		}
		if err := walk(startIndex, 0); err != nil {
			return nil, err
		}
	}
	return matches, nil
}

func checkQueryContext(ctx context.Context, source string) error {
	if err := ctx.Err(); err != nil {
		return newQueryError("deadline", "query evaluation deadline exceeded", 0, source)
	}
	return nil
}

func nodeMatches(n Node, p nodePattern) bool { return p.kind == "" || n.Kind == p.kind }

func projectMatches(graph Graph, plan queryPlan, matches []match, source string, limits QueryLimits) (Graph, error) {
	selectedNodes := make(map[int]bool)
	selectedEdges := make(map[int]bool)
	returnPath := false
	returnEdge := false
	for _, name := range plan.returns {
		if name == plan.pathAlias && plan.pathAlias != "" {
			returnPath = true
		}
		if plan.edge != nil && name == plan.edge.alias && plan.edge.alias != "" {
			returnEdge = true
		}
	}
	for _, m := range matches {
		if returnPath || returnEdge {
			for _, i := range m.edges {
				selectedEdges[i] = true
			}
			// An edge without its endpoints is not renderable by Starmap.
			for _, i := range m.nodes {
				selectedNodes[i] = true
			}
		}
		for _, name := range plan.returns {
			if name == plan.pathAlias || (plan.edge != nil && name == plan.edge.alias) {
				continue
			}
			if i, ok := m.aliases[name]; ok {
				selectedNodes[i] = true
			}
		}
	}
	if limits.MaxReturnedNodes > 0 && len(selectedNodes) > limits.MaxReturnedNodes {
		return Graph{}, newQueryError("returned_node_limit", "query exceeded maximum returned nodes", 0, source)
	}
	if limits.MaxReturnedEdges > 0 && len(selectedEdges) > limits.MaxReturnedEdges {
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

func (e boolExpr) eval(g Graph, m match, p map[string]any, source string) (bool, error) {
	left, err := e.left.eval(g, m, p, source)
	if err != nil {
		return false, err
	}
	if e.op == "AND" && !left {
		return false, nil
	}
	if e.op == "OR" && left {
		return true, nil
	}
	right, err := e.right.eval(g, m, p, source)
	if err != nil {
		return false, err
	}
	if e.op == "AND" {
		return left && right, nil
	}
	return left || right, nil
}

func (e boolExpr) validate(aliases map[string]string, params map[string]any, source string) error {
	if err := e.left.validate(aliases, params, source); err != nil {
		return err
	}
	return e.right.validate(aliases, params, source)
}

func (e notExpr) eval(g Graph, m match, p map[string]any, source string) (bool, error) {
	v, err := e.inner.eval(g, m, p, source)
	return !v, err
}

func (e notExpr) validate(aliases map[string]string, params map[string]any, source string) error {
	return e.inner.validate(aliases, params, source)
}

func (e compareExpr) validate(aliases map[string]string, params map[string]any, source string) error {
	if err := validatePropertyRef(e.ref, aliases, source); err != nil {
		return err
	}
	if e.value.param == "" {
		return validateComparisonType(e.ref, aliases, e.value.literal, e.value.pos, source)
	}
	value, ok := params[e.value.param]
	if !ok {
		return newQueryError("missing_parameter", fmt.Sprintf("missing parameter $%s", e.value.param), e.value.pos, source)
	}
	return validateComparisonType(e.ref, aliases, value, e.value.pos, source)
}

func validateComparisonType(ref propertyRef, aliases map[string]string, value any, pos int, source string) error {
	kind := aliases[ref.alias]
	expected := queryPropertyType(kind, ref.property)
	if expected == "" {
		return nil
	}
	actual := queryValueType(value)
	if actual == expected {
		return nil
	}
	return newQueryError("type_mismatch", fmt.Sprintf("%s.%s expects %s, got %s", ref.alias, ref.property, expected, actual), pos, source)
}

func queryPropertyType(kind, property string) string {
	if kind != "" {
		for _, p := range queryPropertiesByKind[kind] {
			if p.Name == property {
				return p.Type
			}
		return ""
	}
	var found string
	for _, props := range queryPropertiesByKind {
		for _, p := range props {
			if p.Name != property {
				continue
			}
			if found == "" {
				found = p.Type
			} else if found != p.Type {
				return ""
			}
		}
	}
	return found
}

func queryValueType(v any) string {
	if _, ok := numberValue(v); ok {
		return "number"
	}
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return "object"
	}
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
	if as, ok := a.(string); ok {
		bs, ok := b.(string)
		if !ok {
			return 0, false
		}
		return strings.Compare(as, bs), true
	}
	if ab, ok := a.(bool); ok {
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

func evalWhere(e expr, graph Graph, m match, params map[string]any, source string) (bool, error) {
	if e == nil {
		return true, nil
	}
	return e.eval(graph, m, params, source)
}
