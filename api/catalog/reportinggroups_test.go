package catalog

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Who may read the organisation dashboard of a catalogue's audience
// (ADR-0515 §3).
//
// A reporting group is an access grant to names: its members see who in the
// catalogue's audience holds and ordered what. That is a disclosure decision, so it
// is made by whoever may share the catalogue — its owner or an administrator —
// and not by every editor, the same line sharing is drawn on.

func TestOnlyWhoMaySharePicksTheReportingGroups(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))
	share := `{"members":[{"ref":{"type":"user","id":"usr_ed"},"role":"editor"}]}`
	if rec := as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", share, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("share = %d (%s)", rec.Code, rec.Body)
	}

	if rec := as(t, s.HandleUpdateCatalog, user("usr_ed"), "PATCH", `{"reportingGroups":["grp_it_bund"]}`, "id", cat.ID); rec.Code != http.StatusForbidden {
		t.Errorf("an editor set the reporting groups: %d (%s), want 403", rec.Code, rec.Body)
	}
	if rec := as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", `{"reportingGroups":["grp_it_bund"]}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("the owner could not set them: %d (%s)", rec.Code, rec.Body)
	}
	got := decode[Catalog](t, as(t, s.HandleGetCatalog, user("usr_a"), "GET", "", "id", cat.ID))
	if len(got.ReportingGroups) != 1 || got.ReportingGroups[0] != "grp_it_bund" {
		t.Fatalf("reporting groups = %v", got.ReportingGroups)
	}
	// An editor's ordinary change leaves them as they are.
	if rec := as(t, s.HandleUpdateCatalog, user("usr_ed"), "PATCH", `{"rank":3}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("editor rank change = %d", rec.Code)
	}
	got = decode[Catalog](t, as(t, s.HandleGetCatalog, user("usr_a"), "GET", "", "id", cat.ID))
	if len(got.ReportingGroups) != 1 {
		t.Errorf("an unrelated change cleared the reporting groups: %v", got.ReportingGroups)
	}
	// And an empty list clears them.
	if rec := as(t, s.HandleUpdateCatalog, user("usr_a"), "PATCH", `{"reportingGroups":[]}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("clear = %d", rec.Code)
	}
	got = decode[Catalog](t, as(t, s.HandleGetCatalog, user("usr_a"), "GET", "", "id", cat.ID))
	if len(got.ReportingGroups) != 0 {
		t.Errorf("reporting groups not cleared: %v", got.ReportingGroups)
	}
}

func TestReportsToNeedsAMembership(t *testing.T) {
	c := Catalog{ReportingGroups: []string{"grp_it_bund", "grp_finanz"}}
	if !c.ReportsTo([]string{"grp_x", "grp_finanz"}) {
		t.Error("a member of a reporting group is not reported to")
	}
	if c.ReportsTo([]string{"grp_x"}) || c.ReportsTo(nil) || (Catalog{}).ReportsTo([]string{"grp_x"}) {
		t.Error("somebody outside every reporting group is reported to")
	}
}

// A document carries no reporting groups out and takes none in: they name this
// installation's groups and grant access to names, so neither an export nor an
// import made by an editor may move them.
func TestReportingGroupsNeitherLeaveNorEnterWithADocument(t *testing.T) {
	s := serviceWithAdmin(t)
	owner := user("usr_a")
	cat := makeCatalog(t, s, owner)
	if rec := as(t, s.HandleUpdateCatalog, owner, "PATCH", `{"reportingGroups":["grp_it_bund"]}`, "id", cat.ID); rec.Code != http.StatusOK {
		t.Fatalf("set = %d (%s)", rec.Code, rec.Body)
	}
	d := exportOf(t, call(t, s.HandleExportCatalog, owner, "GET", "/x", "", "id", cat.ID))
	if len(d.Catalogs) != 1 || len(d.Catalogs[0].ReportingGroups) != 0 {
		t.Fatalf("the export carries reporting groups: %+v", d.Catalogs)
	}
	d.Catalogs[0].ReportingGroups = []string{"grp_somebody_else"}
	body, _ := json.Marshal(d)
	if rec := call(t, s.HandleImportDocument, owner, "POST", "/x?overwrite=*", string(body)); rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("import = %d (%s)", rec.Code, rec.Body)
	}
	got := decode[Catalog](t, as(t, s.HandleGetCatalog, owner, "GET", "", "id", cat.ID))
	if len(got.ReportingGroups) != 1 || got.ReportingGroups[0] != "grp_it_bund" {
		t.Errorf("an import changed the reporting groups to %v", got.ReportingGroups)
	}
}

func TestReportingGroupsAreTrimmedAndKeptOnce(t *testing.T) {
	got := cleanGroups([]string{" grp_a ", "", "grp_b", "grp_a", "  "})
	if len(got) != 2 || got[0] != "grp_a" || got[1] != "grp_b" {
		t.Errorf("cleanGroups = %q", got)
	}
}
