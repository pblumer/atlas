package entra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// VarStore is the slice of the state store the engine half reads.
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// Op describes one Entra lifecycle operation: how it reaches Graph, and what a model
// must author for it. Keeping this a table rather than a switch is what lets the
// compiler, the modeler hints and the worker agree on the same rules — a new
// operation is a row, not three edits that can disagree.
type Op struct {
	// Method and the path builder produce the Graph request.
	Method string
	// NeedsUser, NeedsGroup, NeedsAttributes and NeedsPassword are what the compiler
	// validates. NeedsPassword marks reset-password, whose new secret is a
	// literal-or-FEEL value (typically a variable) the connector wraps in a
	// passwordProfile — the same shape the LDAP connector's modify-password takes, so
	// a modeler picks the operation rather than authoring the encoding (ADR-0172).
	NeedsUser       bool
	NeedsGroup      bool
	NeedsAttributes bool
	NeedsPassword   bool
	// IsList marks the operation that returns a collection instead of one object or
	// nothing. It is the one operation [Run] does not perform with a single call:
	// a collection is paged, and following those pages is this connector's work
	// rather than something a process has to model with a loop.
	IsList bool
	// Describes the operation for an error message.
	Label string
}

// Ops is the operation table. The set covers a joiner/mover/leaver lifecycle: create
// and read an account, find accounts, change one, enable and disable it, delete it,
// reset its password, move it in and out of groups, create and delete a group, and
// stand up a Team on a group and add a member to it.
var Ops = map[string]Op{
	"create-user":         {Method: "POST", NeedsAttributes: true, Label: "create a user"},
	"get-user":            {Method: "GET", NeedsUser: true, Label: "read a user"},
	"list-users":          {Method: "GET", IsList: true, Label: "list users"},
	"update-user":         {Method: "PATCH", NeedsUser: true, NeedsAttributes: true, Label: "update a user"},
	"delete-user":         {Method: "DELETE", NeedsUser: true, Label: "delete a user"},
	"reset-password":      {Method: "PATCH", NeedsUser: true, NeedsPassword: true, Label: "reset a password"},
	"enable":              {Method: "PATCH", NeedsUser: true, Label: "enable an account"},
	"disable":             {Method: "PATCH", NeedsUser: true, Label: "disable an account"},
	"add-group-member":    {Method: "POST", NeedsUser: true, NeedsGroup: true, Label: "add a group member"},
	"remove-group-member": {Method: "DELETE", NeedsUser: true, NeedsGroup: true, Label: "remove a group member"},
	"create-group":        {Method: "POST", NeedsAttributes: true, Label: "create a group"},
	"delete-group":        {Method: "DELETE", NeedsGroup: true, Label: "delete a group"},
	// A Team's id is its group's id: create-team teamifies an existing (Microsoft
	// 365) group, and add-team-member addresses /teams/{groupId}. GroupID therefore
	// carries the team throughout, so no separate team-id field is authored.
	"create-team":     {Method: "PUT", NeedsGroup: true, Label: "create a team"},
	"add-team-member": {Method: "POST", NeedsUser: true, NeedsGroup: true, Label: "add a team member"},
}

// OpNames lists the operations, sorted, for the error messages that have to say what
// was expected.
func OpNames() []string {
	out := make([]string, 0, len(Ops))
	for n := range Ops {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Job is an Entra task with everything already evaluated. There is nowhere here to
// put a tenant id, a client id or a client secret, which is what makes "the engine
// holds no Entra credential" a property of the type (ADR-0172).
type Job struct {
	// Connector names the tenant the *worker* is configured for.
	Connector string `json:"connector"`
	Operation string `json:"operation"`
	// UserID is a user principal name or object id; GroupID an object id.
	UserID  string `json:"userId,omitempty"`
	GroupID string `json:"groupId,omitempty"`
	// Attributes is the resolved JSON body for create-user, update-user and
	// create-group.
	Attributes map[string]any `json:"attributes,omitempty"`
	// NewPassword is the resolved secret for reset-password: the value the connector
	// wraps in a passwordProfile. It is zero on every other operation.
	NewPassword string `json:"newPassword,omitempty"`
	// Filter, Select, PageSize and MaxUsers configure list-users and are zero on
	// every other operation. Filter is the resolved OData $filter and Select the
	// $select projection; PageSize is the $top asked of each request (0 leaves Graph
	// its own page size) and MaxUsers caps what may reach the result variable
	// (0 unbounded). The compiler has already applied the defaults.
	Filter   string `json:"filter,omitempty"`
	Select   string `json:"select,omitempty"`
	PageSize int32  `json:"pageSize,omitempty"`
	MaxUsers int32  `json:"maxUsers,omitempty"`
	// Search is Graph's $search term, authored exactly as Graph takes it — quotes
	// included, so a compound term stays expressible. Advanced asks for advanced
	// query support (ConsistencyLevel: eventual plus $count=true), which a search
	// requires and which endsWith, ne and not need too. A search implies it; the
	// compiler has already set both.
	Search string `json:"search,omitempty"`
	// The tag is advancedQuery, not advanced: every other field here is named after
	// the attribute a model authors, and the resolved detail the engine hands the
	// worker is keyed the same way. A tag that disagreed with that key decoded to
	// false in silence — the listing then ran as a plain query and Graph refused the
	// filter that needed the header, which is how this was found.
	Advanced       bool   `json:"advancedQuery,omitempty"`
	ResultVariable string `json:"resultVariable,omitempty"`
}

// Resolve turns a compiled Entra connector task into a [Job]: the authored ids
// evaluated against the instance's variables, and the attributes object read up the
// task's scope chain. It is engine work by necessity — FEEL is compiled at deploy and
// only the engine has the scope chain.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("entra: connector task has no detail")
	}
	op := cp.Intern(detail.EntraOp)
	spec, ok := Ops[op]
	if !ok {
		return Job{}, fmt.Errorf("entra: unknown operation %q (want %s)", op, strings.Join(OpNames(), ", "))
	}
	vars, err := scopeChainVars(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("entra: read variables: %w", err)
	}
	j := Job{
		Connector:      cp.Intern(detail.Connector),
		Operation:      op,
		UserID:         resolveValue(detail.EntraUserID, elementInstanceKey, vars),
		GroupID:        resolveValue(detail.EntraGroupID, elementInstanceKey, vars),
		NewPassword:    resolveValue(detail.EntraNewPassword, elementInstanceKey, vars),
		Filter:         resolveValue(detail.EntraFilter, elementInstanceKey, vars),
		Select:         cp.Intern(detail.EntraSelect),
		PageSize:       detail.EntraPageSize,
		MaxUsers:       detail.EntraMaxUsers,
		Search:         resolveValue(detail.EntraSearch, elementInstanceKey, vars),
		Advanced:       detail.EntraAdvanced,
		ResultVariable: cp.Intern(detail.ResultVar),
	}
	if spec.NeedsAttributes {
		name := cp.Intern(detail.EntraAttributesVar)
		v, ok := vars[name]
		if !ok {
			return Job{}, fmt.Errorf("entra: attributes variable %q is not set at this task", name)
		}
		attrs, err := attributesOf(v, name)
		if err != nil {
			return Job{}, err
		}
		j.Attributes = attrs
	}
	return j, nil
}

// attributesOf reads the attributes variable as a JSON object. A list or a scalar is
// refused: Graph takes an object of directory properties, and sending anything else
// would be a 400 an operator has to decode from the far side.
func attributesOf(v model.VariableValue, name string) (map[string]any, error) {
	if v.Kind != model.VarJSON {
		return nil, fmt.Errorf("entra: attributes variable %q must be an object of directory properties, not a %v", name, v.Kind)
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(v.Text)))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("entra: attributes variable %q is not a JSON object: %w", name, err)
	}
	if out == nil {
		return nil, fmt.Errorf("entra: attributes variable %q is null", name)
	}
	return out, nil
}

// Run performs a resolved job through the caller's own registry and returns the
// variables the job completes with. It is the whole of the worker's half.
//
// The connector lookup comes first, as it does for mail and SQL: an unconfigured name
// is the actionable failure, and reporting it ahead of anything about the request
// keeps the message pointed at the fix.
func Run(ctx context.Context, j Job, reg *Registry) (map[string]any, error) {
	client, ok := reg.Client(j.Connector)
	if !ok {
		return nil, reg.Unresolved("entra", j.Connector)
	}
	spec, ok := Ops[j.Operation]
	if !ok {
		return nil, fmt.Errorf("entra: unknown operation %q (want %s)", j.Operation, strings.Join(OpNames(), ", "))
	}
	if err := checkRequired(j, spec); err != nil {
		return nil, err
	}
	if spec.IsList {
		users, err := listUsers(ctx, j, client)
		if err != nil {
			return nil, err
		}
		return map[string]any{j.ResultVariable: users}, nil
	}
	res, err := client.Call(ctx, request(j, spec, client.BaseURL()))
	if err != nil {
		return nil, err
	}
	if j.ResultVariable == "" {
		return nil, nil
	}
	return map[string]any{j.ResultVariable: res}, nil
}

// nextLinkKey is the member Graph puts the continuation URL in. It is an OData
// annotation rather than a property, which is why it is addressed by its literal
// name and not by a struct tag.
const nextLinkKey = "@odata.nextLink"

// maxListPages bounds a listing that has no user cap of its own.
//
// Without it, a server that offers another page forever — broken, misconfigured, or
// simply a directory nobody expected to be this large — would hold a worker until
// the job's lease expired, which surfaces as a task that mysteriously retries rather
// than as a listing that was too big. At Graph's own ceiling of 999 users per page
// this is far above any listing that belongs in a process variable, so reaching it
// means something is wrong and saying so is the useful outcome.
const maxListPages = 1000

// listUsers performs the whole listing: the first request this connector builds, and
// then every continuation Graph hands back, until there is no next page.
//
// Following the pages here rather than exposing them is the point of the operation.
// A model that had to loop over @odata.nextLink itself would be carrying Graph's
// paging protocol in its diagram — the same thing ADR-0172 refused to make a modeler
// hand-author for a $ref URL.
func listUsers(ctx context.Context, j Job, client Client) ([]any, error) {
	req := request(j, Ops[j.Operation], client.BaseURL())
	users := []any{}
	for pages := 0; pages < maxListPages; pages++ {
		res, err := client.Call(ctx, req)
		if err != nil {
			return nil, err
		}
		batch, next, err := usersPage(res)
		if err != nil {
			return nil, err
		}
		users = append(users, batch...)
		// The cap fails the job rather than truncating, for the reason the LDAP
		// connector's does: a short result set is a wrong answer, not a partial one,
		// and a process deciding something from it decides it confidently.
		if j.MaxUsers > 0 && len(users) > int(j.MaxUsers) {
			return nil, fmt.Errorf("entra: the listing returned more than the %d-user maxUsers cap; narrow the filter or raise maxUsers (truncating would be a wrong answer, not a partial one)", j.MaxUsers)
		}
		if next == "" {
			return users, nil
		}
		// The continuation replaces the path but keeps everything else — above all
		// Eventual, because Graph rejects a page of an advanced query fetched
		// without the header that made the query legal in the first place.
		req.Path = next
	}
	return nil, fmt.Errorf("entra: the listing still offered another page after %d requests; narrow the filter, or set maxUsers so an oversized listing fails by its own bound", maxListPages)
}

// usersPage reads one Graph collection response: the users it carries and the link to
// the next page, empty on the last one.
//
// A response that is not a collection is an error rather than an empty page, because
// an empty page is indistinguishable from "no such users" — and a process that reads
// a listing as empty acts on it.
func usersPage(res any) ([]any, string, error) {
	obj, ok := res.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("entra: list-users expected a user collection, got %T", res)
	}
	raw, ok := obj["value"]
	if !ok {
		return nil, "", fmt.Errorf("entra: list-users response carries no %q collection", "value")
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, "", fmt.Errorf("entra: list-users response has a %q that is a %T, not a list of users", "value", raw)
	}
	next := ""
	if nl, present := obj[nextLinkKey]; present {
		s, ok := nl.(string)
		if !ok {
			return nil, "", fmt.Errorf("entra: list-users response has an %s that is a %T, not a URL", nextLinkKey, nl)
		}
		next = s
	}
	return list, next, nil
}

// listPath builds the first request of a listing. The parameter names are written
// literally rather than through url.Values, which would percent-encode the leading
// $ into %24: legal, decoded identically by Graph, and unreadable in a log or a
// replay next to the documentation an operator is holding.
func listPath(j Job) string {
	var q []string
	if f := strings.TrimSpace(j.Filter); f != "" {
		q = append(q, "$filter="+url.QueryEscape(f))
	}
	// The term is encoded but not quoted: Graph's $search carries its own quoting,
	// and a compound term ("a" AND "b") has quotes inside it. Inventing them here
	// would make the compound case unwritable.
	if se := strings.TrimSpace(j.Search); se != "" {
		q = append(q, "$search="+url.QueryEscape(se))
	}
	if sel := strings.TrimSpace(j.Select); sel != "" {
		q = append(q, "$select="+url.QueryEscape(sel))
	}
	if j.PageSize > 0 {
		q = append(q, "$top="+strconv.FormatInt(int64(j.PageSize), 10))
	}
	// $count=true is not a request for a number here — it is the other half of
	// Graph's advanced query support, which refuses ConsistencyLevel: eventual
	// without it. The two are one switch, so the connector never sends half of it.
	if j.advanced() {
		q = append(q, "$count=true")
	}
	if len(q) == 0 {
		return "/users"
	}
	return "/users?" + strings.Join(q, "&")
}

// checkRequired repeats the compiler's shape rules on the worker. The compiler
// catches an under-specified model at deploy; this catches a job built by hand or by
// an older engine, and turns what would be a confusing Graph 404 into a sentence
// naming the missing field.
func checkRequired(j Job, spec Op) error {
	if spec.NeedsUser && strings.TrimSpace(j.UserID) == "" {
		return fmt.Errorf("entra: operation %q resolved no userId", j.Operation)
	}
	if spec.NeedsGroup && strings.TrimSpace(j.GroupID) == "" {
		return fmt.Errorf("entra: operation %q resolved no groupId", j.Operation)
	}
	if spec.NeedsAttributes && len(j.Attributes) == 0 {
		return fmt.Errorf("entra: operation %q resolved no attributes", j.Operation)
	}
	if spec.NeedsPassword && strings.TrimSpace(j.NewPassword) == "" {
		return fmt.Errorf("entra: operation %q resolved no newPassword", j.Operation)
	}
	if spec.IsList && strings.TrimSpace(j.ResultVariable) == "" {
		return fmt.Errorf("entra: operation %q resolved no resultVariable; a listing that discards its result is a directory read nothing asked for", j.Operation)
	}
	return nil
}

// advanced reports whether the listing runs as an advanced query.
//
// A search implies it even when the flag is unset. The compiler already sets both, so
// this is the worker repeating the rule for the same reason [checkRequired] repeats
// the shape rules: a job built by hand, or by an engine older than the flag, would
// otherwise send Graph a $search it refuses — a 400 an operator has to decode from
// the far side instead of a request that simply works.
func (j Job) advanced() bool {
	return j.Advanced || strings.TrimSpace(j.Search) != ""
}

// request builds the Graph request for one operation. baseURL is needed only by
// add-group-member, whose body carries an absolute @odata.id.
//
// Only a listing can ask for eventual consistency: every other operation addresses
// one object by id, where an advanced query has nothing to mean.
func request(j Job, spec Op, baseURL string) Request {
	user := url.PathEscape(strings.TrimSpace(j.UserID))
	group := url.PathEscape(strings.TrimSpace(j.GroupID))
	base := strings.TrimRight(baseURL, "/")
	r := Request{Method: spec.Method, Eventual: spec.IsList && j.advanced()}
	switch j.Operation {
	case "create-user":
		r.Path, r.Body = "/users", j.Attributes
	case "get-user", "delete-user":
		r.Path = "/users/" + user
	case "list-users":
		r.Path = listPath(j)
	case "update-user":
		r.Path, r.Body = "/users/"+user, j.Attributes
	case "reset-password":
		// forceChangePasswordNextSignIn is the convention a reset carries: the account
		// gets a temporary secret its owner must replace. Encoding it here is the point
		// of a named operation over a hand-authored passwordProfile.
		r.Path = "/users/" + user
		r.Body = map[string]any{"passwordProfile": map[string]any{
			"password":                      j.NewPassword,
			"forceChangePasswordNextSignIn": true,
		}}
	case "enable":
		r.Path, r.Body = "/users/"+user, map[string]any{"accountEnabled": true}
	case "disable":
		r.Path, r.Body = "/users/"+user, map[string]any{"accountEnabled": false}
	case "add-group-member":
		r.Path = "/groups/" + group + "/members/$ref"
		r.Body = map[string]any{"@odata.id": base + "/directoryObjects/" + user}
	case "remove-group-member":
		r.Path = "/groups/" + group + "/members/" + user + "/$ref"
	case "create-group":
		r.Path, r.Body = "/groups", j.Attributes
	case "delete-group":
		r.Path = "/groups/" + group
	case "create-team":
		// A Team is stood up on the group by PUT .../team; the id it gets is the
		// group's own. The default settings are sent explicitly so a team never depends
		// on whatever Graph would infer from an empty body.
		r.Path = "/groups/" + group + "/team"
		r.Body = defaultTeam()
	default: // add-team-member — the only one left, guarded by the Ops lookup
		r.Path = "/teams/" + group + "/members"
		r.Body = map[string]any{
			"@odata.type":     "#microsoft.graph.aadUserConversationMember",
			"roles":           []any{},
			"user@odata.bind": base + "/users('" + user + "')",
		}
	}
	return r
}

// defaultTeam is the body create-team sends to PUT /groups/{id}/team: a standard team
// with sane collaboration defaults. It is spelled out rather than left to Graph so the
// team a process creates is the same team every time, independent of tenant defaults.
func defaultTeam() map[string]any {
	return map[string]any{
		"memberSettings":    map[string]any{"allowCreateUpdateChannels": true},
		"messagingSettings": map[string]any{"allowUserEditMessages": true, "allowUserDeleteMessages": true},
		"funSettings":       map[string]any{"allowGiphy": true, "giphyContentRating": "moderate"},
	}
}

// builtinProcessInstanceKey is the reserved FEEL name that binds to the instance's
// own key, as it does for every other connector.
const builtinProcessInstanceKey = "processInstanceKey"

// resolveValue evaluates a literal-or-FEEL connector value against the scope's
// variables and coerces it to its string form, the same way every other connector's
// authored values are read. A FEEL null — an absent variable or a failed evaluation —
// becomes the empty string, which the required-field checks then report by name.
func resolveValue(rv compiler.RestExpr, scope uint64, scopeVars map[string]model.VariableValue) string {
	if rv.Expr == nil {
		return rv.Literal
	}
	v, err := rv.Expr.Eval(bindVars(scope, scopeVars, rv.Expr.Inputs()))
	if err != nil {
		return ""
	}
	_, _, text := expr.Classify(v)
	return text
}

// bindVars turns the named variables from a scope into a FEEL binding. A name absent
// from the scope is left unbound (FEEL null); the reserved name processInstanceKey
// binds to the scope's own key as a string.
func bindVars(scope uint64, scopeVars map[string]model.VariableValue, names []string) map[string]expr.Value {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]expr.Value, len(names))
	for _, n := range names {
		if n == builtinProcessInstanceKey {
			m[n] = expr.String(strconv.FormatUint(scope, 10))
			continue
		}
		if v, ok := scopeVars[n]; ok {
			m[n] = expr.FromStored(toExprKind(v.Kind), v.Bool, v.Text)
		}
	}
	return m
}

// toExprKind maps a stored variable kind to the expr kind for binding it into an
// evaluation.
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

// scopeChainVars reads the variables an element sees up its scope chain (nearest
// wins), through the shared walk every job worker uses (ADR-0068).
func scopeChainVars(store VarStore, elementInstanceKey uint64) (map[string]model.VariableValue, error) {
	return state.VisibleVariablesMap(store, elementInstanceKey)
}
