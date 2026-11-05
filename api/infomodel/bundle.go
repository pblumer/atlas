package infomodel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/api/token"
)

// An application's information model as an application bundle carries it
// (ADR-0528). The model already belongs to its
// application (applicationId); a bundle carries it in the JSON shape
// POST /api/v1/infomodel/import reads, and an import files it under its name — created
// the first time, replaced in place after that, keeping the stored model's id so a link
// to it survives.
//
// These run on the run loop, inside the source import's turn; they take no request.

// ErrBundleModelInvalid is a bundled model the validator refuses.
var ErrBundleModelInvalid = errors.New("the information model is not valid")

// ParseBundleModel reads a bundled model document as an import reads it. The name is
// the document's own; a model without one is refused, because the name is what a later
// import finds it by.
func ParseBundleModel(document []byte) (Model, []ImportNote, error) {
	imported, err := ParseImport("json", document)
	if err != nil {
		return Model{}, nil, err
	}
	m := imported.Model
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		return Model{}, nil, errors.New("the information model has no name")
	}
	return m, imported.Notes, nil
}

// CheckBundleModel says whether a parsed bundled model would be stored: the checks
// FileBundleModel makes, on a copy, so an import can refuse a bad model before it has
// written anything. Runs on the loop (it mints ids).
func (s *Service) CheckBundleModel(m Model) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var cp Model
	if err := json.Unmarshal(raw, &cp); err != nil {
		return err
	}
	if cp.Classes == nil {
		cp.Classes = []Class{}
	}
	if cp.Associations == nil {
		cp.Associations = []Association{}
	}
	if cp.Stores == nil {
		cp.Stores = []DataStore{}
	}
	defaultStoreModes(&cp)
	if err := s.assignIDs(&cp); err != nil {
		return err
	}
	if res := Validate(cp); !res.Valid {
		return fmt.Errorf("%w: %s", ErrBundleModelInvalid, strings.Join(findingTexts(res), "; "))
	}
	return nil
}

// ApplicationModel finds an application's model by name. Runs on the loop.
func (s *Service) ApplicationModel(applicationID, name string) (Model, bool, error) {
	models, err := s.store.ForApplication(applicationID)
	if err != nil {
		return Model{}, false, err
	}
	for _, m := range models {
		if m.Name == name {
			full, ok, err := s.store.Get(m.ID)
			return full, ok, err
		}
	}
	return Model{}, false, nil
}

// FileBundleModel stores a parsed bundled model for an application: a new model, or the
// one of that name replaced in place. It answers the stored model. Runs on the loop.
func (s *Service) FileBundleModel(applicationID string, m Model, actor string) (Model, error) {
	current, exists, err := s.ApplicationModel(applicationID, m.Name)
	if err != nil {
		return Model{}, err
	}
	m.ApplicationID = applicationID
	if m.Classes == nil {
		m.Classes = []Class{}
	}
	if m.Associations == nil {
		m.Associations = []Association{}
	}
	if m.Stores == nil {
		m.Stores = []DataStore{}
	}
	defaultStoreModes(&m)
	if err := s.assignIDs(&m); err != nil {
		return Model{}, err
	}
	if res := Validate(m); !res.Valid {
		return Model{}, fmt.Errorf("%w: %s", ErrBundleModelInvalid, strings.Join(findingTexts(res), "; "))
	}
	now := s.now().Unix()
	if exists {
		m.ID, m.CreatedAt, m.CreatedBy = current.ID, current.CreatedAt, current.CreatedBy
		m.Revision = current.Revision + 1
	} else {
		id, err := s.newID()
		if err != nil {
			return Model{}, err
		}
		m.ID, m.Revision, m.CreatedAt, m.CreatedBy = id, 1, now, actor
	}
	m.UpdatedAt, m.UpdatedBy = now, actor
	if err := s.store.Save(m); err != nil {
		return Model{}, err
	}
	return m, nil
}

// ContentFingerprint is a model's content without what only says where and when it was
// stored: id, application, revision and the audit fields. Two stored models with the
// same fingerprint say the same thing; an edit on the canvas changes it.
func ContentFingerprint(m Model) []byte {
	m.ID, m.ApplicationID, m.Revision = "", "", 0
	m.CreatedAt, m.CreatedBy, m.UpdatedAt, m.UpdatedBy = 0, "", 0, ""
	data, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return data
}

// SameContent reports whether a parsed bundled model says exactly what a stored model
// says: the case of a tree exported from this model and imported again. A document
// whose classes, associations or stores still need ids minted cannot be the same —
// storing it would give it ids the stored model does not have.
func SameContent(stored, m Model) bool {
	if m.Classes == nil {
		m.Classes = []Class{}
	}
	if m.Associations == nil {
		m.Associations = []Association{}
	}
	if m.Stores == nil {
		m.Stores = []DataStore{}
	}
	defaultStoreModes(&m)
	for _, c := range m.Classes {
		if !token.IsHex(c.ID) {
			return false
		}
	}
	for _, a := range m.Associations {
		if !token.IsHex(a.ID) {
			return false
		}
	}
	for _, st := range m.Stores {
		if !token.IsHex(st.ID) {
			return false
		}
	}
	return string(ContentFingerprint(stored)) == string(ContentFingerprint(m))
}

// BundleDocument is a stored model as a bundle carries it: the import shape, without
// the record's id, application, revision and audit fields.
func BundleDocument(m Model) ([]byte, error) {
	m.ID, m.ApplicationID, m.Revision = "", "", 0
	m.CreatedAt, m.CreatedBy, m.UpdatedAt, m.UpdatedBy = 0, "", 0, ""
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func findingTexts(res ValidationResult) []string {
	out := make([]string, 0, len(res.Findings))
	for _, f := range res.Findings {
		out = append(out, f.Message)
	}
	return out
}
