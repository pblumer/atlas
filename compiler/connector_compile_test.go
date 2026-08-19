package compiler

import "testing"

// TestConnectorCompilersRegistry pins the connector-flavor registry that
// registerJobWorkerTask iterates: every entry is fully populated, and each present
// predicate fires for exactly its own extension so a job-worker task compiles to the
// intended connector flavor.
func TestConnectorCompilersRegistry(t *testing.T) {
	if len(connectorCompilers) == 0 {
		t.Fatal("connectorCompilers is empty")
	}
	for i, cc := range connectorCompilers {
		if cc.present == nil || cc.compile == nil {
			t.Errorf("connectorCompilers[%d] is incompletely defined", i)
		}
	}
	// Each present predicate is discriminating: it fires for its own extension and no
	// other. The slice index lines up with the extension set on the task below.
	cases := []xmlServiceTask{
		{Clio: &xmlClioConnector{}},
		{Rest: &xmlRestConnector{}},
		{Mail: &xmlMailConnector{}},
		{User: &xmlUserConnector{}},
		{SharePoint: &xmlSharePointConnector{}},
		{Remedy: &xmlRemedyConnector{}},
		{WebScrape: &xmlWebScrapeConnector{}},
		{Scim: &xmlScimConnector{}},
	}
	if len(cases) != len(connectorCompilers) {
		t.Fatalf("cases = %d, connectorCompilers = %d; keep them in step", len(cases), len(connectorCompilers))
	}
	for i, st := range cases {
		for j, cc := range connectorCompilers {
			got := cc.present(st)
			if want := i == j; got != want {
				t.Errorf("connectorCompilers[%d].present(cases[%d]) = %v, want %v", j, i, got, want)
			}
		}
	}
	// A plain service task (no extension) matches no connector flavor, so it falls
	// through to the plain external-worker path.
	for j, cc := range connectorCompilers {
		if cc.present(xmlServiceTask{}) {
			t.Errorf("connectorCompilers[%d].present matched a plain service task", j)
		}
	}
}
