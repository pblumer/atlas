package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/apppkg"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// The application package from the Console (ADR-0467, amended 2026-10-21).
//
// `atlas export` writes a package from a terminal: the application's source, and the
// catalogues named with --catalog, whose groups and people become questions
// answered by name. A person without a terminal had only the source download, which
// carries no shop, and the catalogue's own export, which carries its groups and
// people as this installation's ids — ids another installation does not have, which
// its import keeps without a word. This is the same package from one route, built by
// the same code the command uses (apppkg), so what the Console downloads `atlas
// import` installs and the other way round.
//
// The import has no route of its own. The Console calls the routes the command
// does — the source import, the constants, the publish, the catalogue import, each
// under the authority it already asks — and needs from the server only what it
// cannot do in a browser without a second copy of the rules: answering the
// package's questions from this server's directory (handlePackageAnswers).

// packagePreview is what GET …/package?preview=true answers instead of the archive:
// what the download would hold, and what it would say on a terminal.
type packagePreview struct {
	ApplicationID string   `json:"applicationId"`
	Key           string   `json:"key"`
	Files         []string `json:"files"`
	Catalogs      []string `json:"catalogs"`
	Products      int      `json:"products"`
	// Publish says the import publishes the catalogues: every one has a release here.
	Publish bool `json:"publish"`
	// Questions counts the groups and people the catalogues name, each a question the
	// importing server answers by name.
	Questions int      `json:"questions"`
	Notes     []string `json:"notes"`
	Warnings  []string `json:"warnings"`
}

// handleExportApplicationPackage serves GET /api/v1/applications/{id}/package: the
// application's source tree, ?constants=true with its constants, and every
// ?catalog= named as katalog.json with fragen.json and antworten.json — the package
// `atlas export` writes, as one gzip tar. ?preview=true answers what it would hold
// instead.
//
// The source needs a viewer's access, as the source download does. A catalogue needs
// the productmanager role and a maintainer's access to it, as its own export does,
// because it is exported through that export.
func (s *Server) handleExportApplicationPackage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, code, msg := s.authorizeProject(r, id, ScopeRoleViewer)
	if code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	q := r.URL.Query()
	withConstants, err := boolQuery(q.Get("constants"), "constants")
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	preview, err := boolQuery(q.Get("preview"), "preview")
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	var catalogs []string
	for _, v := range q["catalog"] {
		for _, c := range strings.Split(v, ",") {
			if c = strings.TrimSpace(c); c != "" && !slices.Contains(catalogs, c) {
				catalogs = append(catalogs, c)
			}
		}
	}
	p := httpapi.PrincipalFrom(r.Context())
	if len(catalogs) > 0 {
		if s.catalogueOff {
			httpapi.Error(w, http.StatusConflict, "this server has its catalogue switched off (--catalogue=false); export the application without catalogues")
			return
		}
		if !mayReach(p, RoleProductManager) {
			refuseRole(w, r, RoleProductManager)
			return
		}
	}

	var (
		rec     sourceRecords
		loadErr error
	)
	s.do(func() { rec, loadErr = s.loadApplicationSource(proj, withConstants) })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "export package: "+loadErr.Error())
		return
	}
	files, err := rec.tree()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "export package: "+err.Error())
		return
	}
	man, _, err := parseSourceTree(files)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "export package: "+err.Error())
		return
	}
	out := packagePreview{ApplicationID: id, Key: man.Key, Catalogs: catalogs, Notes: []string{}, Warnings: []string{}}

	if len(catalogs) > 0 {
		shop, code, msg := s.packageShop(p, catalogs, man)
		if code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
		files = append(files, sourceFile{Path: apppkg.CatalogueFile, Data: shop.Document})
		if len(shop.Questions) > 0 {
			questions, err := apppkg.EncodeJSON(shop.Questions)
			if err != nil {
				httpapi.Error(w, http.StatusInternalServerError, "export package: "+err.Error())
				return
			}
			answers, err := apppkg.EncodeJSON(shop.Answers)
			if err != nil {
				httpapi.Error(w, http.StatusInternalServerError, "export package: "+err.Error())
				return
			}
			files = append(files, sourceFile{Path: apppkg.QuestionsFile, Data: questions},
				sourceFile{Path: apppkg.AnswersFile, Data: answers})
		}
		out.Products, out.Publish, out.Questions = shop.Products, shop.Publish, len(shop.Questions)
		out.Notes = append(out.Notes, shop.Notes...)
		out.Warnings = append(out.Warnings, shop.Warnings...)
	}
	if preview {
		for _, f := range files {
			out.Files = append(out.Files, f.Path)
		}
		httpapi.JSON(w, http.StatusOK, out)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", man.Key+"-package.tar.gz"))
	if err := writeSourceArchive(w, files); err != nil {
		logging.Error(logging.ApplicationSourceStreamFailed, "application package stream failed after the header was sent",
			slog.String("error", err.Error()))
	}
}

// packageShop exports each catalogue as its own export does, for p, and builds the
// package's shop from them. code and msg are the first refusal, 0 when there is none.
// Each export takes its own turn of the run loop, so this runs off it.
func (s *Server) packageShop(p *httpapi.Principal, catalogs []string, man sourceManifest) (apppkg.Shop, int, string) {
	parts := make([]apppkg.Part, 0, len(catalogs))
	for _, cid := range catalogs {
		doc, code, msg := s.catalogs.ExportDocument(p, cid, true)
		if code != 0 {
			return apppkg.Shop{}, code, msg
		}
		released, err := s.catalogs.Released(cid)
		if err != nil {
			return apppkg.Shop{}, http.StatusInternalServerError, "export package: " + err.Error()
		}
		// As data, the way the command reads the export's answer: a field this server
		// adds later travels without this code knowing it.
		raw, err := json.Marshal(doc)
		if err != nil {
			return apppkg.Shop{}, http.StatusInternalServerError, "export package: " + err.Error()
		}
		var data map[string]any
		if err := apppkg.Decode(raw, &data); err != nil {
			return apppkg.Shop{}, http.StatusInternalServerError, "export package: " + err.Error()
		}
		parts = append(parts, apppkg.Part{ID: cid, Document: data, Released: released})
	}
	var app apppkg.Application
	for _, proc := range man.Processes {
		app.Processes = append(app.Processes, proc.ID)
	}
	for _, f := range man.Forms {
		app.Forms = append(app.Forms, f.ID)
	}
	dir, err := s.packageDirectory()
	if err != nil {
		return apppkg.Shop{}, http.StatusInternalServerError, "export package: " + err.Error()
	}
	shop, err := apppkg.BuildShop(parts, app, dir, nil)
	if err != nil {
		return apppkg.Shop{}, http.StatusInternalServerError, "export package: " + err.Error()
	}
	return shop, 0, ""
}

// packageDirectory is this server's directory in the package's terms: its users and
// groups by id, as GET /api/v1/principals lists them, and every enabled account by
// username — a fixed approver is an account, because the approval becomes a task
// assigned to that username.
func (s *Server) packageDirectory() ([]apppkg.DirectoryEntry, error) {
	var (
		list  []principalDirEntry
		users []User
		err   error
	)
	s.do(func() {
		if list, err = s.principalDirectoryOnLoop(); err != nil {
			return
		}
		users, err = s.users.LoadAll()
	})
	if err != nil {
		return nil, err
	}
	out := make([]apppkg.DirectoryEntry, 0, len(list)+len(users))
	for _, e := range list {
		out = append(out, apppkg.DirectoryEntry{Type: e.Type, ID: e.ID, Name: e.Name})
	}
	for _, u := range users {
		if u.Disabled {
			continue
		}
		name := u.DisplayName
		if name == "" {
			name = u.Username
		}
		out = append(out, apppkg.DirectoryEntry{Type: apppkg.KindAccount, ID: u.Username, Name: name})
	}
	return out, nil
}

// packageAnswersReq is what POST /api/v1/catalogs/import/answers takes: a package's
// katalog.json, its fragen.json, its answers — antworten.json as it came — and what
// the person importing chose instead.
type packageAnswersReq struct {
	Document  json.RawMessage            `json:"document"`
	Questions map[string]apppkg.Question `json:"questions"`
	Answers   map[string]string          `json:"answers"`
	Chosen    map[string]string          `json:"chosen"`
}

// packageAnswersResp is its answer: the document with every question answered, and
// how each answer was taken; or, with 422, the questions it could not answer.
type packageAnswersResp struct {
	Document json.RawMessage   `json:"document,omitempty"`
	Resolved map[string]string `json:"resolved,omitempty"`
	Notes    []string          `json:"notes"`
	Problems []apppkg.Problem  `json:"problems,omitempty"`
	// Learned are the choices worth adding to the table of names
	// (POST /api/v1/catalogs/import/mappings) once the import went through;
	// Remembered names the questions the table answered.
	Learned    []apppkg.Mapping `json:"learned"`
	Remembered []string         `json:"remembered"`
}

// handlePackageAnswers serves POST /api/v1/catalogs/import/answers: it answers a
// package's questions — the person's choice first, then this server's table of names
// for the packaged answer, then its directory, a group or a person by id or by a name
// exactly one of them carries — and puts the answers in place, the way `atlas import`
// does before it imports a catalogue. Nothing is written: the filled document is what
// the person then imports, through the catalogue import and its plan, and the choices
// worth remembering are answered for the caller to record once that went through. A
// question it cannot answer is named with why, and the ids an ambiguous name matches,
// so the Console can ask the person instead.
func (s *Server) handlePackageAnswers(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, s.budgets().AppBundle))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req packageAnswersReq
	if err := json.Unmarshal(raw, &req); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	doc := []byte(req.Document)
	if len(doc) == 0 || !json.Valid(doc) {
		httpapi.Error(w, http.StatusBadRequest, "document is not a catalogue document")
		return
	}
	// A document sent as a JSON string (the file read as text) is the same document.
	var text string
	if json.Unmarshal(doc, &text) == nil {
		doc = []byte(text)
	}
	mappings, err := s.packageMappings()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read the table: "+err.Error())
		return
	}
	res, err := apppkg.Resolve(doc, req.Questions, apppkg.Answers{Packaged: req.Answers, Chosen: req.Chosen, Mappings: mappings}, s.packageDirectory)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read the directory: "+err.Error())
		return
	}
	resp := packageAnswersResp{Notes: res.Notes, Learned: res.Learned, Remembered: res.Remembered}
	if resp.Notes == nil {
		resp.Notes = []string{}
	}
	if resp.Learned == nil {
		resp.Learned = []apppkg.Mapping{}
	}
	if resp.Remembered == nil {
		resp.Remembered = []string{}
	}
	if len(res.Problems) > 0 {
		resp.Problems = res.Problems
		httpapi.JSON(w, http.StatusUnprocessableEntity, resp)
		return
	}
	filled, err := apppkg.Fill(doc, res.Values)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	resp.Document, resp.Resolved = filled, res.Values
	httpapi.JSON(w, http.StatusOK, resp)
}

// boolQuery reads a query flag that is true, false or absent.
func boolQuery(v, name string) (bool, error) {
	switch v {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	}
	return false, errors.New(name + " takes true or false, not " + fmt.Sprintf("%q", v))
}
