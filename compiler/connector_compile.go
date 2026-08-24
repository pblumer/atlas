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
	{
		present: func(st xmlServiceTask) bool { return st.MsSql != nil },
		retries: func(st xmlServiceTask) string { return st.MsSql.Retries },
		compile: func(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
			return compileSqlConnectorTask(b, st, retries, sqlProducts[MsSqlJobType])
		},
	},
	{
		present: func(st xmlServiceTask) bool { return st.MariaDB != nil },
		retries: func(st xmlServiceTask) string { return st.MariaDB.Retries },
		compile: func(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
			return compileSqlConnectorTask(b, st, retries, sqlProducts[MariaDBJobType])
		},
	},
	{
		present: func(st xmlServiceTask) bool { return st.Postgres != nil },
		retries: func(st xmlServiceTask) string { return st.Postgres.Retries },
		compile: func(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
			return compileSqlConnectorTask(b, st, retries, sqlProducts[PostgresJobType])
		},
	},
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
	// isList marks the one operation that returns a collection rather than an object
	// or nothing. It is what makes filter/select/pageSize/maxUsers meaningful — and
	// what makes them an error anywhere else.
	isList bool
}

var entraOps = map[string]entraOp{
	"create-user":         {needsAttributes: true},
	"get-user":            {needsUser: true},
	"list-users":          {isList: true},
	"update-user":         {needsUser: true, needsAttributes: true},
	"delete-user":         {needsUser: true},
	"enable":              {needsUser: true},
	"disable":             {needsUser: true},
	"add-group-member":    {needsUser: true, needsGroup: true},
	"remove-group-member": {needsUser: true, needsGroup: true},
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
	if spec.needsAttributes && strings.TrimSpace(cn.AttributesVariable) == "" {
		return 0, fmt.Errorf("compiler: entra connector task %q operation %q needs an attributesVariable naming the directory properties", st.Id, op)
	}
	if spec.isList && strings.TrimSpace(cn.ResultVariable) == "" {
		return 0, fmt.Errorf("compiler: entra connector task %q operation list-users needs a resultVariable (a listing that discards its result is a directory read nothing asked for)", st.Id)
	}
	if err := entraListOnly(st.Id, op, spec.isList, cn); err != nil {
		return 0, err
	}
	pageSize, err := entraListBound(st.Id, op, spec.isList, "pageSize", cn.PageSize, defaultEntraPageSize, maxEntraPageSize)
	if err != nil {
		return 0, err
	}
	maxUsers, err := entraListBound(st.Id, op, spec.isList, "maxUsers", cn.MaxUsers, defaultEntraMaxUsers, 0)
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
	filter, err := connectorValue(st.Id, "entra connector", "filter", cn.Filter)
	if err != nil {
		return 0, err
	}
	return b.AddEntraConnectorTask(EntraConfig{
		Connector:     strings.TrimSpace(cn.Connector),
		Op:            op,
		UserID:        userID,
		GroupID:       groupID,
		AttributesVar: strings.TrimSpace(cn.AttributesVariable),
		ResultVar:     strings.TrimSpace(cn.ResultVariable),
		Filter:        filter,
		Select:        strings.TrimSpace(cn.Select),
		PageSize:      pageSize,
		MaxUsers:      maxUsers,
		Retries:       retries,
	}), nil
}

// entraListOnly rejects a listing attribute on an operation that returns one object
// or none. Ignoring it would be worse than failing: an author who wrote a filter
// believes the task is filtered, and a get-user that quietly ignores one addresses
// whatever userId happens to say instead.
func entraListOnly(taskID, op string, isList bool, cn *xmlEntraConnector) error {
	if isList {
		return nil
	}
	for _, a := range []struct{ what, raw string }{
		{"filter", cn.Filter},
		{"select", cn.Select},
	} {
		if strings.TrimSpace(a.raw) != "" {
			return fmt.Errorf("compiler: entra connector task %q sets %s on operation %q, which addresses one user directly (%s applies to list-users)", taskID, a.what, op, a.what)
		}
	}
	return nil
}

// entraListBound reads one authored listing bound, returning the effective value the
// compiled process carries: the default when the attribute is absent, and the
// authored number otherwise — including 0, which is how a model says unbounded.
// A max of 0 means the bound has no ceiling of its own.
//
// A bound on a non-listing operation is rejected rather than ignored, for
// [entraListOnly]'s reason.
func entraListBound(taskID, op string, isList bool, what, raw string, def, max int32) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if isList {
			return def, nil
		}
		return 0, nil
	}
	if !isList {
		return 0, fmt.Errorf("compiler: entra connector task %q sets %s on operation %q, which returns no collection (%s applies to list-users)", taskID, what, op, what)
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
	isSync        bool // reads a subtree delta rather than acting on one entry
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
	// sync is the only AD operation that reads rather than writes, and the only one
	// that addresses a subtree instead of an entry — so it is also the only one that
	// does not take a dn.
	"sync": {isSync: true},
}

// defaultAdSyncMaxEntries caps one DirSync pass when the model authors no cap. A pass
// is resumable by construction — the cookie says where it got to — so a bound here
// costs nothing but a second pass, unlike a plain search where it costs the answer.
const defaultAdSyncMaxEntries = 1000

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
// the model and the bind password never does (a secret reference, ADR-0041); every
// operation targets a dn. create-user takes an attribute variable; set-password a new
// password; the group operations a member dn.
func compileAdConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.Ad
	if strings.TrimSpace(cn.URL) == "" {
		return 0, fmt.Errorf("compiler: ad connector task %q needs a url (ldaps://host for a password set)", st.Id)
	}
	op := strings.ToLower(strings.TrimSpace(cn.Operation))
	if op == "" {
		return 0, fmt.Errorf("compiler: ad connector task %q needs an operation (%s)", st.Id, strings.Join(adOpNames(), ", "))
	}
	spec, ok := adOps[op]
	if !ok {
		return 0, fmt.Errorf("compiler: ad connector task %q has an unknown operation %q (want %s)", st.Id, cn.Operation, strings.Join(adOpNames(), ", "))
	}
	// Every operation but sync addresses an existing or to-be-created entry by dn —
	// including move, where dn is the entry being moved and newDN where it lands.
	// sync addresses a naming context instead, so it takes a baseDN.
	if spec.isSync {
		if strings.TrimSpace(cn.BaseDN) == "" {
			return 0, fmt.Errorf("compiler: ad connector task %q operation sync needs a baseDN (the naming context root the delta is read from)", st.Id)
		}
		if strings.TrimSpace(cn.CookieVariable) == "" {
			return 0, fmt.Errorf("compiler: ad connector task %q operation sync needs a cookieVariable; without one every pass re-reads the whole directory", st.Id)
		}
		if strings.TrimSpace(cn.ResultVariable) == "" {
			return 0, fmt.Errorf("compiler: ad connector task %q operation sync needs a resultVariable to receive the changes", st.Id)
		}
	} else if strings.TrimSpace(cn.DN) == "" {
		return 0, fmt.Errorf("compiler: ad connector task %q operation %q needs a dn", st.Id, op)
	}
	maxEntries, err := adSyncMaxEntries(st.Id, op, spec.isSync, cn.MaxEntries)
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
		CookieVar:      strings.TrimSpace(cn.CookieVariable),
		ResultVar:      strings.TrimSpace(cn.ResultVariable),
		MaxEntries:     maxEntries,
		ObjectSecurity: strings.EqualFold(strings.TrimSpace(cn.ObjectSecurity), "true"),
		Retries:        retries,
	}), nil
}

// adSyncMaxEntries reads the authored cap for one DirSync pass. It applies to sync
// alone; on any other operation it is an author believing something the connector
// will not do, reported rather than ignored.
func adSyncMaxEntries(taskID, op string, isSync bool, raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if isSync {
			return defaultAdSyncMaxEntries, nil
		}
		return 0, nil
	}
	if !isSync {
		return 0, fmt.Errorf("compiler: ad connector task %q sets maxEntries on operation %q, which returns no entries (maxEntries applies to sync)", taskID, op)
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

// ldapScopes maps the authored search scope names to their canonical form. An empty
// scope defaults to "sub" (whole subtree) at compile time.
var ldapScopes = map[string]bool{"base": true, "one": true, "sub": true}

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
		if !ldapScopes[scope] {
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

// compileWebScrapeConnectorTask compiles an <atlas:webscrapeConnector> task: it
// fetches the model-authored URL and extracts the elements matching a CSS selector
// via the job path (ADR-0118), not an external service-task worker. The URL and
// selector live in the model (like REST's endpoint, ADR-0067); the extracted values
// are written back into the required result variable as a JSON array.
func compileWebScrapeConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.WebScrape
	if strings.TrimSpace(cn.Url) == "" {
		return 0, fmt.Errorf("compiler: webscrape connector task %q needs a url", st.Id)
	}
	if strings.TrimSpace(cn.Selector) == "" {
		return 0, fmt.Errorf("compiler: webscrape connector task %q needs a selector", st.Id)
	}
	if strings.TrimSpace(cn.ResultVariable) == "" {
		return 0, fmt.Errorf("compiler: webscrape connector task %q needs a resultVariable", st.Id)
	}
	url, err := restValue(st.Id, "url", cn.Url)
	if err != nil {
		return 0, err
	}
	selector, err := restValue(st.Id, "selector", cn.Selector)
	if err != nil {
		return 0, err
	}
	return b.AddWebScrapeConnectorTask(WebScrapeConfig{
		Url:       url,
		Selector:  selector,
		Attribute: strings.TrimSpace(cn.Attribute),
		Result:    strings.TrimSpace(cn.ResultVariable),
		Retries:   retries,
	}), nil
}
