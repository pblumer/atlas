package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/ad"
)

// recordingDialer captures the connection an AD job asked for and hands back a
// connection that records the operation, so the worker path is assertable without a
// directory.
type recordingDialer struct {
	url, bindDN, bindPassword string
	startTLS                  bool
	conn                      *recordingConn
	err                       error
}

func (d *recordingDialer) Dial(url, bindDN, bindPassword string, startTLS bool) (ad.Conn, error) {
	d.url, d.bindDN, d.bindPassword, d.startTLS = url, bindDN, bindPassword, startTLS
	if d.err != nil {
		return nil, d.err
	}
	if d.conn == nil {
		d.conn = &recordingConn{}
	}
	return d.conn, nil
}

type recordingConn struct {
	addDN   string
	addAt   map[string][]string
	modDN   string
	mods    []ad.Mod
	delDN   string
	mdnDN   string
	syncReq *ad.DirSyncRequest
	closed  bool
}

func (c *recordingConn) Add(dn string, attrs map[string][]string) error {
	c.addDN, c.addAt = dn, attrs
	return nil
}
func (c *recordingConn) Modify(dn string, mods []ad.Mod) error {
	c.modDN, c.mods = dn, mods
	return nil
}
func (c *recordingConn) ReadAttr(string, string) ([]string, error) {
	return []string{"512"}, nil
}
func (c *recordingConn) ModifyDN(dn, _, _ string) error { c.mdnDN = dn; return nil }
func (c *recordingConn) Delete(dn string) error         { c.delDN = dn; return nil }
func (c *recordingConn) DirSync(req ad.DirSyncRequest) (ad.DirSyncResult, error) {
	c.syncReq = &req
	return ad.DirSyncResult{
		Entries: []ad.Entry{{DN: "cn=Arno,dc=x", Attributes: map[string][]string{"cn": {"Arno"}}}},
		Cookie:  []byte{0x01, 0x02},
		More:    true,
	}, nil
}
func (c *recordingConn) Close() error { c.closed = true; return nil }

// The AD kind registers a handler and needs no startup configuration — its server is
// model-authored and its bind password is a per-task reference, so unlike mail, SQL
// and Entra there is nothing a worker could validate before the first poll.
func TestBuiltinConnectorsRegistersADWithoutConfiguration(t *testing.T) {
	got, err := BuiltinConnectors(envMap(nil), "ad")
	if err != nil {
		t.Fatalf("--connector ad must not need configuration to start: %v", err)
	}
	if _, ok := got.Handlers[compiler.AdJobType]; !ok {
		t.Errorf("no handler for %s; have %v", compiler.AdJobType, got.Handlers)
	}
	// AD resolves no connector *names*, so it reports none to the Workers view.
	if len(got.Names) != 0 {
		t.Errorf("names = %v, want none for a model-authored server", got.Names)
	}
}

// The bind password is read from the worker's own environment under the same
// reference the model authored — offloading the kind moves the variable, not the
// model (ADR-0041/0168).
func TestADBindSecretComesFromTheWorkersEnvironment(t *testing.T) {
	dialer := &recordingDialer{}
	_, err := RunADJob(context.Background(), adJob(map[string]any{
		"url": "ldaps://dc.example.com:636", "bindDN": "cn=svc,dc=x",
		"bindSecretRef": "AD_BIND", "operation": "disable", "dn": "cn=Arno,dc=x",
	}), dialer, adSecretFromEnv(envMap(map[string]string{
		"ATLAS_CONNECTOR_AD_BIND_TOKEN": "s3cr3t",
	})))
	if err != nil {
		t.Fatalf("RunADJob: %v", err)
	}
	if dialer.bindPassword != "s3cr3t" {
		t.Errorf("bind password = %q, want the one resolved from this worker's env", dialer.bindPassword)
	}
	if dialer.url != "ldaps://dc.example.com:636" || dialer.bindDN != "cn=svc,dc=x" {
		t.Errorf("dial = %q / %q, want the model-authored server", dialer.url, dialer.bindDN)
	}
	if !dialer.conn.closed {
		t.Error("connection not closed")
	}
}

// A reference nothing answers to fails the job naming the variable, which is the same
// failure the in-process path gives.
func TestADUnresolvedBindSecret(t *testing.T) {
	_, err := RunADJob(context.Background(), adJob(map[string]any{
		"url": "ldaps://dc", "bindSecretRef": "AD_BIND", "operation": "disable", "dn": "cn=x",
	}), &recordingDialer{}, adSecretFromEnv(envMap(nil)))
	if err == nil {
		t.Fatal("an unresolved bind reference must fail the job")
	}
	if !strings.Contains(err.Error(), "AD_BIND") {
		t.Errorf("the error should name the reference, got: %v", err)
	}
}

// An anonymous bind authors no reference and needs none.
func TestADAnonymousBind(t *testing.T) {
	dialer := &recordingDialer{}
	if _, err := RunADJob(context.Background(), adJob(map[string]any{
		"url": "ldap://dc", "operation": "delete", "dn": "cn=Alt,dc=x",
	}), dialer, adSecretFromEnv(envMap(nil))); err != nil {
		t.Fatalf("RunADJob: %v", err)
	}
	if dialer.bindPassword != "" || dialer.conn.delDN != "cn=Alt,dc=x" {
		t.Errorf("dial = %q, delete = %q", dialer.bindPassword, dialer.conn.delDN)
	}
}

// Every field the engine puts on the payload must survive the round trip into an
// ad.Job. The two sides are written by hand in different packages, so this is what
// keeps a renamed key from silently dropping an operand — a create-user that arrived
// with no attributes would otherwise fail far from its cause.
func TestADResolvedDetailRoundTripsEveryField(t *testing.T) {
	full := ad.Job{
		URL: "ldaps://dc:636", BindDN: "cn=svc,dc=x", BindSecret: "AD_BIND", StartTLS: true,
		Operation: "create-user", DN: "cn=Arno,dc=x", MemberDN: "cn=g,dc=x",
		NewDN: "cn=Arno,ou=neu,dc=x", NewPassword: "N3w!pass",
		Attributes: map[string][]string{"sAMAccountName": {"arno"}},
	}
	// Marshal the way the engine's payload map is keyed, then read it back.
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{
		"url", "bindDN", "bindSecretRef", "startTLS", "operation", "dn",
		"memberDN", "newDN", "newPassword", "attributes",
	} {
		if _, ok := fields[key]; !ok {
			t.Errorf("ad.Job does not serialize %q; the engine's payload map keys it", key)
		}
	}
	dialer := &recordingDialer{}
	if _, err := RunADJob(context.Background(), adJob(fields), dialer, adSecretFromEnv(envMap(map[string]string{
		"ATLAS_CONNECTOR_AD_BIND_TOKEN": "s3cr3t",
	}))); err != nil {
		t.Fatalf("RunADJob: %v", err)
	}
	if !dialer.startTLS {
		t.Error("startTLS did not survive the round trip")
	}
	if dialer.conn.addAt["sAMAccountName"][0] != "arno" {
		t.Errorf("attributes did not survive the round trip: %v", dialer.conn.addAt)
	}
}

func TestRunADJobErrors(t *testing.T) {
	if _, err := RunADJob(context.Background(), Job{}, &recordingDialer{}, adSecretFromEnv(envMap(nil))); err == nil {
		t.Error("a job with no connector detail must fail")
	}
	// A dial that fails, fails the job.
	_, err := RunADJob(context.Background(), adJob(map[string]any{
		"url": "ldaps://dc", "operation": "delete", "dn": "cn=x",
	}), &recordingDialer{err: errors.New("no route to host")}, adSecretFromEnv(envMap(nil)))
	if err == nil {
		t.Error("a dial failure must fail the job")
	}
	// An empty url is refused before any dial.
	if _, err := RunADJob(context.Background(), adJob(map[string]any{
		"operation": "delete", "dn": "cn=x",
	}), &recordingDialer{}, adSecretFromEnv(envMap(nil))); err == nil {
		t.Error("an empty url must fail the job")
	}
}

// adJob wraps resolved fields the way a leased job carries them.
func adJob(fields map[string]any) Job {
	return Job{Connector: &ConnectorPayload{Kind: "ad", Fields: fields}}
}

// A sync job round-trips through the payload and comes back with what the worker must
// hand the engine: the changes, and the cookie the next pass presents.
func TestRunADJobSync(t *testing.T) {
	dialer := &recordingDialer{}
	out, err := RunADJob(context.Background(), adJob(map[string]any{
		"url": "ldaps://dc", "operation": "sync", "baseDN": "dc=x",
		"cookieVariable": "cookie", "resultVariable": "aenderungen",
		"cookie": base64.StdEncoding.EncodeToString([]byte{0xBE, 0xEF}),
	}), dialer, adSecretFromEnv(envMap(nil)))
	if err != nil {
		t.Fatalf("RunADJob: %v", err)
	}
	if dialer.conn.syncReq == nil {
		t.Fatal("no DirSync was performed")
	}
	if string(dialer.conn.syncReq.Cookie) != string([]byte{0xBE, 0xEF}) {
		t.Errorf("cookie sent = %v, want the decoded one from the payload", dialer.conn.syncReq.Cookie)
	}
	// The new cookie replaces the old one in the same variable, which is what lets a
	// reconciliation loop carry its own position forward.
	if got := out["cookie"]; got != base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}) {
		t.Errorf("cookie variable = %v, want the server's new one", got)
	}
	res, ok := out["aenderungen"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v", out["aenderungen"])
	}
	if res["more"] != true {
		t.Errorf("more = %v, want the server's signal passed through", res["more"])
	}
}
