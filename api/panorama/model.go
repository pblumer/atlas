package panorama

// Model is one application-owned Panorama architecture document. XML remains
// the canonical source; the surrounding fields provide Atlas ownership,
// optimistic concurrency, and cheap listings without parsing every document.
type Model struct {
	ID            string `json:"id"`
	ApplicationID string `json:"applicationId"`
	Name          string `json:"name"`
	Notation      string `json:"notation"`
	Revision      int64  `json:"revision"`
	XML           string `json:"xml"`
	CreatedAt     int64  `json:"createdAt"`
	CreatedBy     string `json:"createdBy,omitempty"`
	UpdatedAt     int64  `json:"updatedAt"`
	UpdatedBy     string `json:"updatedBy,omitempty"`
}

// Summary is the list/read representation of a Model. The large XML document is
// intentionally absent and is fetched only through the dedicated export route.
type Summary struct {
	ID            string `json:"id"`
	ApplicationID string `json:"applicationId"`
	Name          string `json:"name"`
	Notation      string `json:"notation"`
	Revision      int64  `json:"revision"`
	CreatedAt     int64  `json:"createdAt"`
	CreatedBy     string `json:"createdBy,omitempty"`
	UpdatedAt     int64  `json:"updatedAt"`
	UpdatedBy     string `json:"updatedBy,omitempty"`
}

func summarize(model Model) Summary {
	return Summary{
		ID: model.ID, ApplicationID: model.ApplicationID, Name: model.Name,
		Notation: model.Notation, Revision: model.Revision,
		CreatedAt: model.CreatedAt, CreatedBy: model.CreatedBy,
		UpdatedAt: model.UpdatedAt, UpdatedBy: model.UpdatedBy,
	}
}
