package sharepoint_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/sharepoint"
	"github.com/pblumer/atlas/model"
)

const mockSite = "https://contoso.sharepoint.com/sites/verwaltung"

func newMock(t *testing.T) *sharepoint.MockSite {
	t.Helper()
	at := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	return sharepoint.NewMockSites(func() time.Time { return at }).Site("intranet")
}

// do runs one operation that must succeed and answers its result as a map.
func do(t *testing.T, c sharepoint.Client, req sharepoint.Request) map[string]any {
	t.Helper()
	if req.Site == "" {
		req.Site = mockSite
	}
	got, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: %v", req.Operation, err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("%s answered %T, want an object", req.Operation, got)
	}
	return m
}

// refused runs one operation that must fail and answers its message.
func refused(t *testing.T, c sharepoint.Client, req sharepoint.Request) string {
	t.Helper()
	if req.Site == "" {
		req.Site = mockSite
	}
	got, err := c.Do(context.Background(), req)
	if err == nil {
		t.Fatalf("%s %+v answered %v, want a refusal", req.Operation, req, got)
	}
	return err.Error()
}

func wantContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%q does not mention %q", got, w)
		}
	}
}

func TestMockListItems(t *testing.T) {
	m := newMock(t)
	created := do(t, m, sharepoint.Request{List: "Antraege", Fields: map[string]any{"Title": "Zugang", "Betrag": json.Number("12500"), "Tags": []any{"a", "b"}}})
	if created["id"] != "1" {
		t.Fatalf("first item id = %v, want \"1\": SharePoint numbers from 1, as text", created["id"])
	}
	if url, _ := created["webUrl"].(string); !strings.HasPrefix(url, mockSite+"/Lists/Antraege/") {
		t.Errorf("webUrl = %q", url)
	}
	fields := created["fields"].(map[string]any)
	if fields["Title"] != "Zugang" || fields["Betrag"] != json.Number("12500") || fields["id"] != "1" {
		t.Errorf("fields = %#v", fields)
	}
	// What a caller holds cannot change what the mockup keeps.
	fields["Tags"].([]any)[0] = "x"

	got := do(t, m, sharepoint.Request{Operation: "get-item", List: "antraege", ItemID: "1"})
	if tags := got["fields"].(map[string]any)["Tags"].([]any); tags[0] != "a" {
		t.Errorf("Tags = %v, want the stored value unchanged by a caller's edit", tags)
	}
	sel := do(t, m, sharepoint.Request{Operation: "get-item", List: "Antraege", ItemID: "1", Select: "Title"})
	if f := sel["fields"].(map[string]any); len(f) != 1 || f["Title"] != "Zugang" {
		t.Errorf("selected fields = %#v, want Title only", f)
	}

	upd := do(t, m, sharepoint.Request{Operation: "update-item", List: "Antraege", ItemID: "1", Fields: map[string]any{"Status": "Erledigt", "Tags": nil}})
	if upd["Status"] != "Erledigt" || upd["Title"] != "Zugang" {
		t.Errorf("update answered %#v, want the item's columns after the change", upd)
	}
	if _, ok := upd["Tags"]; ok {
		t.Error("a null value did not clear the column")
	}

	wantContains(t, refused(t, m, sharepoint.Request{Operation: "get-item", List: "Antraege", ItemID: "9"}), "no item 9", "(mockup)")
	wantContains(t, refused(t, m, sharepoint.Request{Operation: "get-item", List: "Nirgends", ItemID: "1"}), `no list "Nirgends"`)
	wantContains(t, refused(t, m, sharepoint.Request{Operation: "update-item", List: "Antraege", ItemID: "abc", Fields: map[string]any{"a": 1}}), "not a list item id")

	if del := do(t, m, sharepoint.Request{Operation: "delete-item", List: "Antraege", ItemID: "1"}); del["deleted"] != true {
		t.Errorf("delete = %v, want deleted=true", del)
	}
	if del := do(t, m, sharepoint.Request{Operation: "delete-item", List: "Antraege", ItemID: "1"}); del["deleted"] != false {
		t.Errorf("second delete = %v, want deleted=false", del)
	}
	if del := do(t, m, sharepoint.Request{Operation: "delete-item", List: "Nirgends", ItemID: "1"}); del["deleted"] != false {
		t.Errorf("delete in a missing list = %v, want deleted=false", del)
	}
	// An id is never reused, as SharePoint never reuses one.
	if next := do(t, m, sharepoint.Request{List: "Antraege", Fields: map[string]any{"Title": "B"}}); next["id"] != "2" {
		t.Errorf("id after a delete = %v, want 2", next["id"])
	}
}

func TestMockRefusesWhatSharePointRefuses(t *testing.T) {
	m := newMock(t)
	do(t, m, sharepoint.Request{Operation: "upload-file", List: "Fallakten", Path: "a.txt", Content: "x"})
	do(t, m, sharepoint.Request{List: "Antraege", Fields: map[string]any{"Title": "A"}})

	for _, c := range []struct {
		req  sharepoint.Request
		want string
	}{
		{sharepoint.Request{List: "Fallakten", Fields: map[string]any{"Title": "x"}}, "document library"},
		{sharepoint.Request{Operation: "delete-item", List: "Fallakten", ItemID: "1"}, "document library"},
		{sharepoint.Request{Operation: "upload-file", List: "Antraege", Path: "a.txt", Content: "x"}, "is a list, not a document library"},
		{sharepoint.Request{Operation: "get-item", List: "Antraege"}, "has no itemId"},
		{sharepoint.Request{Operation: "frobnicate", List: "Antraege"}, "unknown operation"},
		{sharepoint.Request{Site: " ", List: "Antraege"}, "has no site"},
		{sharepoint.Request{Operation: "upload-file", List: "Fallakten", Path: "../x", Content: "x"}, `".." segment`},
	} {
		wantContains(t, refused(t, m, c.req), c.want)
	}
}

func TestMockSiteAddressForms(t *testing.T) {
	m := newMock(t)
	do(t, m, sharepoint.Request{Site: "https://contoso.sharepoint.com/sites/Verwaltung/", List: "L", Fields: map[string]any{"Title": "a"}})
	for _, site := range []string{"contoso.sharepoint.com:/sites/verwaltung", "contoso.sharepoint.com/sites/verwaltung", "CONTOSO.sharepoint.com,/sites/verwaltung"} {
		do(t, m, sharepoint.Request{Site: site, Operation: "get-item", List: "L", ItemID: "1"})
	}
	refused(t, m, sharepoint.Request{Site: "contoso.sharepoint.com:/sites/hr", Operation: "get-item", List: "L", ItemID: "1"})
	if snap := m.Snapshot(); len(snap.Sites) != 1 {
		t.Errorf("sites = %+v, want the one that was written to — a read of another leaves nothing behind", snap.Sites)
	}
}

func TestMockFilterAndPaging(t *testing.T) {
	m := newMock(t)
	for i := 1; i <= 25; i++ {
		status := "Erledigt"
		if i%5 == 0 {
			status = "Neu"
		}
		do(t, m, sharepoint.Request{List: "Antraege", Fields: map[string]any{
			"Title": fmt.Sprintf("Antrag %02d", i), "Status": status, "Betrag": json.Number(fmt.Sprint(i * 1000)), "Dringend": i > 20,
		}})
	}
	count := func(filter string) int {
		t.Helper()
		var n int
		token := ""
		for pages := 0; ; pages++ {
			if pages > 10 {
				t.Fatalf("%q: paging does not end", filter)
			}
			page := do(t, m, sharepoint.Request{Operation: "list-items", List: "Antraege", Filter: filter, MaxResults: 7, PageToken: token})
			items := page["items"].([]any)
			if page["count"] != json.Number(fmt.Sprint(len(items))) {
				t.Errorf("count = %v, items %d", page["count"], len(items))
			}
			n += len(items)
			if page["hasMore"] != true {
				if page["nextPageToken"] != "" {
					t.Errorf("last page carries token %v", page["nextPageToken"])
				}
				return n
			}
			token = page["nextPageToken"].(string)
		}
	}
	for filter, want := range map[string]int{
		"":                            25,
		"fields/Status eq 'Erledigt'": 20,
		"fields/Status eq 'erledigt'": 20,
		"fields/Status ne 'Erledigt'": 5,
		"fields/Betrag gt 20000":      5,
		"fields/Betrag le 3000":       3,
		"fields/Dringend eq true":     5,
		"fields/Status eq 'Neu' and fields/Betrag ge 15000":                                  3,
		"(fields/Betrag lt 2000 or fields/Betrag gt 24000) and not (fields/Status eq 'Neu')": 1,
		"startswith(fields/Title,'Antrag 1')":                                                10,
		"fields/id le 4":                                                                     4,
		"fields/id eq '7'":                                                                   1,
		"fields/Title eq 'O''Brien'":                                                         0,
	} {
		if got := count(filter); got != want {
			t.Errorf("%q matched %d, want %d", filter, got, want)
		}
	}
	// A filter and a selection carried in the token, as Graph carries them.
	page := do(t, m, sharepoint.Request{Operation: "list-items", List: "Antraege", Filter: "fields/Status eq 'Neu'", Select: "Title", MaxResults: 2})
	next := do(t, m, sharepoint.Request{Operation: "list-items", List: "Antraege", PageToken: page["nextPageToken"].(string)})
	for _, it := range next["items"].([]any) {
		f := it.(map[string]any)["fields"].(map[string]any)
		if len(f) != 1 || f["Status"] != nil {
			t.Errorf("resumed page lost its selection: %#v", f)
		}
	}
	if items := next["items"].([]any); len(items) != 2 || items[0].(map[string]any)["id"] != "15" {
		t.Errorf("resumed page = %v, want items 15 and 20", items)
	}

	wantContains(t, refused(t, m, sharepoint.Request{Operation: "list-items", List: "Antraege", Filter: "fields/Stauts eq 'Neu'"}), `no column "Stauts"`)
	for _, bad := range []string{"Status eq 'Neu'", "fields/Status eq Neu", "fields/Betrag add 1 gt 2", "contains(fields/Title,'x')", "fields/Status eq 'Neu", "fields/Betrag gt null"} {
		wantContains(t, refused(t, m, sharepoint.Request{Operation: "list-items", List: "Antraege", Filter: bad}), "the mockup understands")
	}
	wantContains(t, refused(t, m, sharepoint.Request{Operation: "list-items", List: "Antraege", PageToken: "weiter"}), "pass nextPageToken back unchanged")
}

func TestMockFiles(t *testing.T) {
	m := newMock(t)
	// Every site has its default library from the start.
	if page := do(t, m, sharepoint.Request{Operation: "list-folder"}); page["count"] != json.Number("0") {
		t.Errorf("a fresh site's default library = %v, want empty", page)
	}
	wantContains(t, refused(t, m, sharepoint.Request{Operation: "list-folder", List: "Fallakten"}), `no document library "Fallakten"`)

	up := do(t, m, sharepoint.Request{Operation: "upload-file", List: "Fallakten", Path: "/Faelle/17/brief.txt", Content: "Grüezi", Fields: map[string]any{"AntragNr": json.Number("17")}})
	if up["name"] != "brief.txt" || up["size"] != json.Number("7") || up["file"] == nil {
		t.Errorf("upload answered %#v", up)
	}
	if f := up["listItem"].(map[string]any)["fields"].(map[string]any); f["AntragNr"] != json.Number("17") {
		t.Errorf("properties = %#v", f)
	}
	if url, _ := up["webUrl"].(string); url != mockSite+"/Fallakten/Faelle/17/brief.txt" {
		t.Errorf("webUrl = %q", url)
	}

	got := do(t, m, sharepoint.Request{Operation: "get-file", List: "Fallakten", Path: "faelle/17/BRIEF.txt"})
	if got["exists"] != true || got["listItem"].(map[string]any)["fields"].(map[string]any)["AntragNr"] != json.Number("17") {
		t.Errorf("get-file = %#v, want the file with its properties, found without regard to case", got)
	}
	if got := do(t, m, sharepoint.Request{Operation: "get-file", List: "Fallakten", Path: "Faelle/18/brief.txt"}); got["exists"] != false {
		t.Errorf("get-file of a missing path = %v, want exists=false", got)
	}
	if got := do(t, m, sharepoint.Request{Operation: "get-file", List: "Nirgends", Path: "x"}); got["exists"] != false {
		t.Errorf("get-file in a missing library = %v, want exists=false", got)
	}

	down := do(t, m, sharepoint.Request{Operation: "download-file", List: "Fallakten", Path: "Faelle/17/brief.txt"})
	if down["content"] != "Grüezi" || down["encoding"] != "text" || !strings.HasPrefix(down["contentType"].(string), "text/plain") {
		t.Errorf("download = %#v", down)
	}
	bin := []byte{0xff, 0x00, 0x10}
	do(t, m, sharepoint.Request{Operation: "upload-file", Path: "bild.bin", Content: base64.StdEncoding.EncodeToString(bin), Encoding: "base64"})
	if down := do(t, m, sharepoint.Request{Operation: "download-file", Path: "bild.bin", Encoding: "base64"}); down["content"] != base64.StdEncoding.EncodeToString(bin) {
		t.Errorf("binary round trip = %v", down["content"])
	}
	wantContains(t, refused(t, m, sharepoint.Request{Operation: "download-file", Path: "bild.bin"}), "is not text")
	wantContains(t, refused(t, m, sharepoint.Request{Operation: "download-file", List: "Fallakten", Path: "Faelle/17"}), "no file")

	folder := do(t, m, sharepoint.Request{Operation: "create-folder", List: "Fallakten", Path: "Faelle/17"})
	if folder["created"] != false || folder["folder"].(map[string]any)["childCount"] != json.Number("1") {
		t.Errorf("create-folder of an existing folder = %#v, want created=false with its one child", folder)
	}
	if folder := do(t, m, sharepoint.Request{Operation: "create-folder", List: "Fallakten", Path: "Faelle/18/Anhang"}); folder["created"] != true {
		t.Errorf("create-folder = %v, want created=true", folder)
	}
	wantContains(t, refused(t, m, sharepoint.Request{Operation: "create-folder", List: "Fallakten", Path: "Faelle/17/brief.txt"}), "is a file, not a folder")
	wantContains(t, refused(t, m, sharepoint.Request{Operation: "upload-file", List: "Fallakten", Path: "Faelle/17", Content: "x"}), "is a folder")
	wantContains(t, refused(t, m, sharepoint.Request{Operation: "upload-file", List: "Fallakten", Path: "Faelle/17/brief.txt/x", Content: "x"}), "is a file, not a folder")

	for i := 0; i < 5; i++ {
		do(t, m, sharepoint.Request{Operation: "upload-file", List: "Fallakten", Path: fmt.Sprintf("Faelle/17/anhang-%d.txt", i), Content: "x"})
	}
	var names []string
	token := ""
	for {
		page := do(t, m, sharepoint.Request{Operation: "list-folder", List: "Fallakten", Path: "Faelle/17", MaxResults: 4, PageToken: token})
		for _, it := range page["items"].([]any) {
			names = append(names, it.(map[string]any)["name"].(string))
		}
		if page["hasMore"] != true {
			break
		}
		token = page["nextPageToken"].(string)
	}
	if fmt.Sprint(names) != "[anhang-0.txt anhang-1.txt anhang-2.txt anhang-3.txt anhang-4.txt brief.txt]" {
		t.Errorf("folder listing = %v", names)
	}
	wantContains(t, refused(t, m, sharepoint.Request{Operation: "list-folder", List: "Fallakten", Path: "Faelle/17/brief.txt"}), "is a file, not a folder")
	wantContains(t, refused(t, m, sharepoint.Request{Operation: "list-folder", List: "Fallakten", Path: "Faelle/99"}), "no folder")

	if del := do(t, m, sharepoint.Request{Operation: "delete-file", List: "Fallakten", Path: "Faelle/17"}); del["deleted"] != true || del["path"] != "Faelle/17" {
		t.Errorf("delete-file = %v", del)
	}
	if got := do(t, m, sharepoint.Request{Operation: "get-file", List: "Fallakten", Path: "Faelle/17/anhang-0.txt"}); got["exists"] != false {
		t.Error("a deleted folder's content is still there")
	}
	if del := do(t, m, sharepoint.Request{Operation: "delete-file", List: "Fallakten", Path: "Faelle/17"}); del["deleted"] != false {
		t.Errorf("second delete-file = %v, want deleted=false", del)
	}
	if snap := m.Snapshot(); snap.StoredBytes != int64(len(bin)) {
		t.Errorf("stored bytes = %d, want only the binary left after the folder went", snap.StoredBytes)
	}
}

func TestMockJournalAndSnapshot(t *testing.T) {
	sites := sharepoint.NewMockSites(nil)
	m := sites.Site("intranet")
	if sites.Site("intranet") != m {
		t.Fatal("Site does not return the same mockup for the same Worker")
	}
	do(t, m, sharepoint.Request{List: "Antraege", Fields: map[string]any{"Title": "A", "Kunde": "Muster AG"}})
	do(t, m, sharepoint.Request{Operation: "upload-file", List: "Fallakten", Path: "Faelle/1/a.txt", Content: "abc", Fields: map[string]any{"AntragNr": json.Number("1")}})
	refused(t, m, sharepoint.Request{Operation: "get-item", List: "Antraege", ItemID: "7"})

	snap := sites.Snapshot()
	if len(snap) != 1 || snap[0].Worker != "intranet" || snap[0].StoredBytes != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}
	web := snap[0].Sites[0]
	if web.URL != mockSite || len(web.Lists) != 2 {
		t.Fatalf("site = %+v", web)
	}
	antraege, akten := web.Lists[0], web.Lists[1]
	if antraege.Title != "Antraege" || antraege.Library || fmt.Sprint(antraege.Columns) != "[Kunde Title]" || antraege.Held != 1 || antraege.Items[0]["Kunde"] != "Muster AG" {
		t.Errorf("list = %+v", antraege)
	}
	if !akten.Library || akten.Held != 3 || akten.Files[2].Path != "Faelle/1/a.txt" || akten.Files[2].Size != 3 || !akten.Files[0].Folder {
		t.Errorf("library = %+v", akten)
	}
	journal := snap[0].Journal
	if len(journal) != 3 || journal[0].Operation != "create-item" || journal[1].Target != "Fallakten/Faelle/1/a.txt" || journal[2].Target != "Antraege #7" || !strings.Contains(journal[2].Error, "no item 7") {
		t.Errorf("journal = %+v", journal)
	}

	for i := 0; i < 250; i++ {
		do(t, m, sharepoint.Request{Operation: "get-item", List: "Antraege", ItemID: "1"})
	}
	if j := m.Snapshot().Journal; len(j) != 200 || j[len(j)-1].Seq != 253 {
		t.Errorf("journal holds %d, last seq %d; want the newest 200", len(j), j[len(j)-1].Seq)
	}

	// Clear empties the site the engine's registry holds, rather than swapping in a new
	// one the engine would never write to.
	if !sites.Clear("intranet") {
		t.Fatal("Clear did not find the mockup")
	}
	if snap := m.Snapshot(); len(snap.Sites) != 0 || len(snap.Journal) != 0 || snap.StoredBytes != 0 {
		t.Errorf("after Clear = %+v, want an empty site", snap)
	}
	if created := do(t, m, sharepoint.Request{List: "Antraege", Fields: map[string]any{"Title": "neu"}}); created["id"] != "1" {
		t.Errorf("first item after Clear = %v, want id 1 in the emptied list", created["id"])
	}
	if sites.Clear("nobody") {
		t.Error("Clear reported a mockup for a Worker that has none")
	}

	sites.Site("other")
	sites.Keep("other")
	if _, ok := sites.Lookup("intranet"); ok {
		t.Error("Keep did not drop a Worker that is no longer a mockup")
	}
	if _, ok := sites.Lookup("other"); !ok {
		t.Error("Keep dropped a Worker it was told to keep")
	}
}

// TestRemoteMockForwards is the worker's half: an operation travels to the server's
// mockup and back unchanged, a refusal arrives as the mockup worded it, and a server
// that holds no mockup for the Worker says what to check.
func TestRemoteMockForwards(t *testing.T) {
	m := newMock(t)
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/v1/sharepoint/mock-sites/intranet eins" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req sharepoint.Request
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		if err := dec.Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		answer, err := m.Do(r.Context(), req)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(sharepoint.MockAnswer{Error: err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(sharepoint.MockAnswer{Answer: answer})
	}))
	defer srv.Close()

	remote := sharepoint.NewRemoteMock(srv.URL+"/api/v1/sharepoint/mock-sites/", "intranet eins", " tok ")
	created := do(t, remote, sharepoint.Request{List: "Antraege", Fields: map[string]any{"Betrag": json.Number("12.5"), "Dringend": true, "Tags": []any{"a"}}})
	if auth != "Bearer tok" {
		t.Errorf("Authorization = %q", auth)
	}
	f := created["fields"].(map[string]any)
	if f["Betrag"] != json.Number("12.5") || f["Dringend"] != true || fmt.Sprint(f["Tags"]) != "[a]" {
		t.Errorf("fields after the round trip = %#v, want each value in its JSON shape", f)
	}
	wantContains(t, refused(t, remote, sharepoint.Request{Operation: "get-item", List: "Antraege", ItemID: "5"}), "no item 5", "(mockup)")

	gone := sharepoint.NewRemoteMock(srv.URL+"/api/v1/sharepoint/mock-sites", "geloescht", "")
	wantContains(t, refused(t, gone, sharepoint.Request{List: "A"}), "holds no mockup for this Worker")

	unreachable := sharepoint.NewRemoteMock("http://127.0.0.1:1/api", "intranet", "")
	wantContains(t, refused(t, unreachable, sharepoint.Request{List: "A"}), "reach the mockup")
}

// The shipped examples run on the mockup as they run on the fake Graph site in
// example_test.go — the setup process first, which puts into the empty site what a
// person would have: an example a user starts against a mockup Worker has to finish.
func TestExamplesRunOnTheMockup(t *testing.T) {
	m := newMock(t)
	runExampleOn(t, "sharepoint-testdaten.bpmn", m)
	if page := do(t, m, sharepoint.Request{Operation: "list-items", List: "Antraege"}); page["count"] != json.Number("1") {
		t.Fatalf("after the setup process, Antraege = %v, want the one request it created", page)
	}

	runExampleOn(t, "sharepoint-fallakte.bpmn", m, model.VariableValue{Name: "antragId", Kind: model.VarString, Text: "1"})

	brief := do(t, m, sharepoint.Request{Operation: "download-file", List: "Fallakten", Path: "Faelle/1/eingangsbestaetigung.txt"})
	if got, _ := brief["content"].(string); !strings.HasPrefix(got, "Guten Tag Muster AG\n\nIhr Antrag Nr. 1 ist bei uns eingegangen.") {
		t.Errorf("letter = %q, want the template from the setup process filled in, line breaks and all", got)
	}
	antrag := do(t, m, sharepoint.Request{Operation: "get-item", List: "Antraege", ItemID: "1"})["fields"].(map[string]any)
	if antrag["Status"] != "In Bearbeitung" || antrag["Dringend"] != true || !strings.HasSuffix(fmt.Sprint(antrag["Fallordner"]), "/Fallakten/Faelle/1") {
		t.Errorf("request after the run = %#v", antrag)
	}
	if page := do(t, m, sharepoint.Request{Operation: "list-items", List: "Protokoll"}); page["count"] != json.Number("1") {
		t.Errorf("protocol = %v, want one entry", page)
	}

	// A settled request from the setup process, the case-file request settled by hand,
	// and an open one: the clean-up takes the two settled ones and the first one's folder.
	runExampleOn(t, "sharepoint-testdaten.bpmn", m, model.VariableValue{Name: "status", Kind: model.VarString, Text: "Erledigt"})
	if st := do(t, m, sharepoint.Request{Operation: "get-item", List: "Antraege", ItemID: "2"})["fields"].(map[string]any)["Status"]; st != "Erledigt" {
		t.Errorf("setup process with status = %v, want the status it was started with", st)
	}
	do(t, m, sharepoint.Request{Operation: "update-item", List: "Antraege", ItemID: "1", Fields: map[string]any{"Status": "Erledigt"}})
	runExampleOn(t, "sharepoint-testdaten.bpmn", m)
	runExampleOn(t, "sharepoint-aufraeumen.bpmn", m)

	left := do(t, m, sharepoint.Request{Operation: "list-items", List: "Antraege"})["items"].([]any)
	if len(left) != 1 || left[0].(map[string]any)["id"] != "3" || left[0].(map[string]any)["fields"].(map[string]any)["Status"] != "Neu" {
		t.Errorf("requests left = %v, want only the open one", left)
	}
	if got := do(t, m, sharepoint.Request{Operation: "get-file", List: "Fallakten", Path: "Faelle/1"}); got["exists"] != false {
		t.Error("the settled request's folder is still there")
	}
}

// A route that answers something other than the mockup's two answers fails the job with
// what it said, quoted, rather than passing on a value nobody can read.
func TestRemoteMockOddAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/m/") {
		case "kaputt":
			_, _ = w.Write([]byte("kein JSON"))
		case "leer":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, strings.Repeat("x", 10000), http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	for worker, want := range map[string]string{
		"kaputt": "answered 200 OK: kein JSON",
		"leer":   "answered 422",
		"fehler": "answered 500",
	} {
		msg := refused(t, sharepoint.NewRemoteMock(srv.URL+"/m", worker, ""), sharepoint.Request{List: "A"})
		wantContains(t, msg, want)
		if len(msg) > 5000 {
			t.Errorf("%s: the error quotes %d bytes of the answer, want it cut", worker, len(msg))
		}
	}
}
