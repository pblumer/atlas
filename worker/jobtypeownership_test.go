package worker

import (
	"maps"
	"slices"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// Which job types a kind serves is decided by hand, one
// `built.Handlers[compiler.XJobType] = ...` per arm of BuiltinConnectors' switch, and
// nothing held the key to the arm it sits in.
//
// A copy-paste that leaves `compiler.SharePointJobType` in the googlesheets arm
// compiles — the registry beside it is typed, so the *handler* cannot be wrong, but the
// map key is a plain string and any job type is a legal one. It passes the kind↔switch
// drift test (the arm is still `case "googlesheets"`), the sortedness test, and the
// acceptance test (the kind is still implemented). What it produces is a worker that
// leases SharePoint jobs it will fail and Google Sheets tasks that park forever, and
// the only symptom is work not happening.
//
// So the ownership is stated here, independently, kind by kind. The table is the point:
// it is a second opinion about what each kind serves, written where a reviewer reads it
// rather than derived from the code it is checking.

// connectorJobTypes is what each Worker Type serves. A kind missing from this table
// fails the coverage check below rather than being skipped, so adding a kind means
// saying which job types are its own.
var connectorJobTypes = map[string][]string{
	"ad": {compiler.AdJobType},
	// Two job types, one kind: the container's round and the ai service task's single call
	// (ADR-0256).
	"agent":        {compiler.AgentJobType, compiler.AiTaskJobType},
	"clio":         {compiler.ClioWriteJobType, compiler.ClioQueryJobType, compiler.ClioReadJobType},
	"csv":          {compiler.CsvImportJobType},
	"discord":      {compiler.DiscordJobType},
	"entra":        {compiler.EntraJobType},
	"googlesheets": {compiler.GoogleSheetsJobType},
	"jira":         {compiler.JiraJobType},
	"ldap":         {compiler.LdapJobType},
	"ldif":         {compiler.LdifJobType},
	"mail":         {compiler.MailJobType},
	"mariadb":      {compiler.MariaDBJobType},
	"mssql":        {compiler.MsSqlJobType},
	"postgres":     {compiler.PostgresJobType},
	"remedy":       {compiler.RemedyJobType},
	"rest":         {compiler.RestJobType},
	"scim":         {compiler.ScimJobType},
	"script":       {compiler.PwshJobType, compiler.PythonJobType, compiler.JsJobType},
	"sharepoint":   {compiler.SharePointJobType},
	"soap":         {compiler.SoapJobType},
	"temis":        {compiler.TemisDecisionJobType},
	"webscrape":    {compiler.WebScrapeJobType},
}

// configuredEnvFor is the smallest environment that makes a kind register rather than
// report itself unconfigured. A credential-free kind needs nothing; a credential-bearing
// one needs enough to build a registry, and no more — none of these is dialled.
func configuredEnvFor(t *testing.T, kind string) map[string]string {
	t.Helper()
	switch kind {
	case "ad":
		return map[string]string{
			"ATLAS_AD_CONNECTORS":    "prod",
			"ATLAS_AD_PROD_URL":      "ldaps://dc.example.com:636",
			"ATLAS_AD_PROD_BIND_DN":  "cn=svc,dc=example,dc=com",
			"ATLAS_AD_PROD_PASSWORD": "pw",
		}
	case "agent":
		return map[string]string{
			"ATLAS_AGENT_CONNECTORS":           "anthropic_pb",
			"ATLAS_AGENT_ANTHROPIC_PB_API_KEY": "sk-test",
		}
	case "clio":
		return map[string]string{
			"ATLAS_CLIO_CONNECTORS":      "events",
			"ATLAS_CLIO_EVENTS_ENDPOINT": "https://events.example.com",
		}
	case "discord":
		return map[string]string{
			"ATLAS_DISCORD_CONNECTORS": "team",
			"ATLAS_DISCORD_TEAM_TOKEN": "bot-t0ken",
		}
	case "entra":
		return map[string]string{
			"ATLAS_ENTRA_CONNECTORS":            "contoso",
			"ATLAS_ENTRA_CONTOSO_TENANT_ID":     "11111111-2222-3333-4444-555555555555",
			"ATLAS_ENTRA_CONTOSO_CLIENT_ID":     "app-1",
			"ATLAS_ENTRA_CONTOSO_CLIENT_SECRET": "s3cr3t",
		}
	case "googlesheets":
		return map[string]string{
			"ATLAS_GOOGLESHEETS_CONNECTORS":       "acme",
			"ATLAS_GOOGLESHEETS_ACME_CREDENTIALS": sheetsBundle(t),
		}
	case "jira":
		return map[string]string{
			"ATLAS_JIRA_CONNECTORS":     "acme",
			"ATLAS_JIRA_ACME_URL":       "https://acme.atlassian.net",
			"ATLAS_JIRA_ACME_EMAIL":     "bot@acme.example",
			"ATLAS_JIRA_ACME_API_TOKEN": "t0ken",
		}
	case "mail":
		return map[string]string{
			"ATLAS_MAIL_CONNECTORS":         "office365",
			"ATLAS_MAIL_OFFICE365_ENDPOINT": "smtp.example.com:587",
			"ATLAS_MAIL_OFFICE365_FROM":     "noreply@example.com",
			"ATLAS_MAIL_OFFICE365_USERNAME": "atlas",
			"ATLAS_MAIL_OFFICE365_PASSWORD": "hunter2",
		}
	case "mariadb":
		return map[string]string{
			"ATLAS_MARIADB_CONNECTORS": "hr-db",
			"ATLAS_MARIADB_HR_DB_DSN":  "u:p@tcp(127.0.0.1:1)/hr",
		}
	case "mssql":
		return map[string]string{
			"ATLAS_MSSQL_CONNECTORS": "hr-db",
			"ATLAS_MSSQL_HR_DB_DSN":  "sqlserver://u:p@127.0.0.1:1/?database=hr",
		}
	case "postgres":
		return map[string]string{
			"ATLAS_POSTGRES_CONNECTORS": "hr-db",
			"ATLAS_POSTGRES_HR_DB_DSN":  "postgres://u:p@127.0.0.1:1/hr",
		}
	case "remedy":
		return map[string]string{
			"ATLAS_REMEDY_CONNECTORS":     "helix",
			"ATLAS_REMEDY_HELIX_ENDPOINT": "https://helix.example.com:8008",
			"ATLAS_REMEDY_HELIX_USERNAME": "atlas-svc",
			"ATLAS_REMEDY_HELIX_PASSWORD": "hunter2",
		}
	case "sharepoint":
		return map[string]string{
			"ATLAS_SHAREPOINT_CONNECTORS":           "intranet",
			"ATLAS_SHAREPOINT_INTRANET_CREDENTIALS": `{"method":"clientCredentials","tenantId":"t-1","clientId":"c-1","clientSecret":"s3cr3t"}`,
		}
	case "temis":
		return map[string]string{
			"ATLAS_TEMIS_CONNECTORS":  "rules",
			"ATLAS_TEMIS_RULES_URL":   "https://rules.example.com",
			"ATLAS_TEMIS_RULES_TOKEN": "s3cr3t",
		}
	default:
		// csv, ldif, ldap, webscrape, script, rest, scim, soap: what these workers
		// contribute is reach or an interpreter, not a credential.
		return nil
	}
}

// Every kind registers exactly the job types the table says are its own.
func TestEveryKindServesItsOwnJobTypes(t *testing.T) {
	for kind, want := range connectorJobTypes {
		t.Run(kind, func(t *testing.T) {
			built, err := BuiltinConnectors(envMap(configuredEnvFor(t, kind)), kind)
			if err != nil {
				t.Fatalf("BuiltinConnectors(%q): %v", kind, err)
			}
			if len(built.Unconfigured) != 0 {
				t.Fatalf("%q reported itself unconfigured (%v); configuredEnvFor no longer "+
					"gives it enough to register, so this test would pass on a kind serving nothing",
					kind, built.Unconfigured)
			}
			got := slices.Sorted(maps.Keys(built.Handlers))
			if !slices.Equal(got, slices.Sorted(slices.Values(want))) {
				t.Errorf("%q serves %v, want %v — a job type in the wrong arm of the switch means "+
					"work leased by a kind that cannot run it, and work nobody leases at all", kind, got, want)
			}
		})
	}
}

// No job type is owned by two kinds. The check above compares each kind against its own
// row, so a job type duplicated *into* a second row passes it twice; this is what says
// the rows are a partition rather than a list.
func TestNoJobTypeIsServedByTwoKinds(t *testing.T) {
	owner := map[string]string{}
	for _, kind := range slices.Sorted(maps.Keys(connectorJobTypes)) {
		for _, jt := range connectorJobTypes[kind] {
			if first, taken := owner[jt]; taken {
				t.Errorf("job type %q is claimed by both %q and %q; a worker asked for both "+
					"registers one handler where two were meant, and which one wins is map order", jt, first, kind)
				continue
			}
			owner[jt] = kind
		}
	}
}

// The table covers every kind BuiltinConnectors serves. Without this a kind added to the
// switch is simply absent here, and the guard silently stops covering it.
func TestEveryKnownKindHasStatedJobTypes(t *testing.T) {
	for _, kind := range KnownConnectorKinds() {
		if _, stated := connectorJobTypes[kind]; !stated {
			t.Errorf("connectorJobTypes has no row for %q: say which job types it serves, "+
				"or nothing checks that it registers under its own", kind)
		}
	}
	for kind := range connectorJobTypes {
		if !slices.Contains(KnownConnectorKinds(), kind) {
			t.Errorf("connectorJobTypes has a row for %q, which is not a known kind", kind)
		}
	}
}
