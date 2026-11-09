package ad_test

import (
	"encoding/base64"
	"testing"

	"github.com/pblumer/atlas/connector/ad"
)

// objectGUID of an account as AD stores it: the GUID 6f0b2a5c-…, in its mixed-endian
// byte order. Base64 of exactly these bytes is the account's onPremisesImmutableId in
// Entra ID, the source anchor a hard match compares.
var rawGUID = string([]byte{0x5c, 0x2a, 0x0b, 0x6f, 0x91, 0x3e, 0x4a, 0x47, 0x8b, 0x00, 0xff, 0x10, 0x22, 0x33, 0x44, 0x55})

// A seeded mock entry carries the binary attributes as raw bytes — what an LDIF
// "objectGUID:: …" line decodes to, and what AD holds — and a search or a sync answers
// them as base64, as the real worker does. The text attributes stay text.
func TestMockAnswersBinaryAttributesAsBase64(t *testing.T) {
	rawSid := string([]byte{0x01, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00, 0x05, 0x15, 0x00, 0x00, 0x00, 0xa0, 0x65})
	d := ad.NewMockDirectory(ad.Entry{DN: arnoDN, Attributes: map[string][]string{
		"objectClass": {"user"}, "sAMAccountName": {"arno"},
		"objectGUID": {rawGUID}, "objectSid": {rawSid}, "mS-DS-ConsistencyGuid": {rawGUID},
	}})
	wantGUID := base64.StdEncoding.EncodeToString([]byte(rawGUID))

	out := run(t, d, ad.Job{URL: mockTLSURL, Operation: "search", BaseDN: usersDN, Filter: "(sAMAccountName=arno)",
		Select: []string{"sAMAccountName", "objectguid", "objectSid", "ms-ds-consistencyGuid"}, ResultVariable: "r"})
	entries := out["r"].(map[string]any)["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %v", entries)
	}
	attrs := entries[0].(map[string]any)["attributes"].(map[string]any)
	if got := first(t, attrs["objectGUID"]); got != wantGUID {
		t.Errorf("objectGUID = %q, want base64 of the stored bytes %q", got, wantGUID)
	}
	if got := first(t, attrs["mS-DS-ConsistencyGuid"]); got != wantGUID {
		t.Errorf("mS-DS-ConsistencyGuid = %q, want %q", got, wantGUID)
	}
	if got := first(t, attrs["objectSid"]); got != base64.StdEncoding.EncodeToString([]byte(rawSid)) {
		t.Errorf("objectSid = %q, want base64", got)
	}
	if got := first(t, attrs["sAMAccountName"]); got != "arno" {
		t.Errorf("sAMAccountName = %q, want the text untouched", got)
	}

	sync := run(t, d, ad.Job{URL: mockTLSURL, Operation: "sync", BaseDN: baseDN,
		Filter: "(objectClass=user)", ResultVariable: "changes", CookieVariable: "cookie"})
	changed := syncEntries(t, sync)
	if len(changed) != 1 {
		t.Fatalf("sync entries = %v", changed)
	}
	if got := first(t, changed[0].(map[string]any)["attributes"].(map[string]any)["objectGUID"]); got != wantGUID {
		t.Errorf("sync objectGUID = %q, want %q", got, wantGUID)
	}
	// What the mock holds is unchanged: it keeps the bytes, as AD does, and encodes
	// only what it answers.
	if e := entry(t, d, arnoDN); e.Attributes["objectGUID"][0] != rawGUID {
		t.Errorf("stored objectGUID = %q, want the raw bytes", e.Attributes["objectGUID"][0])
	}
	// The Console's view of the mock forest shows what a search would answer, not
	// bytes a JSON snapshot cannot carry intact.
	snap := d.Snapshot(0)
	if len(snap.Forests) != 1 || len(snap.Forests[0].Entries) != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if got := snap.Forests[0].Entries[0].Attributes["objectGUID"]; len(got) != 1 || got[0] != wantGUID {
		t.Errorf("snapshot objectGUID = %q, want [%s]", got, wantGUID)
	}
}

// first is an attribute's first value as the worker's result carries it.
func first(t *testing.T, v any) string {
	t.Helper()
	switch vals := v.(type) {
	case []string:
		if len(vals) > 0 {
			return vals[0]
		}
	case []any:
		if len(vals) > 0 {
			s, _ := vals[0].(string)
			return s
		}
	}
	t.Fatalf("attribute = %#v, want a list of values", v)
	return ""
}
