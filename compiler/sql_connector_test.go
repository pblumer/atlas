package compiler

import (
	"fmt"
	"strings"
	"testing"
)

// The three SQL worker products, and what a task of each compiles to. They share a
// shape exactly, so every table-driven case below runs against all three: a rule that
// held for only one of them would be the drift this table exists to prevent.
var sqlProductCases = []struct {
	elem     string // the extension element name
	kind     string // how a compiler error names it
	jobType  string
	jobIndex int32
}{
	{"mssqlConnector", "mssql", MsSqlJobType, MsSqlJobTypeIndex},
	{"mariadbConnector", "mariadb", MariaDBJobType, MariaDBJobTypeIndex},
	{"postgresConnector", "postgres", PostgresJobType, PostgresJobTypeIndex},
}

// sqlTaskBPMN builds a one-task model from a raw worker extension element, so each
// case states only the attributes it is about.
func sqlTaskBPMN(ext string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>` + ext + `</bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

// sqlDetail parses a one-task model and returns the task's worker detail.
func sqlDetail(t *testing.T, ext string) (*CompiledProcess, *ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(sqlTaskBPMN(ext)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	return cp, cp.ConnectorTask(node.Detail)
}

// A service task bearing one of the three SQL worker extensions is a worker
// task of that product (ADR-0173): it runs one statement against a database the
// *worker* is configured for, so the model names a worker and never an address.
func TestParseSqlConnectorTask(t *testing.T) {
	for _, p := range sqlProductCases {
		t.Run(p.elem, func(t *testing.T) {
			cp, d := sqlDetail(t, fmt.Sprintf(
				`<atlas:%s connector="hr-db" operation="query"
                   statement="SELECT id, mail FROM personen WHERE abteilung = @p1"
                   parametersVariable="params" resultVariable="zeilen" maxRows="500"/>`, p.elem))
			if got := cp.Intern(d.JobType); got != p.jobType {
				t.Errorf("jobType = %q, want %q", got, p.jobType)
			}
			if d.JobType != p.jobIndex {
				t.Errorf("jobType index = %d, want the reserved %d", d.JobType, p.jobIndex)
			}
			if got := cp.Intern(d.Connector); got != "hr-db" {
				t.Errorf("worker = %q, want hr-db", got)
			}
			if got := cp.Intern(d.SqlOp); got != "query" {
				t.Errorf("operation = %q, want query", got)
			}
			if got := cp.Intern(d.SqlStatement); got != "SELECT id, mail FROM personen WHERE abteilung = @p1" {
				t.Errorf("statement = %q", got)
			}
			if got := cp.Intern(d.SqlParamsVar); got != "params" {
				t.Errorf("parametersVariable = %q, want params", got)
			}
			if got := cp.Intern(d.ResultVar); got != "zeilen" {
				t.Errorf("resultVariable = %q, want zeilen", got)
			}
			if d.SqlMaxRows != 500 {
				t.Errorf("maxRows = %d, want 500", d.SqlMaxRows)
			}
			// A SQL task authors no HTTP method and no inline auth: the DSN lives on
			// the worker, so there is not even a secret reference to carry.
			if cp.Intern(d.Method) != "" || d.Auth != -1 {
				t.Errorf("method/auth = %q/%d, want empty/-1", cp.Intern(d.Method), d.Auth)
			}
		})
	}
}

// The statement is literal by construction: a FEEL statement would let a process
// variable become part of the SQL text, so the compiler refuses one outright rather
// than trusting an author to quote (ADR-0173).
func TestSqlConnectorRejectsFeelStatement(t *testing.T) {
	for _, p := range sqlProductCases {
		t.Run(p.elem, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(sqlTaskBPMN(fmt.Sprintf(
				`<atlas:%s connector="db" operation="query" statement="=&quot;SELECT * FROM &quot; + tabelle" resultVariable="r"/>`, p.elem))))
			if err == nil {
				t.Fatal("a FEEL statement must not compile")
			}
			if !strings.Contains(err.Error(), "parametersVariable") {
				t.Errorf("the error should point at parametersVariable as the way to pass data, got: %v", err)
			}
			if !strings.Contains(err.Error(), p.kind) {
				t.Errorf("the error should name the product %q, got: %v", p.kind, err)
			}
		})
	}
}

func TestSqlConnectorValidation(t *testing.T) {
	for _, tc := range []struct{ name, attrs, want string }{
		{"no worker", `operation="query" statement="SELECT 1" resultVariable="r"`, "worker"},
		{"no operation", `connector="db" statement="SELECT 1" resultVariable="r"`, "operation"},
		{"unknown operation", `connector="db" operation="drop" statement="SELECT 1" resultVariable="r"`, "unknown operation"},
		{"no statement", `connector="db" operation="query" resultVariable="r"`, "statement"},
		{"query without result", `connector="db" operation="query" statement="SELECT 1"`, "resultVariable"},
		{"query-one without result", `connector="db" operation="query-one" statement="SELECT 1"`, "resultVariable"},
		{"maxRows on execute", `connector="db" operation="execute" statement="DELETE FROM t" maxRows="10"`, "maxRows"},
		{"maxRows on query-one", `connector="db" operation="query-one" statement="SELECT 1" resultVariable="r" maxRows="10"`, "maxRows"},
		{"maxRows not a number", `connector="db" operation="query" statement="SELECT 1" resultVariable="r" maxRows="viele"`, "maxRows"},
		{"maxRows negative", `connector="db" operation="query" statement="SELECT 1" resultVariable="r" maxRows="-1"`, "maxRows"},
	} {
		for _, p := range sqlProductCases {
			t.Run(tc.name+"/"+p.elem, func(t *testing.T) {
				_, err := Parse(1, 1, strings.NewReader(sqlTaskBPMN(
					fmt.Sprintf(`<atlas:%s %s/>`, p.elem, tc.attrs))))
				if err == nil {
					t.Fatalf("want an error mentioning %q, got none", tc.want)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("error = %v, want it to mention %q", err, tc.want)
				}
			})
		}
	}
}

// execute needs no result variable — the affected-row count is optional — and
// query-one needs no maxRows, because "one row" is the cap.
func TestSqlConnectorExecuteAndQueryOne(t *testing.T) {
	for _, p := range sqlProductCases {
		t.Run(p.elem, func(t *testing.T) {
			cp, d := sqlDetail(t, fmt.Sprintf(
				`<atlas:%s connector="db" operation="execute" statement="DELETE FROM sperren WHERE id = ?" parametersVariable="p"/>`, p.elem))
			if got := cp.Intern(d.SqlOp); got != "execute" {
				t.Errorf("operation = %q, want execute", got)
			}
			if d.ResultVar != -1 {
				t.Errorf("resultVar = %d, want -1 when none is authored", d.ResultVar)
			}

			cp2, d2 := sqlDetail(t, fmt.Sprintf(
				`<atlas:%s connector="db" operation="query-one" statement="SELECT * FROM p WHERE id = ?" resultVariable="person"/>`, p.elem))
			if got := cp2.Intern(d2.SqlOp); got != "query-one" {
				t.Errorf("operation = %q, want query-one", got)
			}
			if d2.SqlMaxRows != 0 {
				t.Errorf("maxRows = %d, want 0 (unset)", d2.SqlMaxRows)
			}
		})
	}
}

// The reserved indices are baked into jobs already on disk, so they may never move.
func TestSqlJobTypeIndicesAreReserved(t *testing.T) {
	for _, p := range sqlProductCases {
		if got := reservedJobTypes[p.jobIndex]; got != p.jobType {
			t.Errorf("reservedJobTypes[%d] = %q, want %q", p.jobIndex, got, p.jobType)
		}
	}
	if MsSqlJobTypeIndex != 20 || MariaDBJobTypeIndex != 21 || PostgresJobTypeIndex != 22 {
		t.Errorf("reserved indices moved: %d/%d/%d, want 20/21/22",
			MsSqlJobTypeIndex, MariaDBJobTypeIndex, PostgresJobTypeIndex)
	}
}
