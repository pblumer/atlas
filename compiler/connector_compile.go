package compiler

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// connectorCompiler compiles one connector flavor of a job-worker task (service or
// send task). A service task bearing a connector extension delegates to a
// server-registered connector via the job path rather than to an external
// service-task worker; each flavor validates its own extension and emits its own
// connector-task node.
//
// The ordered connectorCompilers table is the single place the set of connector
// flavors lives, so adding one is a new entry here plus its compile function — not
// another arm grafted into registerJobWorkerTask. Order is preserved (the first
// present extension wins, as when the arms were inlined), so it stays stable.
type connectorCompiler struct {
	// present reports whether st carries this connector's extension.
	present func(st xmlServiceTask) bool
	// retries reads this connector extension's own retries attribute (ADR-0135),
	// which overrides a <zeebe:taskDefinition retries> on the same task. It is called
	// only when present(st) is true, so it may dereference the extension pointer
	// directly; an empty string means the attribute was omitted.
	retries func(st xmlServiceTask) string
	// compile validates the extension's fields and adds the connector-task node,
	// returning its node id. It is called only when present(st) is true, so it may
	// dereference the extension pointer directly.
	compile func(b *Builder, st xmlServiceTask, retries int32) (int32, error)
}

// connectorCompilers is the ordered registry of connector flavors a job-worker task
// can take. registerJobWorkerTask consults it before falling through to the plain
// external-worker task.
var connectorCompilers = []connectorCompiler{
	{
		present: func(st xmlServiceTask) bool { return st.Clio != nil },
		retries: func(st xmlServiceTask) string { return st.Clio.Retries },
		compile: compileClioConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.Rest != nil },
		retries: func(st xmlServiceTask) string { return st.Rest.Retries },
		compile: compileRestConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.Mail != nil },
		retries: func(st xmlServiceTask) string { return st.Mail.Retries },
		compile: compileMailConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.User != nil },
		retries: func(st xmlServiceTask) string { return st.User.Retries },
		compile: compileUserConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.sharePointConn() != nil },
		retries: func(st xmlServiceTask) string { return st.sharePointConn().Retries },
		compile: compileSharePointConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.Remedy != nil },
		retries: func(st xmlServiceTask) string { return st.Remedy.Retries },
		compile: compileRemedyConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.WebScrape != nil },
		retries: func(st xmlServiceTask) string { return st.WebScrape.Retries },
		compile: compileWebScrapeConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.Scim != nil },
		retries: func(st xmlServiceTask) string { return st.Scim.Retries },
		compile: compileScimConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.Ldap != nil },
		retries: func(st xmlServiceTask) string { return st.Ldap.Retries },
		compile: compileLdapConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.Soap != nil },
		retries: func(st xmlServiceTask) string { return st.Soap.Retries },
		compile: compileSoapConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.Ad != nil },
		retries: func(st xmlServiceTask) string { return st.Ad.Retries },
		compile: compileAdConnectorTask,
	},
	// The three SQL products, in their registry positions. Each is built from the
	// product table rather than written out, so which extension element a product is
	// read from is stated once — the way to get this wrong by hand is an entry whose
	// `present` tests one product's element and whose `compile` names another's, which
	// compiles a MariaDB task as if it were a SQL Server one.
	sqlConnectorCompiler(MsSqlJobType),
	sqlConnectorCompiler(MariaDBJobType),
	sqlConnectorCompiler(PostgresJobType),
	{
		present: func(st xmlServiceTask) bool { return st.Entra != nil },
		retries: func(st xmlServiceTask) string { return st.Entra.Retries },
		compile: compileEntraConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.Ldif != nil },
		retries: func(st xmlServiceTask) string { return st.Ldif.Retries },
		compile: compileLdifConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.Jira != nil },
		retries: func(st xmlServiceTask) string { return st.Jira.Retries },
		compile: compileJiraConnectorTask,
	},
	{
		present: func(st xmlServiceTask) bool { return st.GoogleSheets != nil },
		retries: func(st xmlServiceTask) string { return st.GoogleSheets.Retries },
		compile: compileGoogleSheetsConnectorTask,
	},
}

// The directory-file formats and directions a model can author. They are spelled here
// as well as in connector/ldif because the compiler cannot import the connector (the
// dependency runs the other way); TestLdifFormatsMatchTheConnector guards the seam.
const (
	ldifFormatLDIF     = "ldif"
	ldifFormatDSML     = "dsml"
	ldifOperationRead  = "read"
	ldifOperationWrite = "write"
)

// compileLdifConnectorTask compiles an <atlas:ldifConnector> task: directory entries
// read from, or written to, a file held in a process variable (ADR-0171).
func compileLdifConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Ldif
	format := strings.ToLower(strings.TrimSpace(cn.Format))
	// There is deliberately no default: a directory file is LDIF or it is DSML, and
	// guessing from the bytes is how a malformed file becomes a plausible-looking
	// empty result.
	if format == "" {
		return 0, fmt.Errorf("compiler: ldif connector task %q needs a format (%s or %s)", st.Id, ldifFormatDSML, ldifFormatLDIF)
	}
	if format != ldifFormatLDIF && format != ldifFormatDSML {
		return 0, fmt.Errorf("compiler: ldif connector task %q has an unknown format %q (want %s or %s)", st.Id, cn.Format, ldifFormatDSML, ldifFormatLDIF)
	}
	op := strings.ToLower(strings.TrimSpace(cn.Operation))
	if op == "" {
		op = ldifOperationRead
	}
	if op != ldifOperationRead && op != ldifOperationWrite {
		return 0, fmt.Errorf("compiler: ldif connector task %q has an unknown operation %q (want %s or %s)", st.Id, cn.Operation, ldifOperationRead, ldifOperationWrite)
	}
	if strings.TrimSpace(cn.Source) == "" {
		return 0, fmt.Errorf("compiler: ldif connector task %q needs a source variable", st.Id)
	}
	if strings.TrimSpace(cn.ResultVariable) == "" {
		return 0, fmt.Errorf("compiler: ldif connector task %q needs a resultVariable to receive the %s", st.Id,
			map[string]string{ldifOperationRead: "entries", ldifOperationWrite: "rendered file"}[op])
	}
	return b.AddLdifConnectorTask(LdifConfig{
		Format:    format,
		Operation: op,
		Source:    strings.TrimSpace(cn.Source),
		Result:    strings.TrimSpace(cn.ResultVariable),
		Retries:   retries,
	}), nil
}

// entraOp describes what one Entra lifecycle operation requires of a model. The
// table is the compiler's half of connector/entra's Ops table; the drift test
// TestEntraOpsMatchTheConnector keeps the two from disagreeing about the operation
// set, which is the failure this shape exists to prevent.
type entraOp struct {
	needsUser       bool
	needsGroup      bool
	needsAttributes bool
	// needsPassword marks reset-password, the one operation that authors a newPassword
	// (literal-or-FEEL). It is an error anywhere else, like the listing attributes.
	needsPassword bool
	// isList marks an operation that returns a collection rather than an object
	// or nothing. It is what makes filter/select/pageSize/maxUsers meaningful — and
	// what makes filter/search/advancedQuery an error anywhere else.
	isList bool
	// isDelta marks a change-tracking (delta-query) operation. Like a listing it returns
	// a collection and takes select/pageSize/maxUsers, but it also takes a deltaLink to
	// resume from and refuses filter/search/advancedQuery, which Graph's delta endpoint
	// does not run.
	isDelta bool
}

var entraOps = map[string]entraOp{
	"create-user":         {needsAttributes: true},
	"get-user":            {needsUser: true},
	"list-users":          {isList: true},
	"delta-users":         {isDelta: true},
	"update-user":         {needsUser: true, needsAttributes: true},
	"delete-user":         {needsUser: true},
	"reset-password":      {needsUser: true, needsPassword: true},
	"enable":              {needsUser: true},
	"disable":             {needsUser: true},
	"add-group-member":    {needsUser: true, needsGroup: true},
	"remove-group-member": {needsUser: true, needsGroup: true},
	"create-group":        {needsAttributes: true},
	"get-group":           {needsGroup: true},
	"list-groups":         {isList: true},
	"delta-groups":        {isDelta: true},
	"update-group":        {needsGroup: true, needsAttributes: true},
	"delete-group":        {needsGroup: true},
	"add-group-owner":     {needsUser: true, needsGroup: true},
	"remove-group-owner":  {needsUser: true, needsGroup: true},
	"create-team":         {needsGroup: true},
	"add-team-member":     {needsUser: true, needsGroup: true},
	"add-team-owner":      {needsUser: true, needsGroup: true},
	"create-channel":      {needsGroup: true, needsAttributes: true},
	"archive-team":        {needsGroup: true},
	"assign-license":      {needsUser: true, needsAttributes: true},
	"assign-role":         {needsUser: true, needsAttributes: true},
}

// The listing bounds a model inherits when it authors none.
//
// maxUsers defaults on for the reason the LDAP connector's entry cap does: an
// unbounded directory listing into a process variable is the failure this hardens
// against, and a truncated one would be a wrong answer rather than a partial one.
// pageSize defaults *off* — absent means no $top, which leaves Graph its own page
// size (100 for /users). Unlike LDAP there is nothing to work around: Graph pages a
// collection whether asked to or not, and this connector follows every page.
const (
	defaultEntraPageSize = 0
	defaultEntraMaxUsers = 1000
)

// maxEntraPageSize is Graph's own ceiling on $top for /users. Refusing a larger one
// at deploy is better than the 400 it becomes at run time, which an operator has to
// read out of a failed job to learn a number that was knowable all along.
const maxEntraPageSize = 999

// entraOpNames lists the operations, sorted, for the error messages.
func entraOpNames() []string {
	out := make([]string, 0, len(entraOps))
	for n := range entraOps {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// compileEntraConnectorTask compiles an <atlas:entraConnector> task: one Microsoft
// Entra ID lifecycle operation through Graph (ADR-0172). The model names a tenant
// connector and the operation; the tenant id, client id and client secret live on the
// worker, so there is nothing here to validate about a credential.
func compileEntraConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Entra
	if strings.TrimSpace(cn.Connector) == "" {
		return 0, fmt.Errorf("compiler: entra connector task %q needs a connector (the name the worker holds the tenant credential under)", st.Id)
	}
	// The connector is normally a static name, but may be a FEEL expression (a leading
	// '=') so one process can serve more than one tenant, resolving the name from its
	// own variables at call time. This is entra-only: the kind is worker-only, so no
	// deploy-time credential lookup keys off a fixed name (ADR-0172).
	connectorExpr, err := connectorValue(st.Id, "entra connector", "connector", cn.Connector)
	if err != nil {
		return 0, err
	}
	op := strings.ToLower(strings.TrimSpace(cn.Operation))
	if op == "" {
		return 0, fmt.Errorf("compiler: entra connector task %q needs an operation (%s)", st.Id, strings.Join(entraOpNames(), ", "))
	}
	spec, ok := entraOps[op]
	if !ok {
		return 0, fmt.Errorf("compiler: entra connector task %q has an unknown operation %q (want %s)", st.Id, cn.Operation, strings.Join(entraOpNames(), ", "))
	}
	if spec.needsUser && strings.TrimSpace(cn.UserID) == "" {
		return 0, fmt.Errorf("compiler: entra connector task %q operation %q needs a userId (a user principal name or object id)", st.Id, op)
	}
	if spec.needsGroup && strings.TrimSpace(cn.GroupID) == "" {
		return 0, fmt.Errorf("compiler: entra connector task %q operation %q needs a groupId", st.Id, op)
	}
	hasInlineAttrs := strings.TrimSpace(cn.Attributes) != ""
	hasAttrsVar := strings.TrimSpace(cn.AttributesVariable) != ""
	if spec.needsAttributes && !hasInlineAttrs && !hasAttrsVar {
		return 0, fmt.Errorf("compiler: entra connector task %q operation %q needs its directory properties — an inline attributes JSON or an attributesVariable naming them", st.Id, op)
	}
	if hasInlineAttrs && hasAttrsVar {
		return 0, fmt.Errorf("compiler: entra connector task %q sets both inline attributes and an attributesVariable; use one, not both", st.Id)
	}
	if !spec.needsAttributes && (hasInlineAttrs || hasAttrsVar) {
		return 0, fmt.Errorf("compiler: entra connector task %q sets attributes on operation %q, which sends no body (attributes apply to create/update and create-channel)", st.Id, op)
	}
	attrs, err := entraAttributesExpr(st.Id, cn.Attributes)
	if err != nil {
		return 0, err
	}
	if spec.needsPassword && strings.TrimSpace(cn.NewPassword) == "" {
		return 0, fmt.Errorf("compiler: entra connector task %q operation %q needs a newPassword (the value to set, typically a FEEL variable)", st.Id, op)
	}
	if !spec.needsPassword && strings.TrimSpace(cn.NewPassword) != "" {
		return 0, fmt.Errorf("compiler: entra connector task %q sets newPassword on operation %q, which sets no password (newPassword applies to reset-password)", st.Id, op)
	}
	if (spec.isList || spec.isDelta) && strings.TrimSpace(cn.ResultVariable) == "" {
		return 0, fmt.Errorf("compiler: entra connector task %q operation %q needs a resultVariable (a directory read that discards its result is one nothing asked for)", st.Id, op)
	}
	if err := entraFieldGating(st.Id, op, spec, cn); err != nil {
		return 0, err
	}
	// select, pageSize and maxUsers apply to a listing or a delta query — both return a
	// collection this connector pages.
	listOrDelta := spec.isList || spec.isDelta
	pageSize, err := entraListBound(st.Id, op, listOrDelta, "pageSize", cn.PageSize, defaultEntraPageSize, maxEntraPageSize)
	if err != nil {
		return 0, err
	}
	maxUsers, err := entraListBound(st.Id, op, listOrDelta, "maxUsers", cn.MaxUsers, defaultEntraMaxUsers, 0)
	if err != nil {
		return 0, err
	}
	userID, err := connectorValue(st.Id, "entra connector", "userId", cn.UserID)
	if err != nil {
		return 0, err
	}
	groupID, err := connectorValue(st.Id, "entra connector", "groupId", cn.GroupID)
	if err != nil {
		return 0, err
	}
	newPassword, err := connectorValue(st.Id, "entra connector", "newPassword", cn.NewPassword)
	if err != nil {
		return 0, err
	}
	filter, err := connectorValue(st.Id, "entra connector", "filter", cn.Filter)
	if err != nil {
		return 0, err
	}
	search, err := connectorValue(st.Id, "entra connector", "search", cn.Search)
	if err != nil {
		return 0, err
	}
	deltaLink, err := connectorValue(st.Id, "entra connector", "deltaLink", cn.DeltaLink)
	if err != nil {
		return 0, err
	}
	advanced, err := entraAdvancedQuery(st.Id, cn)
	if err != nil {
		return 0, err
	}
	return b.AddEntraConnectorTask(EntraConfig{
		Connector:     strings.TrimSpace(cn.Connector),
		ConnectorExpr: connectorExpr,
		Op:            op,
		UserID:        userID,
		GroupID:       groupID,
		NewPassword:   newPassword,
		Attributes:    attrs,
		AttributesVar: strings.TrimSpace(cn.AttributesVariable),
		ResultVar:     strings.TrimSpace(cn.ResultVariable),
		Filter:        filter,
		Select:        strings.TrimSpace(cn.Select),
		PageSize:      pageSize,
		MaxUsers:      maxUsers,
		Search:        search,
		Advanced:      advanced,
		DeltaLink:     deltaLink,
		Retries:       retries,
	}), nil
}

// entraAdvancedQuery settles whether the listing asks for Graph's advanced query
// support: the ConsistencyLevel: eventual header plus $count=true, which is what makes
// endsWith, ne, not and $search usable on a directory collection.
//
// A search sets it on its own. Graph offers no other way to run one, so making an
// author tick a second box would be a trap whose only outcome is a 400 — and refusing
// advancedQuery="false" next to a search is better than honouring a combination that
// cannot work.
//
// Otherwise it is opt-in, and deliberately not inferred from the filter text. The
// filter may be a FEEL expression with no text to read at deploy, and eventual
// consistency is a change to what the process is told about the directory — a
// decision that belongs to the author rather than to a substring match.
func entraAdvancedQuery(taskID string, cn *xmlEntraConnector) (bool, error) {
	raw := strings.ToLower(strings.TrimSpace(cn.AdvancedQuery))
	hasSearch := strings.TrimSpace(cn.Search) != ""
	switch raw {
	case "":
		return hasSearch, nil
	case "true":
		return true, nil
	case "false":
		if hasSearch {
			return false, fmt.Errorf("compiler: entra connector task %q sets a search with advancedQuery=\"false\", but Graph runs a $search only as an advanced query", taskID)
		}
		return false, nil
	default:
		return false, fmt.Errorf("compiler: entra connector task %q has a non-boolean advancedQuery %q (want true or false)", taskID, cn.AdvancedQuery)
	}
}

// entraFieldGating rejects a query field on an operation that has no meaning for it.
// Ignoring one would be worse than failing: an author who wrote a filter believes the
// task is filtered, and a get-user that quietly ignores it addresses whatever userId
// happens to say instead. There are three classes:
//
//   - filter, search, advancedQuery — a listing only (list-users, list-groups). Graph's
//     delta endpoint runs none of them, so they are an error on a delta op too.
//   - select — a listing or a delta query (both return a collection to project).
//   - deltaLink — a delta query alone; a resume cursor has nothing to resume elsewhere.
//
// pageSize and maxUsers are numbers, gated by [entraListBound]; this covers the
// string-valued fields.
func entraFieldGating(taskID, op string, spec entraOp, cn *xmlEntraConnector) error {
	for _, a := range []struct{ what, raw string }{
		{"filter", cn.Filter},
		{"search", cn.Search},
		{"advancedQuery", cn.AdvancedQuery},
	} {
		if strings.TrimSpace(a.raw) != "" && !spec.isList {
			return fmt.Errorf("compiler: entra connector task %q sets %s on operation %q, which is not a listing (%s applies to list-users and list-groups)", taskID, a.what, op, a.what)
		}
	}
	if strings.TrimSpace(cn.Select) != "" && !spec.isList && !spec.isDelta {
		return fmt.Errorf("compiler: entra connector task %q sets select on operation %q, which returns no collection (select applies to list-users, list-groups and the delta operations)", taskID, op)
	}
	if strings.TrimSpace(cn.DeltaLink) != "" && !spec.isDelta {
		return fmt.Errorf("compiler: entra connector task %q sets deltaLink on operation %q, which is not a change-tracking query (deltaLink applies to delta-users and delta-groups)", taskID, op)
	}
	return nil
}

// entraListBound reads one authored listing bound, returning the effective value the
// compiled process carries: the default when the attribute is absent, and the
// authored number otherwise — including 0, which is how a model says unbounded.
// A max of 0 means the bound has no ceiling of its own.
//
// A bound on an operation that returns no collection is rejected rather than ignored,
// for [entraFieldGating]'s reason. isList here means "returns a collection" — a listing
// or a delta query.
func entraListBound(taskID, op string, isList bool, what, raw string, def, max int32) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if isList {
			return def, nil
		}
		return 0, nil
	}
	if !isList {
		return 0, fmt.Errorf("compiler: entra connector task %q sets %s on operation %q, which returns no collection (%s applies to list-users, list-groups and the delta operations)", taskID, what, op, what)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("compiler: entra connector task %q has a non-numeric %s %q", taskID, what, raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("compiler: entra connector task %q has a negative %s %d", taskID, what, n)
	}
	if max > 0 && int32(n) > max {
		return 0, fmt.Errorf("compiler: entra connector task %q has a %s of %d, above the %d Graph accepts for /users", taskID, what, n, max)
	}
	return int32(n), nil
}

// sqlProduct is one of the three SQL connector products: how a task of it is named
// in errors, which extension it is read from, and which reserved job type it
// compiles to. Everything else about the three is identical, which is why they share
// one compile function rather than three that would drift.
type sqlProduct struct {
	kind    string // how the product is named in a compiler error
	jobType string // the reserved job type a task of it carries
	ext     func(xmlServiceTask) *xmlSqlConnector
}

// sqlProducts is the product table, keyed by reserved job type.
var sqlProducts = map[string]sqlProduct{
	MsSqlJobType:    {kind: "mssql connector", jobType: MsSqlJobType, ext: func(st xmlServiceTask) *xmlSqlConnector { return st.MsSql }},
	MariaDBJobType:  {kind: "mariadb connector", jobType: MariaDBJobType, ext: func(st xmlServiceTask) *xmlSqlConnector { return st.MariaDB }},
	PostgresJobType: {kind: "postgres connector", jobType: PostgresJobType, ext: func(st xmlServiceTask) *xmlSqlConnector { return st.Postgres }},
}

// sqlConnectorCompiler is the registry entry for one SQL product. All three read the
// same fields and compile through the same function; what a product contributes is the
// extension element it is read from, which sqlProducts already holds.
func sqlConnectorCompiler(jobType string) connectorCompiler {
	p := sqlProducts[jobType]
	return connectorCompiler{
		present: func(st xmlServiceTask) bool { return p.ext(st) != nil },
		retries: func(st xmlServiceTask) string { return p.ext(st).Retries },
		compile: func(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
			return compileSqlConnectorTask(b, st, retries, p)
		},
	}
}

// sqlOps is the set of operations a SQL connector task can author. query returns
// rows, query-one a single row, execute an affected-row count.
var sqlOps = map[string]bool{"query": true, "query-one": true, "execute": true}

// compileSqlConnectorTask compiles a SQL connector task of one product: it runs one
// statement against a database a *worker* is configured for (ADR-0173). The model
// names a connector and authors the statement; the DSN and its credential never
// enter the engine, so there is nothing here to validate about an address.
//
// The statement is literal by construction. A FEEL statement would let a process
// value become part of the SQL text — an injection that needs no quoting bug, because
// it would be the field doing what it says — so one is refused here rather than
// guarded against later. Data reaches the statement through bound parameters, whose
// placeholder syntax belongs to the product this task was authored for.
func compileSqlConnectorTask(b *Builder, st xmlServiceTask, retries int32, p sqlProduct) (int32, error) {
	cn := p.ext(st)
	if strings.TrimSpace(cn.Connector) == "" {
		return 0, fmt.Errorf("compiler: %s task %q needs a connector (the name the worker holds the DSN under)", p.kind, st.Id)
	}
	op := strings.ToLower(strings.TrimSpace(cn.Operation))
	if op == "" {
		return 0, fmt.Errorf("compiler: %s task %q needs an operation (query, query-one, or execute)", p.kind, st.Id)
	}
	if !sqlOps[op] {
		return 0, fmt.Errorf("compiler: %s task %q has an unknown operation %q (want query, query-one, or execute)", p.kind, st.Id, cn.Operation)
	}
	stmt := strings.TrimSpace(cn.Statement)
	if stmt == "" {
		return 0, fmt.Errorf("compiler: %s task %q needs a statement", p.kind, st.Id)
	}
	if strings.HasPrefix(stmt, "=") {
		return 0, fmt.Errorf("compiler: %s task %q has a FEEL statement; the statement must be literal SQL so no process value can become part of it — pass data through parametersVariable instead (ADR-0173)", p.kind, st.Id)
	}
	result := strings.TrimSpace(cn.ResultVariable)
	if result == "" && op != "execute" {
		return 0, fmt.Errorf("compiler: %s task %q operation %q needs a resultVariable to receive the result", p.kind, st.Id, op)
	}
	maxRows, err := sqlMaxRows(p.kind, st.Id, op, cn.MaxRows)
	if err != nil {
		return 0, err
	}
	return b.AddSqlConnectorTask(SqlConfig{
		JobType:   p.jobType,
		Connector: strings.TrimSpace(cn.Connector),
		Op:        op,
		Statement: stmt,
		ParamsVar: strings.TrimSpace(cn.ParametersVariable),
		MaxRows:   maxRows,
		ResultVar: result,
		Retries:   retries,
	}), nil
}

// sqlMaxRows reads the authored row cap. It applies to query alone: query-one is
// capped at one row by its own definition and execute returns a count, so a cap on
// either is an author believing something the connector will not do — reported rather
// than ignored.
func sqlMaxRows(kind, taskID, op, raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if op != "query" {
		return 0, fmt.Errorf("compiler: %s task %q sets maxRows on operation %q, which returns no row set (maxRows applies to query)", kind, taskID, op)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("compiler: %s task %q has a non-numeric maxRows %q", kind, taskID, raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("compiler: %s task %q has a negative maxRows %d", kind, taskID, n)
	}
	return int32(n), nil
}

// adOps is the set of Active Directory provisioning operations a connector task can
// author, and what each requires of a model.
//
// Most are AD-specific primitives the generic LDAP connector cannot express directly
// (unicodePwd, userAccountControl, incremental group membership). The lifecycle
// operations added later — update-attributes, move, delete, create-group — are not,
// and they are here anyway: an identity process that has to leave the AD connector
// and pick up the LDAP one to rename an account has been handed two ways to bind to
// the same directory, which is a worse answer than a little overlap (ADR-0166,
// amended).
type adOp struct {
	needsEntry    bool // an entryVariable naming the attribute object
	needsPassword bool
	needsMember   bool
	needsNewDN    bool
	isSync        bool  // reads a subtree delta rather than acting on one entry
	isSearch      bool  // reads what is under a base now, rather than acting on one entry
	maxEntries    int32 // the default entry cap, for the two operations that return entries
}

var adOps = map[string]adOp{
	"create-user":  {needsEntry: true},
	"create-group": {needsEntry: true},
	// A contact is a mail-enabled object with no account behind it — how a person in
	// another forest appears in this one's address book. It is a third create for the
	// same reason create-group is a second: the object classes differ, and AD rejects
	// an add without them.
	"create-contact":      {needsEntry: true},
	"update-attributes":   {needsEntry: true},
	"set-password":        {needsPassword: true},
	"enable":              {},
	"disable":             {},
	"move":                {needsNewDN: true},
	"delete":              {},
	"add-group-member":    {needsMember: true},
	"remove-group-member": {needsMember: true},
	// sync and search are the two AD operations that read rather than write, and the
	// two that address a subtree instead of an entry — so they are also the two that
	// take a baseDN rather than a dn.
	"sync": {isSync: true, maxEntries: defaultAdSyncMaxEntries},
	// search answers "is this entry there, and what is its DN?" — the question a
	// membership change has to settle before it can name a group (ADR-0166, amended a
	// fifth time).
	"search": {isSearch: true, maxEntries: defaultAdSearchMaxEntries},
}

// The default entry caps for the two reading operations, applied when the model
// authors none so the runtime interprets nothing (I5).
//
// They are the same number for different reasons, which is why they are two
// constants. A DirSync pass is resumable by construction — the cookie says where it
// got to — so capping it costs nothing but a second pass. A search has no such
// resume: its cap is the sqldb row cap's argument (ADR-0173), a bound on what an
// unqualified filter can pull into a process variable, and exceeding it fails the job
// rather than truncating, because a short result set is a wrong answer and not a
// partial one.
const (
	defaultAdSyncMaxEntries   = 1000
	defaultAdSearchMaxEntries = 1000
)

// adOpNames lists the operations, sorted, for the error messages that say what was
// expected.
func adOpNames() []string {
	out := make([]string, 0, len(adOps))
	for n := range adOps {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// compileAdConnectorTask compiles an <atlas:adConnector> task: it performs an Active
// Directory operation against a model-authored server via the job path (ADR-0166), not
// an external service-task worker. AD speaks LDAP, so the server URL and DNs live in
// the model and the bind password never does (a secret reference, ADR-0041). Most
// operations target a dn: create-user takes an attribute variable, set-password a new
// password, the group operations a member dn. The two reading operations — sync and
// search — take a baseDN and a result variable instead.
func compileAdConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Ad
	// A task addresses its directory one of two ways: by the name of a connector an
	// operator configured in the Console (the way every other credential-bearing kind
	// is addressed), or by carrying url/bindDN/bindSecret itself — the original form,
	// still accepted so models written before this keep compiling
	// (ADR-0206).
	//
	// Both at once is refused rather than resolved by precedence. Whichever rule we
	// picked, half the readers of the model would assume the other, and the two point
	// at different directories: a silent winner there writes to the wrong forest.
	named := strings.TrimSpace(cn.Connector)
	inline := strings.TrimSpace(cn.URL) != "" || strings.TrimSpace(cn.BindDN) != "" || strings.TrimSpace(cn.BindSecret) != ""
	switch {
	case named != "" && inline:
		return 0, fmt.Errorf("compiler: ad connector task %q names a connector %q *and* carries its own url/bindDN/bindSecret; keep one — the connector, unless the model must hold the directory itself", st.Id, named)
	case named == "" && strings.TrimSpace(cn.URL) == "":
		return 0, fmt.Errorf("compiler: ad connector task %q needs a connector (the name of a directory configured in the Console), or a url (ldaps://host for a password set)", st.Id)
	}
	op := strings.ToLower(strings.TrimSpace(cn.Operation))
	if op == "" {
		return 0, fmt.Errorf("compiler: ad connector task %q needs an operation (%s)", st.Id, strings.Join(adOpNames(), ", "))
	}
	spec, ok := adOps[op]
	if !ok {
		return 0, fmt.Errorf("compiler: ad connector task %q has an unknown operation %q (want %s)", st.Id, cn.Operation, strings.Join(adOpNames(), ", "))
	}
	// The two reading operations address a subtree by baseDN; every other operation
	// addresses an existing or to-be-created entry by dn — including move, where dn is
	// the entry being moved and newDN where it lands.
	switch {
	case spec.isSync:
		if strings.TrimSpace(cn.BaseDN) == "" {
			return 0, fmt.Errorf("compiler: ad connector task %q operation sync needs a baseDN (the naming context root the delta is read from)", st.Id)
		}
		if strings.TrimSpace(cn.CookieVariable) == "" {
			return 0, fmt.Errorf("compiler: ad connector task %q operation sync needs a cookieVariable; without one every pass re-reads the whole directory", st.Id)
		}
		if strings.TrimSpace(cn.ResultVariable) == "" {
			return 0, fmt.Errorf("compiler: ad connector task %q operation sync needs a resultVariable to receive the changes", st.Id)
		}
	case spec.isSearch:
		if strings.TrimSpace(cn.BaseDN) == "" {
			return 0, fmt.Errorf("compiler: ad connector task %q operation search needs a baseDN (where in the tree to look)", st.Id)
		}
		if strings.TrimSpace(cn.ResultVariable) == "" {
			return 0, fmt.Errorf("compiler: ad connector task %q operation search needs a resultVariable to receive what it found; a directory read that discards its result is one nothing asked for", st.Id)
		}
	default:
		if strings.TrimSpace(cn.DN) == "" {
			return 0, fmt.Errorf("compiler: ad connector task %q operation %q needs a dn", st.Id, op)
		}
	}
	scope, err := adSearchScope(st.Id, op, spec.isSearch, cn.Scope)
	if err != nil {
		return 0, err
	}
	maxEntries, err := adMaxEntries(st.Id, op, spec, cn.MaxEntries)
	if err != nil {
		return 0, err
	}
	if spec.needsEntry && strings.TrimSpace(cn.EntryVariable) == "" {
		return 0, fmt.Errorf("compiler: ad connector task %q operation %q needs an entryVariable naming the attribute object", st.Id, op)
	}
	if spec.needsPassword && strings.TrimSpace(cn.NewPassword) == "" {
		return 0, fmt.Errorf("compiler: ad connector task %q operation %q needs a newPassword", st.Id, op)
	}
	if spec.needsMember && strings.TrimSpace(cn.MemberDN) == "" {
		return 0, fmt.Errorf("compiler: ad connector task %q operation %q needs a memberDN", st.Id, op)
	}
	if spec.needsNewDN && strings.TrimSpace(cn.NewDN) == "" {
		return 0, fmt.Errorf("compiler: ad connector task %q operation %q needs a newDN (the entry's new distinguished name)", st.Id, op)
	}
	url, err := connectorValue(st.Id, "ad connector", "url", cn.URL)
	if err != nil {
		return 0, err
	}
	bindDN, err := connectorValue(st.Id, "ad connector", "bindDN", cn.BindDN)
	if err != nil {
		return 0, err
	}
	dn, err := connectorValue(st.Id, "ad connector", "dn", cn.DN)
	if err != nil {
		return 0, err
	}
	newDN, err := connectorValue(st.Id, "ad connector", "newDN", cn.NewDN)
	if err != nil {
		return 0, err
	}
	baseDN, err := connectorValue(st.Id, "ad connector", "baseDN", cn.BaseDN)
	if err != nil {
		return 0, err
	}
	filter, err := connectorValue(st.Id, "ad connector", "filter", cn.Filter)
	if err != nil {
		return 0, err
	}
	memberDN, err := connectorValue(st.Id, "ad connector", "memberDN", cn.MemberDN)
	if err != nil {
		return 0, err
	}
	newPassword, err := connectorValue(st.Id, "ad connector", "newPassword", cn.NewPassword)
	if err != nil {
		return 0, err
	}
	return b.AddAdConnectorTask(AdConfig{
		Connector:      named,
		URL:            url,
		BindDN:         bindDN,
		BindSecret:     strings.TrimSpace(cn.BindSecret),
		StartTLS:       strings.EqualFold(strings.TrimSpace(cn.StartTLS), "true"),
		Op:             op,
		DN:             dn,
		MemberDN:       memberDN,
		EntryVar:       strings.TrimSpace(cn.EntryVariable),
		NewPassword:    newPassword,
		NewDN:          newDN,
		BaseDN:         baseDN,
		Filter:         filter,
		Scope:          scope,
		CookieVar:      strings.TrimSpace(cn.CookieVariable),
		ResultVar:      strings.TrimSpace(cn.ResultVariable),
		MaxEntries:     maxEntries,
		ObjectSecurity: strings.EqualFold(strings.TrimSpace(cn.ObjectSecurity), "true"),
		Retries:        retries,
	}), nil
}

// adMaxEntries reads the authored cap on what an operation returns. It applies to the
// two reading operations; on any other it is an author believing something the
// connector will not do, reported rather than ignored.
func adMaxEntries(taskID, op string, spec adOp, raw string) (int32, error) {
	reads := spec.isSync || spec.isSearch
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if reads {
			return spec.maxEntries, nil
		}
		return 0, nil
	}
	if !reads {
		return 0, fmt.Errorf("compiler: ad connector task %q sets maxEntries on operation %q, which returns no entries (maxEntries applies to sync and search)", taskID, op)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("compiler: ad connector task %q has a non-numeric maxEntries %q", taskID, raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("compiler: ad connector task %q has a negative maxEntries %d", taskID, n)
	}
	return int32(n), nil
}

// adSearchScope reads the authored search scope, defaulting an empty one to the whole
// subtree — which is what "does this entry exist anywhere under here" means, and the
// answer an author who did not think about scope wants.
//
// It belongs to search alone. A sync authors none because AD answers DirSync only for
// the whole subtree, and the writing operations address one entry by DN, where a scope
// would mean nothing at all — so authoring one there is refused rather than dropped.
func adSearchScope(taskID, op string, isSearch bool, raw string) (string, error) {
	scope := strings.ToLower(strings.TrimSpace(raw))
	if !isSearch {
		if scope != "" {
			return "", fmt.Errorf("compiler: ad connector task %q sets a scope on operation %q, which addresses one entry rather than a subtree (scope applies to search)", taskID, op)
		}
		return "", nil
	}
	if scope == "" {
		return "sub", nil
	}
	if !directoryScopes[scope] {
		return "", fmt.Errorf("compiler: ad connector task %q has an unknown scope %q (want base, one, or sub)", taskID, raw)
	}
	return scope, nil
}

// soapVersions is the set of SOAP protocol versions a connector task can author. 1.1
// sends text/xml with a quoted SOAPAction header; 1.2 sends application/soap+xml with
// the action as a Content-Type parameter (envelope namespaces differ too).
var soapVersions = map[string]bool{"1.1": true, "1.2": true}

// compileSoapConnectorTask compiles an <atlas:soapConnector> task: it invokes a SOAP
// operation against a model-authored web-service endpoint via the job path (ADR-0165),
// not an external service-task worker. Like REST the endpoint lives in the model and
// credentials never do; unlike REST it wraps the authored body in a SOAP envelope and
// turns a SOAP Fault into the job-failure message. A call needs an endpoint, an
// operation name, and a body (the operation's request element).
func compileSoapConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Soap
	if strings.TrimSpace(cn.Endpoint) == "" {
		return 0, fmt.Errorf("compiler: soap connector task %q needs an endpoint (the web-service URL)", st.Id)
	}
	if strings.TrimSpace(cn.Operation) == "" {
		return 0, fmt.Errorf("compiler: soap connector task %q needs an operation", st.Id)
	}
	if strings.TrimSpace(cn.Body) == "" {
		return 0, fmt.Errorf("compiler: soap connector task %q needs a body (the SOAP request payload)", st.Id)
	}
	version := strings.TrimSpace(cn.Version)
	if version == "" {
		version = "1.1"
	}
	if !soapVersions[version] {
		return 0, fmt.Errorf("compiler: soap connector task %q has an unknown soapVersion %q (want 1.1 or 1.2)", st.Id, cn.Version)
	}
	endpoint, err := connectorValue(st.Id, "soap connector", "endpoint", cn.Endpoint)
	if err != nil {
		return 0, err
	}
	action, err := connectorValue(st.Id, "soap connector", "soapAction", cn.Action)
	if err != nil {
		return 0, err
	}
	body, err := connectorValue(st.Id, "soap connector", "body", cn.Body)
	if err != nil {
		return 0, err
	}
	auth, err := connectorAuth(st.Id, "soap connector", cn.AuthType, cn.AuthUsername, cn.AuthApiKeyName, cn.AuthSecret)
	if err != nil {
		return 0, err
	}
	return b.AddSoapConnectorTask(SoapConfig{
		Endpoint:  endpoint,
		Op:        strings.TrimSpace(cn.Operation),
		Action:    action,
		Body:      body,
		Version:   version,
		ResultVar: strings.TrimSpace(cn.ResultVariable),
		Auth:      auth,
		Retries:   retries,
	}), nil
}

// ldapOps is the set of directory operations an LDAP connector task can author.
//
// modify, add-values and delete-values are the same LDAP modify with different change
// operations: modify *replaces* an attribute wholesale, while the other two change
// individual values. Whole-attribute replace was the only shape ADR-0154 shipped, and
// it is the wrong one for a multi-valued attribute two processes both write — one
// adding a group member does not intend to remove everyone else's (ADR-0154, amended).
var ldapOps = map[string]bool{
	"search": true, "add": true, "modify": true, "add-values": true,
	"delete-values": true, "delete": true, "modify-password": true,
}

// ldapEntryOps are the operations that read the authored attribute object.
var ldapEntryOps = map[string]bool{"add": true, "modify": true, "add-values": true, "delete-values": true}

// Default search bounds, applied by the compiler when a model authors none, so the
// runtime interprets nothing (I5).
//
// Paging defaults *on* because a directory's admin size limit otherwise refuses a
// perfectly reasonable search, and an author who has not met that limit has no reason
// to know the control exists. The entry cap defaults on for the reason sqldb's row cap
// does (ADR-0173): an unbounded subtree search into a process variable is the failure
// mode this is hardening against, and a short result set would be a wrong answer
// rather than a partial one.
const (
	defaultLdapPageSize   = 500
	defaultLdapMaxEntries = 1000
)

// directoryScopes is the set of LDAP search scopes a model may author, shared by the
// two connectors that search a directory. An empty scope defaults to "sub" (the whole
// subtree) at compile time.
var directoryScopes = map[string]bool{"base": true, "one": true, "sub": true}

// compileLdapConnectorTask compiles an <atlas:ldapConnector> task: it performs a
// directory operation against a model-authored LDAP server via the job path
// (ADR-0154), not an external service-task worker. The server URL and DNs live in the
// model; the bind password never does (it is a secret reference, ADR-0041). A search
// needs a base DN; add/modify/delete/modify-password need a target DN; add/modify take
// an attribute variable; modify-password needs a new password.
func compileLdapConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Ldap
	if strings.TrimSpace(cn.URL) == "" {
		return 0, fmt.Errorf("compiler: ldap connector task %q needs a url (ldap://host or ldaps://host)", st.Id)
	}
	op := strings.ToLower(strings.TrimSpace(cn.Operation))
	if op == "" {
		return 0, fmt.Errorf("compiler: ldap connector task %q needs an operation (%s)", st.Id, strings.Join(ldapOpNames(), ", "))
	}
	if !ldapOps[op] {
		return 0, fmt.Errorf("compiler: ldap connector task %q has an unknown operation %q (want %s)", st.Id, cn.Operation, strings.Join(ldapOpNames(), ", "))
	}
	scope := strings.ToLower(strings.TrimSpace(cn.Scope))
	if op == "search" {
		if strings.TrimSpace(cn.BaseDN) == "" {
			return 0, fmt.Errorf("compiler: ldap connector task %q operation search needs a baseDN", st.Id)
		}
		if scope == "" {
			scope = "sub"
		}
		if !directoryScopes[scope] {
			return 0, fmt.Errorf("compiler: ldap connector task %q has an unknown scope %q (want base, one, or sub)", st.Id, cn.Scope)
		}
	} else {
		scope = ""
		if strings.TrimSpace(cn.DN) == "" {
			return 0, fmt.Errorf("compiler: ldap connector task %q operation %q needs a dn", st.Id, op)
		}
	}
	if ldapEntryOps[op] && strings.TrimSpace(cn.EntryVariable) == "" {
		return 0, fmt.Errorf("compiler: ldap connector task %q operation %q needs an entryVariable naming the attribute object", st.Id, op)
	}
	if op == "modify-password" && strings.TrimSpace(cn.NewPassword) == "" {
		return 0, fmt.Errorf("compiler: ldap connector task %q operation modify-password needs a newPassword", st.Id)
	}
	pageSize, err := ldapSearchBound(st.Id, op, "pageSize", cn.PageSize, defaultLdapPageSize)
	if err != nil {
		return 0, err
	}
	maxEntries, err := ldapSearchBound(st.Id, op, "maxEntries", cn.MaxEntries, defaultLdapMaxEntries)
	if err != nil {
		return 0, err
	}
	url, err := connectorValue(st.Id, "ldap connector", "url", cn.URL)
	if err != nil {
		return 0, err
	}
	bindDN, err := connectorValue(st.Id, "ldap connector", "bindDN", cn.BindDN)
	if err != nil {
		return 0, err
	}
	dn, err := connectorValue(st.Id, "ldap connector", "dn", cn.DN)
	if err != nil {
		return 0, err
	}
	baseDN, err := connectorValue(st.Id, "ldap connector", "baseDN", cn.BaseDN)
	if err != nil {
		return 0, err
	}
	filter, err := connectorValue(st.Id, "ldap connector", "filter", cn.Filter)
	if err != nil {
		return 0, err
	}
	newPassword, err := connectorValue(st.Id, "ldap connector", "newPassword", cn.NewPassword)
	if err != nil {
		return 0, err
	}
	return b.AddLdapConnectorTask(LdapConfig{
		URL:              url,
		BindDN:           bindDN,
		BindSecret:       strings.TrimSpace(cn.BindSecret),
		StartTLS:         strings.EqualFold(strings.TrimSpace(cn.StartTLS), "true"),
		Op:               op,
		DN:               dn,
		BaseDN:           baseDN,
		Filter:           filter,
		Scope:            scope,
		EntryVar:         strings.TrimSpace(cn.EntryVariable),
		NewPassword:      newPassword,
		ResultVar:        strings.TrimSpace(cn.ResultVariable),
		PageSize:         pageSize,
		MaxEntries:       maxEntries,
		ClientCertSecret: strings.TrimSpace(cn.ClientCertSecret),
		Retries:          retries,
	}), nil
}

// ldapOpNames lists the directory operations, sorted, for the error messages.
func ldapOpNames() []string {
	out := make([]string, 0, len(ldapOps))
	for n := range ldapOps {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ldapSearchBound reads one authored search bound, returning the effective value the
// compiled process carries: the default when the attribute is absent, and the
// authored number otherwise — including 0, which is how a model says unbounded.
//
// A bound on a non-search operation is rejected rather than ignored: it is an author
// believing something the connector will not do, and silently dropping it is how a
// model comes to look bounded without being it.
func ldapSearchBound(taskID, op, what, raw string, def int32) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if op == "search" {
			return def, nil
		}
		return 0, nil
	}
	if op != "search" {
		return 0, fmt.Errorf("compiler: ldap connector task %q sets %s on operation %q, which returns no entries (%s applies to search)", taskID, what, op, what)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("compiler: ldap connector task %q has a non-numeric %s %q", taskID, what, raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("compiler: ldap connector task %q has a negative %s %d", taskID, what, n)
	}
	return int32(n), nil
}

// scimOps is the set of SCIM 2.0 operations a connector task can author. create/get/
// replace/patch/delete/search map to the provider's POST/GET/PUT/PATCH/DELETE and a
// filtered GET (RFC 7644 §3).
var scimOps = map[string]bool{"create": true, "get": true, "replace": true, "patch": true, "delete": true, "search": true}

// compileScimConnectorTask compiles an <atlas:scimConnector> task: it performs a SCIM
// 2.0 resource operation against a model-authored service-provider endpoint via the
// job path (ADR-0153), not an external service-task worker. Like REST the base URL
// lives in the model and credentials never do; unlike REST it speaks SCIM (resource
// paths, operations, filtered search). A get/replace/patch/delete needs a resource
// id; the payload for create/replace/patch is a named body variable or the whole
// instance scope.
func compileScimConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Scim
	if strings.TrimSpace(cn.BaseUrl) == "" {
		return 0, fmt.Errorf("compiler: scim connector task %q needs a baseUrl", st.Id)
	}
	if strings.TrimSpace(cn.Resource) == "" {
		return 0, fmt.Errorf("compiler: scim connector task %q needs a resource (e.g. Users)", st.Id)
	}
	op := strings.ToLower(strings.TrimSpace(cn.Operation))
	if op == "" {
		return 0, fmt.Errorf("compiler: scim connector task %q needs an operation (create, get, replace, patch, delete, or search)", st.Id)
	}
	if !scimOps[op] {
		return 0, fmt.Errorf("compiler: scim connector task %q has an unknown operation %q (want create, get, replace, patch, delete, or search)", st.Id, cn.Operation)
	}
	if strings.TrimSpace(cn.ResourceId) == "" {
		switch op {
		case "get", "replace", "patch", "delete":
			return 0, fmt.Errorf("compiler: scim connector task %q operation %q needs a resourceId", st.Id, op)
		}
	}
	baseURL, err := connectorValue(st.Id, "scim connector", "baseUrl", cn.BaseUrl)
	if err != nil {
		return 0, err
	}
	resource, err := connectorValue(st.Id, "scim connector", "resource", cn.Resource)
	if err != nil {
		return 0, err
	}
	resourceID, err := connectorValue(st.Id, "scim connector", "resourceId", cn.ResourceId)
	if err != nil {
		return 0, err
	}
	filter, err := connectorValue(st.Id, "scim connector", "filter", cn.Filter)
	if err != nil {
		return 0, err
	}
	auth, err := connectorAuth(st.Id, "scim connector", cn.AuthType, cn.AuthUsername, cn.AuthApiKeyName, cn.AuthSecret)
	if err != nil {
		return 0, err
	}
	return b.AddScimConnectorTask(ScimConfig{
		BaseURL:    baseURL,
		Resource:   resource,
		Op:         op,
		ResourceID: resourceID,
		Filter:     filter,
		BodyVar:    strings.TrimSpace(cn.BodyVariable),
		ResultVar:  strings.TrimSpace(cn.ResultVariable),
		Auth:       auth,
		Retries:    retries,
	}), nil
}

// firstNonBlank returns the first value that is not empty once trimmed — the
// precedence rule for an attribute a task can carry in more than one place (a
// connector's own retries over its task definition's, ADR-0135).
func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// parseRetries interprets a task's authored retries attribute — the number of
// attempts the engine grants the task's job before an exhausted failure parks the
// token behind an incident (ADR-0061/0135). An omitted (blank) attribute means
// defaultRetries. label names the element ("service task", "script task", …) and id
// identifies it, so every task kind reports the same diagnostic.
//
// A budget below 1 is refused at deploy time (invariant I5): a job is on the
// activatable index only while it has retries left (state.PutJob), so a task
// authored with no attempts would create a job no worker is ever offered — a token
// parked forever with no incident to resolve.
func parseRetries(label, id, attr string) (int32, error) {
	s := strings.TrimSpace(attr)
	if s == "" {
		return defaultRetries, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("compiler: %s %q has invalid retries %q: %w", label, id, s, err)
	}
	if n < 1 {
		return 0, fmt.Errorf("compiler: %s %q has retries %d: a job needs at least one attempt — use retries=\"1\" for a single try (no retry)", label, id, n)
	}
	return int32(n), nil
}

// serviceTaskRetries reads the retries count from a job-worker task's
// <taskDefinition>, defaulting to defaultRetries when it is omitted. label names the
// element ("service task"/"send task") for diagnostics. A connector extension on the
// same task may override it with its own retries attribute (ADR-0135).
func serviceTaskRetries(st xmlServiceTask, label string) (int32, error) {
	return parseRetries(label, st.Id, st.TaskDefinition.Retries)
}

// compileClioConnectorTask compiles an <atlas:clioConnector> task: it delegates to a
// server-registered clio connector via the job path (ADR-0036), not to an external
// service-task worker. operation selects the clio call (write/query/read); write is
// the default for back-compatibility with the original write-only element.
func compileClioConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Clio
	if cn.Connector == "" {
		return 0, fmt.Errorf("compiler: clio connector task %q needs a connector", st.Id)
	}
	switch op := clioOperation(cn.Operation); op {
	case "write":
		if cn.Subject == "" || cn.EventType == "" {
			return 0, fmt.Errorf("compiler: clio write task %q needs subject and eventType", st.Id)
		}
		return b.AddClioWriteTask(cn.Connector, cn.Subject, cn.EventType, retries), nil
	case "query":
		if cn.Query == "" && cn.Subject == "" {
			return 0, fmt.Errorf("compiler: clio query task %q needs a query or a subject", st.Id)
		}
		if strings.TrimSpace(cn.ResultVariable) == "" {
			return 0, fmt.Errorf("compiler: clio query task %q needs a resultVariable", st.Id)
		}
		return b.AddClioQueryTask(cn.Connector, cn.Subject, cn.ReduceSpec, cn.Query, strings.TrimSpace(cn.ResultVariable), retries), nil
	case "read":
		if cn.Subject == "" {
			return 0, fmt.Errorf("compiler: clio read task %q needs a subject", st.Id)
		}
		if strings.TrimSpace(cn.ResultVariable) == "" {
			return 0, fmt.Errorf("compiler: clio read task %q needs a resultVariable", st.Id)
		}
		limit, err := clioLimit(st.Id, cn.Limit)
		if err != nil {
			return 0, err
		}
		return b.AddClioReadTask(cn.Connector, cn.Subject, strings.TrimSpace(cn.ResultVariable), limit, retries), nil
	default:
		return 0, fmt.Errorf("compiler: clio connector task %q has unknown operation %q (want write, query, or read)", st.Id, op)
	}
}

// compileRestConnectorTask compiles an <atlas:restConnector> task: it calls the
// model-authored URL via the job path (ADR-0067), not an external service-task worker.
// The URL lives in the model (unlike clio's registry-only endpoint); credentials never
// do.
func compileRestConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Rest
	if strings.TrimSpace(cn.Url) == "" {
		return 0, fmt.Errorf("compiler: rest connector task %q needs a url", st.Id)
	}
	method, err := normalizeHTTPMethod(cn.Method)
	if err != nil {
		return 0, fmt.Errorf("compiler: rest connector task %q: %w", st.Id, err)
	}
	url, err := restValue(st.Id, "url", cn.Url)
	if err != nil {
		return 0, err
	}
	headers, err := httpKVList(st.Id, "header", cn.Headers)
	if err != nil {
		return 0, err
	}
	query, err := httpKVList(st.Id, "query parameter", cn.QueryParams)
	if err != nil {
		return 0, err
	}
	auth, err := restAuth(st.Id, cn)
	if err != nil {
		return 0, err
	}
	return b.AddRestConnectorTask(RestConfig{
		Method:    method,
		Url:       url,
		ResultVar: strings.TrimSpace(cn.ResultVariable),
		Headers:   headers,
		Query:     query,
		Auth:      auth,
		Retries:   retries,
	}), nil
}

// compileMailConnectorTask compiles an <atlas:mailConnector> task: it sends a
// model-authored message through a server-registered mail provider via the job path
// (ADR-0079). The provider (host, credentials) is resolved server-side by connector
// name, like clio; only the message (recipients, subject, and the text and/or HTML
// body) lives in the model.
func compileMailConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Mail
	if strings.TrimSpace(cn.Connector) == "" {
		return 0, fmt.Errorf("compiler: mail connector task %q needs a connector", st.Id)
	}
	if strings.TrimSpace(cn.To) == "" {
		return 0, fmt.Errorf("compiler: mail connector task %q needs a to recipient", st.Id)
	}
	to, err := restValue(st.Id, "to", cn.To)
	if err != nil {
		return 0, err
	}
	cc, err := restValue(st.Id, "cc", cn.Cc)
	if err != nil {
		return 0, err
	}
	bcc, err := restValue(st.Id, "bcc", cn.Bcc)
	if err != nil {
		return 0, err
	}
	from, err := restValue(st.Id, "from", cn.From)
	if err != nil {
		return 0, err
	}
	subject, err := restValue(st.Id, "subject", cn.Subject)
	if err != nil {
		return 0, err
	}
	body, err := restValue(st.Id, "body", cn.Body)
	if err != nil {
		return 0, err
	}
	bodyHTML, err := restValue(st.Id, "bodyHtml", cn.BodyHtml)
	if err != nil {
		return 0, err
	}
	return b.AddMailConnectorTask(MailConfig{
		Connector: strings.TrimSpace(cn.Connector),
		To:        to,
		Cc:        cc,
		Bcc:       bcc,
		From:      from,
		Subject:   subject,
		Body:      body,
		BodyHTML:  bodyHTML,
		Retries:   retries,
	}), nil
}

// compileUserConnectorTask compiles an <atlas:userConnector> task: it delegates to
// the in-process user-provisioning worker via the job path (ADR-0123), which mutates
// the internal user store. operation selects the action; username is always required,
// and create/set-password additionally require a password. The field values are
// literal-or-FEEL (like the mail connector); no connector name or credential is
// authored — the worker uses the local store, gated to the system project.
func compileUserConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.User
	op := strings.TrimSpace(cn.Operation)
	switch op {
	case "create", "set-password", "disable":
	default:
		return 0, fmt.Errorf("compiler: user connector task %q has unknown operation %q (want create, set-password, or disable)", st.Id, op)
	}
	if strings.TrimSpace(cn.Username) == "" {
		return 0, fmt.Errorf("compiler: user connector task %q needs a username", st.Id)
	}
	if (op == "create" || op == "set-password") && strings.TrimSpace(cn.Password) == "" {
		return 0, fmt.Errorf("compiler: user connector task %q (%s) needs a password", st.Id, op)
	}
	username, err := restValue(st.Id, "username", cn.Username)
	if err != nil {
		return 0, err
	}
	email, err := restValue(st.Id, "email", cn.Email)
	if err != nil {
		return 0, err
	}
	displayName, err := restValue(st.Id, "displayName", cn.DisplayName)
	if err != nil {
		return 0, err
	}
	roles, err := restValue(st.Id, "roles", cn.Roles)
	if err != nil {
		return 0, err
	}
	password, err := restValue(st.Id, "password", cn.Password)
	if err != nil {
		return 0, err
	}
	return b.AddUserConnectorTask(UserConnectorConfig{
		Operation:   op,
		Username:    username,
		Email:       email,
		DisplayName: displayName,
		Roles:       roles,
		Password:    password,
		Retries:     retries,
	}), nil
}

// compileSharePointConnectorTask compiles an <atlas:sharepointConnector> task: it
// creates a list item in a model-authored site/list through a server-registered
// SharePoint provider (Microsoft Graph) via the job path (ADR-0141). The provider
// (Graph base, OAuth credential) is resolved server-side by connector name, like mail;
// only the target (site, list, item fields) lives in the model.
func compileSharePointConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.sharePointConn()
	if strings.TrimSpace(cn.Connector) == "" {
		return 0, fmt.Errorf("compiler: sharepoint connector task %q needs a connector", st.Id)
	}
	if strings.TrimSpace(cn.Site) == "" {
		return 0, fmt.Errorf("compiler: sharepoint connector task %q needs a site", st.Id)
	}
	if strings.TrimSpace(cn.List) == "" {
		return 0, fmt.Errorf("compiler: sharepoint connector task %q needs a list", st.Id)
	}
	site, err := restValue(st.Id, "site", cn.Site)
	if err != nil {
		return 0, err
	}
	list, err := restValue(st.Id, "list", cn.List)
	if err != nil {
		return 0, err
	}
	fields, err := httpKVList(st.Id, "item field", cn.Fields)
	if err != nil {
		return 0, err
	}
	return b.AddSharePointConnectorTask(SharePointConfig{
		Connector: strings.TrimSpace(cn.Connector),
		Site:      site,
		List:      list,
		Fields:    fields,
		ResultVar: strings.TrimSpace(cn.ResultVariable),
		Retries:   retries,
	}), nil
}

// compileRemedyConnectorTask compiles an <atlas:remedyConnector> task: it creates an
// entry (e.g. an incident) in a Remedy form through the AR System REST API via the job
// path (ADR-0106). The Remedy base URL and credentials are resolved server-side by
// connector name, like clio and mail; only the form and its field values live in the
// model.
func compileRemedyConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Remedy
	if strings.TrimSpace(cn.Connector) == "" {
		return 0, fmt.Errorf("compiler: remedy connector task %q needs a connector", st.Id)
	}
	if strings.TrimSpace(cn.Form) == "" {
		return 0, fmt.Errorf("compiler: remedy connector task %q needs a form", st.Id)
	}
	form, err := restValue(st.Id, "form", cn.Form)
	if err != nil {
		return 0, err
	}
	fields, err := httpKVList(st.Id, "field", cn.Fields)
	if err != nil {
		return 0, err
	}
	return b.AddRemedyConnectorTask(RemedyConfig{
		Connector: strings.TrimSpace(cn.Connector),
		Form:      form,
		Fields:    fields,
		ResultVar: strings.TrimSpace(cn.ResultVariable),
		Retries:   retries,
	}), nil
}

// compileWebScrapeConnectorTask compiles an <atlas:webscrapeConnector> task.
// HTML preserves ADR-0118's selector-to-string-array behavior. ADR-0190 adds RSS
// and Atom as explicit deploy-time formats whose worker output is a structured feed
// array. No response inspection chooses the mode at runtime (I5).
func compileWebScrapeConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.WebScrape
	if strings.TrimSpace(cn.Url) == "" {
		return 0, fmt.Errorf("compiler: webscrape connector task %q needs a url", st.Id)
	}
	if strings.TrimSpace(cn.ResultVariable) == "" {
		return 0, fmt.Errorf("compiler: webscrape connector task %q needs a resultVariable", st.Id)
	}
	format, err := webScrapeFormat(st.Id, cn.Format)
	if err != nil {
		return 0, err
	}
	maxItems, err := webScrapeMaxItems(st.Id, cn.MaxItems)
	if err != nil {
		return 0, err
	}
	hasSelector := strings.TrimSpace(cn.Selector) != ""
	hasAttribute := strings.TrimSpace(cn.Attribute) != ""
	switch format {
	case WebScrapeFormatHTML:
		if !hasSelector {
			return 0, fmt.Errorf("compiler: webscrape connector task %q needs a selector for html format", st.Id)
		}
	case WebScrapeFormatRSS, WebScrapeFormatAtom:
		if hasSelector {
			return 0, fmt.Errorf("compiler: webscrape connector task %q format %q does not use a selector", st.Id, format.String())
		}
		if hasAttribute {
			return 0, fmt.Errorf("compiler: webscrape connector task %q format %q does not use an attribute", st.Id, format.String())
		}
	}
	url, err := restValue(st.Id, "url", cn.Url)
	if err != nil {
		return 0, err
	}
	var selector RestExpr
	if format == WebScrapeFormatHTML {
		selector, err = restValue(st.Id, "selector", cn.Selector)
		if err != nil {
			return 0, err
		}
	}
	return b.AddWebScrapeExtractionTask(WebScrapeExtractionConfig{
		Url:       url,
		Selector:  selector,
		Attribute: strings.TrimSpace(cn.Attribute),
		Format:    format,
		MaxItems:  maxItems,
		Result:    strings.TrimSpace(cn.ResultVariable),
		Retries:   retries,
	}), nil
}

func webScrapeFormat(taskID, raw string) (WebScrapeFormat, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "html":
		return WebScrapeFormatHTML, nil
	case "rss":
		return WebScrapeFormatRSS, nil
	case "atom":
		return WebScrapeFormatAtom, nil
	default:
		return WebScrapeFormatHTML, fmt.Errorf("compiler: webscrape connector task %q has an unknown format %q (want html, rss, or atom)", taskID, raw)
	}
}

func webScrapeMaxItems(taskID, raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("compiler: webscrape connector task %q has a non-numeric maxItems %q", taskID, raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("compiler: webscrape connector task %q has a negative maxItems %d", taskID, n)
	}
	return int32(n), nil
}

// jiraDefaultMaxResults is the cap a search gets when a model authors none. The
// compiler writes the effective value into the detail so the runtime interprets
// nothing (I5); a model asking for more says so, and 0 means unbounded.
const jiraDefaultMaxResults int32 = 50

// jiraOp describes what one Jira operation requires of a model, and what it is allowed
// to carry. The table is the compiler's half of connector/jira's Ops table; the drift
// test TestJiraOpsMatchTheConnector keeps the two from disagreeing about the operation
// set, which is the failure this shape exists to prevent.
//
// Both halves matter. "Needs" is the familiar one: a create with no summary cannot be
// sent. "Takes" is the half that is easy to forget and expensive to have forgotten — a
// comment authored on a search, or a project on a transition, would otherwise compile
// and then be silently dropped at call time, which from the author's side is
// indistinguishable from a connector that ignored it.
type jiraOp struct {
	needsIssue      bool
	needsProject    bool // project and issue type: what an issue is created in and as
	needsSummary    bool
	needsTransition bool
	needsComment    bool
	needsAssignee   bool
	needsJQL        bool
	// needsQuery marks search-users: the fragment of a name or an address an account is
	// looked up by. Not a reuse of needsJQL, because the two are different languages.
	needsQuery bool
	// takesSummary allows summary and description (create, and an update that changes
	// them); takesComment a comment body alongside another operation; takesFields the
	// extra issue fields; takesSearch a search's own maxResults.
	takesSummary bool
	takesComment bool
	takesFields  bool
	takesSearch  bool
	// takesProject allows a project without requiring it, and without the issue type that
	// creating an issue always pairs it with: an account search may name one to restrict
	// itself to the accounts that project can assign.
	takesProject bool
	// needsResult marks an operation whose whole point is what it returns: a read that
	// discards its answer is a call made for nothing. takesResult marks one that
	// returns something a model may keep or discard — and, by its absence, the three
	// Jira answers with 204 No Content, where a result variable would name a value
	// that is never written.
	needsResult bool
	takesResult bool
	// needsChange marks update-issue: without a summary, a description or one extra
	// field it is a request that changes nothing.
	needsChange bool
}

// jiraOps is the operation table: the loop a process actually runs against an issue
// tracker — open a ticket, read it, change it, move it through its workflow, say
// something on it, hand it to somebody, find the ones that match, and look up the
// account to hand one to. It mirrors connector/jira.Ops, which the drift test
// TestJiraOpsMatchTheConnector keeps honest.
var jiraOps = map[string]jiraOp{
	"create-issue":     {needsProject: true, needsSummary: true, takesSummary: true, takesFields: true, takesResult: true},
	"get-issue":        {needsIssue: true, needsResult: true, takesResult: true},
	"update-issue":     {needsIssue: true, takesSummary: true, takesFields: true, needsChange: true},
	"transition-issue": {needsIssue: true, needsTransition: true, takesComment: true, takesFields: true},
	"add-comment":      {needsIssue: true, needsComment: true, takesComment: true, takesResult: true},
	"assign-issue":     {needsIssue: true, needsAssignee: true},
	"search":           {needsJQL: true, takesSearch: true, needsResult: true, takesResult: true},
	"search-users":     {needsQuery: true, takesProject: true, takesSearch: true, needsResult: true, takesResult: true},
}

// jiraOpNames lists the operations, sorted, for the messages that have to say what was
// expected.
func jiraOpNames() []string {
	out := make([]string, 0, len(jiraOps))
	for name := range jiraOps {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// compileJiraConnectorTask compiles an <atlas:jiraConnector> task: one issue-tracker
// operation against a server-registered Jira instance via the job path
// (ADR-0201). The base URL and credential are resolved server-side by
// connector name, like Remedy's and SharePoint's; only the operation and its values
// live in the model.
func compileJiraConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Jira
	if strings.TrimSpace(cn.Connector) == "" {
		return 0, fmt.Errorf("compiler: jira connector task %q needs a connector (the name the server holds the Jira base URL and credential under)", st.Id)
	}
	op := strings.ToLower(strings.TrimSpace(cn.Operation))
	if op == "" {
		return 0, fmt.Errorf("compiler: jira connector task %q needs an operation (%s)", st.Id, strings.Join(jiraOpNames(), ", "))
	}
	spec, ok := jiraOps[op]
	if !ok {
		return 0, fmt.Errorf("compiler: jira connector task %q has an unknown operation %q (want %s)", st.Id, cn.Operation, strings.Join(jiraOpNames(), ", "))
	}
	// One pass over every authored value: required where the operation needs it,
	// refused where it does not use it. A single table means neither half can be
	// forgotten for a field, and an added field is one row rather than two checks in
	// two places.
	values := []struct {
		attr     string
		raw      string
		required bool
		allowed  bool
		why      string
	}{
		{"issueKey", cn.IssueKey, spec.needsIssue, spec.needsIssue, "the issue key or id the operation addresses (e.g. OPS-42)"},
		{"project", cn.Project, spec.needsProject, spec.needsProject || spec.takesProject,
			"the project key: the project an issue is created in, or the one an account search restricts itself to"},
		{"issueType", cn.IssueType, spec.needsProject, spec.needsProject, "the issue type the issue is created as (e.g. Task)"},
		{"summary", cn.Summary, spec.needsSummary, spec.takesSummary, "the issue's one-line summary"},
		{"description", cn.Description, false, spec.takesSummary, "the issue's description"},
		{"transition", cn.Transition, spec.needsTransition, spec.needsTransition, "the workflow transition to perform, by id or by the name Jira shows"},
		{"comment", cn.Comment, spec.needsComment, spec.takesComment, "the comment body"},
		{"assignee", cn.Assignee, spec.needsAssignee, spec.needsAssignee, "the account the issue is assigned to (an accountId on Jira Cloud, a username on Data Center)"},
		{"jql", cn.JQL, spec.needsJQL, spec.needsJQL, "the JQL query the search runs"},
		{"query", cn.Query, spec.needsQuery, spec.needsQuery, "the name or address fragment an account is looked up by"},
		{"maxResults", cn.MaxResults, false, spec.takesSearch, "how many results a search may return"},
		{"resultVariable", cn.ResultVariable, spec.needsResult, spec.takesResult, "the process variable receiving what Jira returned"},
	}
	for _, v := range values {
		set := strings.TrimSpace(v.raw) != ""
		if v.required && !set {
			return 0, fmt.Errorf("compiler: jira connector task %q operation %q needs a %s (%s)", st.Id, op, v.attr, v.why)
		}
		if set && !v.allowed {
			return 0, fmt.Errorf("compiler: jira connector task %q operation %q does not use %s (%s); remove it rather than leaving a value the connector ignores",
				st.Id, op, v.attr, v.why)
		}
	}
	if len(cn.Fields) > 0 && !spec.takesFields {
		return 0, fmt.Errorf("compiler: jira connector task %q operation %q does not use jiraField values; remove them rather than leaving values the connector ignores", st.Id, op)
	}
	fields, err := httpKVList(st.Id, "jira field", cn.Fields)
	if err != nil {
		return 0, err
	}
	if spec.needsChange && strings.TrimSpace(cn.Summary) == "" && strings.TrimSpace(cn.Description) == "" && len(fields) == 0 {
		return 0, fmt.Errorf("compiler: jira connector task %q operation %q changes nothing: give it a summary, a description, or at least one jiraField", st.Id, op)
	}
	maxResults := int32(0)
	if spec.takesSearch {
		maxResults, err = jiraMaxResults(st.Id, cn.MaxResults)
		if err != nil {
			return 0, err
		}
	}
	cfg := JiraConfig{
		Connector:  strings.TrimSpace(cn.Connector),
		Operation:  op,
		MaxResults: maxResults,
		Fields:     fields,
		ResultVar:  strings.TrimSpace(cn.ResultVariable),
		Retries:    retries,
	}
	// Each authored value is literal or FEEL (the fx toggle, ADR-0067), compiled once
	// here and evaluated over the variables the task sees at call time.
	for _, v := range []struct {
		what string
		raw  string
		into *RestExpr
	}{
		{"issueKey", cn.IssueKey, &cfg.Issue},
		{"project", cn.Project, &cfg.Project},
		{"issueType", cn.IssueType, &cfg.IssueType},
		{"summary", cn.Summary, &cfg.Summary},
		{"description", cn.Description, &cfg.Description},
		{"transition", cn.Transition, &cfg.Transition},
		{"comment", cn.Comment, &cfg.Comment},
		{"assignee", cn.Assignee, &cfg.Assignee},
		{"jql", cn.JQL, &cfg.JQL},
		{"query", cn.Query, &cfg.Query},
	} {
		if strings.TrimSpace(v.raw) == "" {
			continue
		}
		val, err := connectorValue(st.Id, "jira connector", v.what, v.raw)
		if err != nil {
			return 0, err
		}
		*v.into = val
	}
	return b.AddJiraConnectorTask(cfg), nil
}

// jiraMaxResults reads a search's cap, applying the default when a model authors none.
// A cap that is not a number, or is negative, is refused at deploy rather than
// silently read as "no cap" at call time.
func jiraMaxResults(taskID, raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return jiraDefaultMaxResults, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("compiler: jira connector task %q has a non-numeric maxResults %q", taskID, raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("compiler: jira connector task %q has a negative maxResults %d", taskID, n)
	}
	return int32(n), nil
}

// The value-input modes a Google Sheets task may author. They are spelled here as well
// as in connector/googlesheets because the compiler cannot import the connector (the
// dependency runs the other way); the drift test guards the seam.
//
// user is the default because a process writing a date or an amount into a sheet
// people read wants it to *be* a date, not a string that looks like one.
const (
	sheetsInputUser = "user"
	sheetsInputRaw  = "raw"
)

// googleSheetsOp describes what one spreadsheet operation requires of a model, and
// what it is allowed to carry. The table is the compiler's half of
// connector/googlesheets's Ops table; the drift test
// TestGoogleSheetsOpsMatchTheConnector keeps the two from disagreeing about the
// operation set, which is the failure this shape exists to prevent.
//
// Both halves matter. "Needs" is the familiar one: a write with no values cannot be
// sent. "Takes" is the half that is easy to forget and expensive to have forgotten — a
// header authored on a write, or a range on a create, would otherwise compile and then
// be silently dropped at call time, which from the author's side is indistinguishable
// from a connector that ignored it.
type googleSheetsOp struct {
	needsSpreadsheet bool
	needsTitle       bool
	needsSheet       bool
	takesSheet       bool
	needsRange       bool
	needsValues      bool
	takesColumns     bool
	takesInput       bool
	takesHeader      bool
	takesFolder      bool
	// needsResult marks an operation whose whole point is what it returns: a read that
	// discards its answer is a call made for nothing. takesResult marks one that
	// returns something a model may keep or discard — and, by its absence, the two
	// deletes, where a result variable would name a value nobody has a use for.
	needsResult bool
	takesResult bool
}

// googleSheetsOps is the operation table: the loop a process actually runs against a
// spreadsheet — make one, give it a tab, read what is in it, change what is in it, add
// to it, empty part of it, and take either the tab or the file away again. It mirrors
// connector/googlesheets.Ops, which the drift test keeps honest.
var googleSheetsOps = map[string]googleSheetsOp{
	"create-spreadsheet": {needsTitle: true, takesSheet: true, takesFolder: true, takesResult: true},
	"add-sheet":          {needsSpreadsheet: true, needsSheet: true, takesResult: true},
	"read-range":         {needsSpreadsheet: true, needsRange: true, takesHeader: true, needsResult: true, takesResult: true},
	"write-range":        {needsSpreadsheet: true, needsRange: true, needsValues: true, takesColumns: true, takesInput: true, takesResult: true},
	"append-row":         {needsSpreadsheet: true, needsRange: true, needsValues: true, takesColumns: true, takesInput: true, takesResult: true},
	"clear-range":        {needsSpreadsheet: true, needsRange: true, takesResult: true},
	"delete-sheet":       {needsSpreadsheet: true, needsSheet: true},
	"delete-spreadsheet": {needsSpreadsheet: true},
}

// googleSheetsOpNames lists the operations, sorted, for the messages that have to say
// what was expected.
func googleSheetsOpNames() []string {
	out := make([]string, 0, len(googleSheetsOps))
	for name := range googleSheetsOps {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// compileGoogleSheetsConnectorTask compiles an <atlas:googleSheetsConnector> task: one
// spreadsheet operation against a server-registered Google credential via the job path
// (ADR-draft-google-sheets-worker). The credential is resolved server-side by
// connector name, like Jira's and SharePoint's; only the operation and its values live
// in the model.
func compileGoogleSheetsConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.GoogleSheets
	if strings.TrimSpace(cn.Connector) == "" {
		return 0, fmt.Errorf("compiler: google sheets connector task %q needs a connector (the name the server holds the Google credential under)", st.Id)
	}
	op := strings.ToLower(strings.TrimSpace(cn.Operation))
	if op == "" {
		return 0, fmt.Errorf("compiler: google sheets connector task %q needs an operation (%s)", st.Id, strings.Join(googleSheetsOpNames(), ", "))
	}
	spec, ok := googleSheetsOps[op]
	if !ok {
		return 0, fmt.Errorf("compiler: google sheets connector task %q has an unknown operation %q (want %s)", st.Id, cn.Operation, strings.Join(googleSheetsOpNames(), ", "))
	}
	// One pass over every authored value: required where the operation needs it,
	// refused where it does not use it. A single table means neither half can be
	// forgotten for a field, and an added field is one row rather than two checks in
	// two places. noun is how the "needs" message reads; attr is the attribute the
	// "does not use" message names, because those are the two different things an
	// author has to be told.
	values := []struct {
		attr     string
		noun     string
		raw      string
		required bool
		allowed  bool
	}{
		{"spreadsheet", "a spreadsheet", cn.Spreadsheet, spec.needsSpreadsheet, spec.needsSpreadsheet},
		{"sheet", "a sheet", cn.Sheet, spec.needsSheet, spec.needsSheet || spec.takesSheet},
		{"range", "a range", cn.Range, spec.needsRange, spec.needsRange},
		{"title", "a title", cn.Title, spec.needsTitle, spec.needsTitle},
		{"folder", "a folder", cn.Folder, false, spec.takesFolder},
		{"values", "values to write", cn.Values, spec.needsValues, spec.needsValues},
		{"columns", "columns", cn.Columns, false, spec.takesColumns},
		{"valueInput", "a valueInput", cn.ValueInput, false, spec.takesInput},
		{"header", "a header", cn.Header, false, spec.takesHeader},
		{"resultVariable", "a resultVariable", cn.ResultVariable, spec.needsResult, spec.takesResult},
	}
	for _, v := range values {
		set := strings.TrimSpace(v.raw) != ""
		if v.required && !set {
			return 0, fmt.Errorf("compiler: google sheets connector task %q operation %q needs %s (%s)",
				st.Id, op, v.noun, googleSheetsWhy(v.attr))
		}
		if set && !v.allowed {
			return 0, fmt.Errorf("compiler: google sheets connector task %q operation %q does not use %s (%s); remove it rather than leaving a value the connector ignores",
				st.Id, op, v.attr, googleSheetsWhy(v.attr))
		}
	}
	input, err := googleSheetsInput(st.Id, op, cn.ValueInput, spec.takesInput)
	if err != nil {
		return 0, err
	}
	header, err := googleSheetsBool(st.Id, op, "header", cn.Header)
	if err != nil {
		return 0, err
	}
	cfg := GoogleSheetsConfig{
		Connector: strings.TrimSpace(cn.Connector),
		Operation: op,
		Columns:   googleSheetsColumns(cn.Columns),
		Input:     input,
		Header:    header,
		ResultVar: strings.TrimSpace(cn.ResultVariable),
		Retries:   retries,
	}
	// Each authored value is literal or FEEL (the fx toggle, ADR-0067), compiled once
	// here and evaluated over the variables the task sees at call time.
	for _, v := range []struct {
		what string
		raw  string
		into *RestExpr
	}{
		{"spreadsheet", cn.Spreadsheet, &cfg.Spreadsheet},
		{"sheet", cn.Sheet, &cfg.Sheet},
		{"range", cn.Range, &cfg.Range},
		{"title", cn.Title, &cfg.Title},
		{"folder", cn.Folder, &cfg.Folder},
		{"values", cn.Values, &cfg.Values},
	} {
		if strings.TrimSpace(v.raw) == "" {
			continue
		}
		val, err := connectorValue(st.Id, "google sheets connector", v.what, v.raw)
		if err != nil {
			return 0, err
		}
		*v.into = val
	}
	return b.AddGoogleSheetsConnectorTask(cfg), nil
}

// googleSheetsWhy explains one attribute, so both halves of the check above — the
// missing value and the ignored one — say the same thing about it in one place.
func googleSheetsWhy(attr string) string {
	switch attr {
	case "spreadsheet":
		return "the spreadsheet the operation addresses, by id or by its URL"
	case "sheet":
		return "a tab title: the tab added or deleted, or a new spreadsheet's first tab"
	case "range":
		return "the cells in A1 notation, optionally naming their sheet (e.g. Anträge!A2:F)"
	case "title":
		return "what the new spreadsheet is called"
	case "folder":
		return "the Drive folder the new spreadsheet is moved into, by id or by its URL"
	case "values":
		return "the rows to write: a list of rows, a list of objects, or one row of cells"
	case "columns":
		return "the fields and their order a list of objects is written through"
	case "valueInput":
		return `whether a written value is interpreted as typed ("user") or stored verbatim ("raw")`
	case "header":
		return "read the range's first row as column names and answer with objects"
	default:
		return "the process variable receiving what Google returned"
	}
}

// googleSheetsColumns splits the authored column list into the projection order. The
// spacing an author leaves around a comma is theirs; an empty entry is dropped rather
// than becoming a column with no name.
func googleSheetsColumns(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// googleSheetsInput reads the value-input mode, applying the default when a model
// authors none so the runtime interprets nothing (I5). A mode that is neither is
// refused at deploy rather than silently read as the default at call time, where the
// difference is a formula stored as text.
func googleSheetsInput(taskID, op, raw string, takes bool) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if !takes {
		return "", nil
	}
	switch mode {
	case "":
		return sheetsInputUser, nil
	case sheetsInputUser, sheetsInputRaw:
		return mode, nil
	default:
		return "", fmt.Errorf("compiler: google sheets connector task %q operation %q has an unknown valueInput %q (want %q or %q)",
			taskID, op, raw, sheetsInputUser, sheetsInputRaw)
	}
}

// googleSheetsBool reads a structural boolean attribute. Anything that is neither
// "true" nor "false" is refused rather than read as false, because a misspelling that
// silently turns a flag off is a defect that looks like a working model.
func googleSheetsBool(taskID, op, attr, raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return false, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("compiler: google sheets connector task %q operation %q has a non-boolean %s %q (want true or false)",
			taskID, op, attr, raw)
	}
}
