package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

// The LDIF kind needs no configuration and serves a pure transform, so a worker can
// take it without holding anything.
func TestBuiltinConnectorsRegistersLdif(t *testing.T) {
	got, err := BuiltinConnectors(envMap(nil), "ldif")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := got.Handlers[compiler.LdifJobType]; !ok {
		t.Errorf("no handler for %s; have %v", compiler.LdifJobType, got.Handlers)
	}
}

// A resolved directory-file job round-trips through the payload and runs the same
// transform the engine would.
func TestRunLdifJob(t *testing.T) {
	out, err := runLdif(context.Background(), Job{Connector: &ConnectorPayload{
		Kind: "ldif",
		Fields: map[string]any{
			"format": "ldif", "source": "dn: uid=ada,dc=x\ncn: Ada\n", "resultVariable": "eintraege",
		},
	}})
	if err != nil {
		t.Fatalf("runLdif: %v", err)
	}
	if out["entryCount"] != 1 {
		t.Errorf("entryCount = %v", out["entryCount"])
	}
	if _, ok := out["eintraege"]; !ok {
		t.Errorf("result = %#v, want the entries", out)
	}
}

func TestRunLdifJobErrors(t *testing.T) {
	if _, err := runLdif(context.Background(), Job{}); err == nil {
		t.Error("a job with no connector detail must fail")
	}
	if _, err := runLdif(context.Background(), Job{Connector: &ConnectorPayload{
		Kind: "ldif", Fields: map[string]any{"format": "ldif", "source": "kaputt", "resultVariable": "r"},
	}}); err == nil {
		t.Error("an unparseable file must fail the job")
	}
}

// Mock mode (ADR-0181). A worker told to serve AD without a
// domain controller serves it against a directory in its own memory, so an identity
// process can be run end to end before anybody is allowed near the real forest.

// Without the switch nothing changes: the worker dials a real directory, which is the
// only default a connector may have.
func TestADWithoutMockModeDialsARealDirectory(t *testing.T) {
	dialer, mock, err := adDialerFromEnv(envMap(nil))
	if err != nil {
		t.Fatalf("adDialerFromEnv: %v", err)
	}
	if mock != nil {
		t.Fatal("mock mode is on without ATLAS_AD_MOCK; it must be asked for")
	}
	if _, ok := dialer.(ad.GoDialer); !ok {
		t.Errorf("dialer = %T, want the production one", dialer)
	}
}

// With the switch the worker performs the operation against its own memory: no
// dial, no directory, and the entry is there afterwards to prove the job really ran.
func TestADMockModeServesAJobWithoutADirectory(t *testing.T) {
	dialer, mock, err := adDialerFromEnv(envMap(map[string]string{"ATLAS_AD_MOCK": "1"}))
	if err != nil {
		t.Fatalf("adDialerFromEnv: %v", err)
	}
	if mock == nil {
		t.Fatal("ATLAS_AD_MOCK=1 did not put the connector into mock mode")
	}
	if _, err := RunADJob(context.Background(), adJob(map[string]any{
		"url": "ldaps://dc.example.com:636", "operation": "create-user",
		"dn":         "cn=Arno,ou=users,dc=example,dc=com",
		"attributes": map[string]any{"sAMAccountName": []any{"arno"}},
	}), dialer, adSecretFromEnv(envMap(nil))); err != nil {
		t.Fatalf("RunADJob in mock mode: %v", err)
	}
	entries := mock.Entries()
	if len(entries) != 1 || !strings.EqualFold(entries[0].DN, "cn=Arno,ou=users,dc=example,dc=com") {
		t.Fatalf("entries = %v, want the created account", entries)
	}
	if len(mock.Operations()) == 0 {
		t.Error("the operation journal is empty; a mockup run must leave what it did behind")
	}
}

// A seed file is how a mock directory holds the accounts a process expects to find:
// a leaver has nothing to disable in an empty forest. It is read with the same
// parser the directory-file connector uses, so LDIF means one thing in Atlas.
func TestADMockModeSeedsFromAnLDIFFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forest.ldif")
	if err := os.WriteFile(path, []byte("dn: cn=Arno,ou=users,dc=example,dc=com\ncn: Arno\nuserAccountControl: 512\n"), 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	dialer, mock, err := adDialerFromEnv(envMap(map[string]string{
		"ATLAS_AD_MOCK": "true", "ATLAS_AD_MOCK_SEED": path,
	}))
	if err != nil {
		t.Fatalf("adDialerFromEnv: %v", err)
	}
	if len(mock.Entries()) != 1 {
		t.Fatalf("entries = %v, want the seeded account", mock.Entries())
	}
	// The seeded account can be disabled, which is what a leaver process does first.
	if _, err := RunADJob(context.Background(), adJob(map[string]any{
		"url": "ldaps://dc", "operation": "disable", "dn": "cn=Arno,ou=users,dc=example,dc=com",
	}), dialer, adSecretFromEnv(envMap(nil))); err != nil {
		t.Fatalf("disable against the seeded directory: %v", err)
	}
	if got := mock.Entries()[0].Attributes["userAccountControl"]; got[0] != "514" {
		t.Errorf("userAccountControl = %v, want the disabled account", got)
	}
}

// A seed the worker cannot read or parse is refused at startup, where the operator is
// still watching, rather than discovered a retry budget later.
func TestADMockSeedFailuresAreRefusedAtStartup(t *testing.T) {
	_, _, err := adDialerFromEnv(envMap(map[string]string{
		"ATLAS_AD_MOCK": "1", "ATLAS_AD_MOCK_SEED": filepath.Join(t.TempDir(), "nope.ldif"),
	}))
	if err == nil || !strings.Contains(err.Error(), "nope.ldif") {
		t.Errorf("error = %v, want it to name the seed file that is not there", err)
	}
	path := filepath.Join(t.TempDir(), "broken.ldif")
	if err := os.WriteFile(path, []byte("kaputt\n"), 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	if _, _, err := adDialerFromEnv(envMap(map[string]string{
		"ATLAS_AD_MOCK": "1", "ATLAS_AD_MOCK_SEED": path,
	})); err == nil {
		t.Error("a seed file that does not parse was accepted")
	}
	// A seed named without mock mode is a mistake worth reporting: the file would be
	// read into a directory nothing ever reaches.
	if _, _, err := adDialerFromEnv(envMap(map[string]string{"ATLAS_AD_MOCK_SEED": path})); err == nil {
		t.Error("a seed without ATLAS_AD_MOCK was accepted")
	}
}

// The switch is a yes/no, and a value that is neither is refused rather than read as
// "no" — "ATLAS_AD_MOCK=maybe" silently dialling the real directory is the outcome
// this exists to prevent.
func TestADMockSwitchIsRefusedWhenItIsNotAYesOrNo(t *testing.T) {
	_, _, err := adDialerFromEnv(envMap(map[string]string{"ATLAS_AD_MOCK": "vielleicht"}))
	if err == nil || !strings.Contains(err.Error(), "ATLAS_AD_MOCK") {
		t.Errorf("error = %v, want it to name the variable it cannot read", err)
	}
	for _, off := range []string{"0", "false", "no", "off", ""} {
		_, mock, err := adDialerFromEnv(envMap(map[string]string{"ATLAS_AD_MOCK": off}))
		if err != nil || mock != nil {
			t.Errorf("ATLAS_AD_MOCK=%q: mock = %v, err = %v; want mock mode off", off, mock != nil, err)
		}
	}
}

// A DSML seed works too, because the directory-file connector reads both and a mock
// directory should not be the one place in Atlas where LDIF is the only file format.
func TestADMockModeSeedsFromDSML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forest.dsml")
	const doc = `<dsml><directory-entries><entry dn="cn=Ada,ou=users,dc=example,dc=com">` +
		`<attr name="cn"><value>Ada</value></attr></entry></directory-entries></dsml>`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	_, mock, err := adDialerFromEnv(envMap(map[string]string{
		"ATLAS_AD_MOCK": "yes", "ATLAS_AD_MOCK_SEED": path,
	}))
	if err != nil {
		t.Fatalf("adDialerFromEnv: %v", err)
	}
	if len(mock.Entries()) != 1 || !strings.EqualFold(mock.Entries()[0].DN, "cn=Ada,ou=users,dc=example,dc=com") {
		t.Errorf("entries = %v, want the seeded contact", mock.Entries())
	}
}

// The kind still registers its handler in mock mode — and a misconfigured seed stops
// the worker instead, because a mock worker that leased AD jobs it cannot serve would
// park a test process on an incident with no explanation.
func TestBuiltinConnectorsInADMockMode(t *testing.T) {
	got, err := BuiltinConnectors(envMap(map[string]string{"ATLAS_AD_MOCK": "1"}), "ad")
	if err != nil {
		t.Fatalf("BuiltinConnectors: %v", err)
	}
	if _, ok := got.Handlers[compiler.AdJobType]; !ok {
		t.Errorf("no handler for %s; have %v", compiler.AdJobType, got.Handlers)
	}
	if _, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_AD_MOCK": "1", "ATLAS_AD_MOCK_SEED": filepath.Join(t.TempDir(), "nope.ldif"),
	}), "ad"); err == nil {
		t.Error("a worker with an unreadable seed started anyway")
	}

	// A seeded worker serves what the seed put there — the handler the worker leases
	// jobs into is the one holding that directory, which is the whole configuration a
	// mock AD worker has.
	path := filepath.Join(t.TempDir(), "forest.ldif")
	if err := os.WriteFile(path, []byte("dn: cn=Arno,ou=users,dc=example,dc=com\ncn: Arno\n"), 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	seeded, err := BuiltinConnectors(envMap(map[string]string{
		"ATLAS_AD_MOCK": "1", "ATLAS_AD_MOCK_SEED": path,
	}), "ad")
	if err != nil {
		t.Fatalf("BuiltinConnectors with a seed: %v", err)
	}
	if _, err := seeded.Handlers[compiler.AdJobType].Run(context.Background(), adJob(map[string]any{
		"url": "ldaps://dc", "operation": "disable", "dn": "cn=Arno,ou=users,dc=example,dc=com",
	})); err != nil {
		t.Errorf("disable against the seeded worker: %v", err)
	}
}
