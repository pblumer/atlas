package worker_test

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
	"github.com/pblumer/atlas/limits"
	"github.com/pblumer/atlas/worker"
)

const adDocumentModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="d" targetNamespace="http://atlas/test">
  <bpmn:process id="adexport" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="lesen">
      <bpmn:extensionElements>
        <atlas:adConnector url="ldaps://dc.example.com:636" operation="search" baseDN="ou=users,dc=x"
          filter="(objectClass=user)" select="sAMAccountName,mail,memberOf"
          resultAs="document" documentName="benutzer.csv" resultVariable="benutzer"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="lesen"/>
    <bpmn:sequenceFlow id="f2" sourceRef="lesen" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// A Worker Instance reading a directory writes the search as a document through its
// job's lease, and the instance completes with a reference to it rather than with
// the entries (docs/planning/ad-search-as-document.md).
func TestAWorkerWritesADirectorySearchAsADocument(t *testing.T) {
	ts := bareAtlas(t, adDocumentModel, api.WithOffloadedConnectorKinds([]string{"ad"}))
	post(t, ts, "/api/v1/processes/1/instances", `{}`, "application/json")

	var seed []ad.Entry
	for i := 1; i <= 750; i++ {
		seed = append(seed, ad.Entry{DN: fmt.Sprintf("cn=u%04d,ou=users,dc=x", i), Attributes: map[string][]string{
			"objectClass":    {"user"},
			"sAMAccountName": {fmt.Sprintf("u%04d", i)},
			"mail":           {fmt.Sprintf("u%04d@x.ch", i)},
			"memberOf":       {"CN=Alle,DC=x", "CN=Team,DC=x"},
		}})
	}
	dir := ad.NewMockDirectory(seed...)
	var handlerErr error
	w := worker.New(worker.Options{Server: ts.URL, ID: "ad-1", Handlers: map[string]worker.Exec{
		compiler.AdJobType: worker.ExecFunc(func(ctx context.Context, j worker.Job) (map[string]any, error) {
			out, err := worker.RunADJob(ctx, j, dir, func(string) string { return "" }, nil)
			handlerErr = err
			return out, err
		}),
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if handlerErr != nil {
		t.Fatalf("the worker's handler failed: %v", handlerErr)
	}
	if running := runningInstances(t, ts); running != 0 {
		t.Fatalf("%d instances still running", running)
	}
	res, _ := instanceVariables(t, ts)["benutzer"].(map[string]any)
	if res["count"] != 750.0 || res["found"] != true || res["dn"] != "cn=u0001,ou=users,dc=x" {
		t.Fatalf("benutzer = %v", res)
	}
	doc, _ := res["document"].(map[string]any)
	id, _ := doc["$document"].(string)
	resp, err := http.Get(ts.URL + "/api/v1/documents/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || resp.StatusCode != http.StatusOK || len(rows) != 751 || strings.Join(rows[0], ",") != "dn,sAMAccountName,mail,memberOf" {
		t.Fatalf("document = %d, %d rows (%v), header %v", resp.StatusCode, len(rows), err, rows)
	}
	if got := strings.Join(rows[1], ";"); got != "cn=u0001,ou=users,dc=x;u0001;u0001@x.ch;CN=Alle,DC=x|CN=Team,DC=x" {
		t.Fatalf("first row = %q", got)
	}
}

// A Worker Instance holds the search to the document limit of the server that handed
// the job out, not to a default of its own: on a server that allows 4,000 bytes, a
// search of 750 accounts fails on the worker with that number, before any upload, and
// the job does not complete.
func TestAWorkerHoldsADirectorySearchToTheServersDocumentLimit(t *testing.T) {
	l := limits.Default()
	l.Document = 4000
	ts := bareAtlas(t, adDocumentModel, api.WithOffloadedConnectorKinds([]string{"ad"}), api.WithLimits(l))
	post(t, ts, "/api/v1/processes/1/instances", `{}`, "application/json")

	var seed []ad.Entry
	for i := 1; i <= 750; i++ {
		seed = append(seed, ad.Entry{DN: fmt.Sprintf("cn=u%04d,ou=users,dc=x", i), Attributes: map[string][]string{
			"objectClass": {"user"}, "sAMAccountName": {fmt.Sprintf("u%04d", i)},
		}})
	}
	dir := ad.NewMockDirectory(seed...)
	var handlerErr error
	w := worker.New(worker.Options{Server: ts.URL, ID: "ad-1", Handlers: map[string]worker.Exec{
		compiler.AdJobType: worker.ExecFunc(func(ctx context.Context, j worker.Job) (map[string]any, error) {
			out, err := worker.RunADJob(ctx, j, dir, func(string) string { return "" }, nil)
			handlerErr = err
			return out, err
		}),
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// The server refuses such an upload in the same words; that the search stopped on
	// the worker, before any upload, is what "after … entries" and the absence of the
	// store's error show.
	if handlerErr == nil || !strings.Contains(handlerErr.Error(), "4000 bytes a document may be (ATLAS_LIMIT_DOCUMENT) after") ||
		strings.Contains(handlerErr.Error(), "store the search result") {
		t.Fatalf("the worker's handler = %v; want the search stopped at the server's limit of 4000 bytes, before any upload", handlerErr)
	}
	if running := runningInstances(t, ts); running != 1 {
		t.Fatalf("%d instances running; the job must not have completed", running)
	}
}
