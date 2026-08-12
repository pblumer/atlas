package compiler

import (
	"fmt"
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
	// compile validates the extension's fields and adds the connector-task node,
	// returning its node id. It is called only when present(st) is true, so it may
	// dereference the extension pointer directly.
	compile func(b *Builder, st xmlServiceTask, retries int32) (int32, error)
}

// connectorCompilers is the ordered registry of connector flavors a job-worker task
// can take. registerJobWorkerTask consults it before falling through to the plain
// external-worker task.
var connectorCompilers = []connectorCompiler{
	{present: func(st xmlServiceTask) bool { return st.Clio != nil }, compile: compileClioConnectorTask},
	{present: func(st xmlServiceTask) bool { return st.Rest != nil }, compile: compileRestConnectorTask},
	{present: func(st xmlServiceTask) bool { return st.Mail != nil }, compile: compileMailConnectorTask},
	{present: func(st xmlServiceTask) bool { return st.SharePoint != nil }, compile: compileSharePointConnectorTask},
	{present: func(st xmlServiceTask) bool { return st.Remedy != nil }, compile: compileRemedyConnectorTask},
	{present: func(st xmlServiceTask) bool { return st.WebScrape != nil }, compile: compileWebScrapeConnectorTask},
}

// serviceTaskRetries reads the retries count from a job-worker task's
// <taskDefinition>, defaulting to defaultRetries when it is omitted. label names the
// element ("service task"/"send task") for diagnostics.
func serviceTaskRetries(st xmlServiceTask, label string) (int32, error) {
	retries := int32(defaultRetries)
	if r := st.TaskDefinition.Retries; r != "" {
		n, err := strconv.Atoi(r)
		if err != nil {
			return 0, fmt.Errorf("compiler: %s %q has invalid retries %q: %w", label, st.Id, r, err)
		}
		retries = int32(n)
	}
	return retries, nil
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
// name, like clio; only the message (recipients, subject, body) lives in the model.
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
	return b.AddMailConnectorTask(MailConfig{
		Connector: strings.TrimSpace(cn.Connector),
		To:        to,
		Cc:        cc,
		Bcc:       bcc,
		From:      from,
		Subject:   subject,
		Body:      body,
		Retries:   retries,
	}), nil
}

// compileSharePointConnectorTask compiles an <atlas:sharepointConnector> task: it
// creates a list item in a model-authored site/list through a server-registered
// SharePoint provider (Microsoft Graph) via the job path (ADR-0105). The provider
// (Graph base, OAuth credential) is resolved server-side by connector name, like mail;
// only the target (site, list, item fields) lives in the model.
func compileSharePointConnectorTask(b *Builder, st xmlServiceTask, retries int32) (int32, error) {
	cn := st.SharePoint
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
// via the job path (ADR-0117), not an external service-task worker. The URL and
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
