package playground

import (
	"fmt"
	"net/http"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/playground"
)

// maxPreviewRows is how many generated cases a preview shows. It is a sample, not
// the dataset: a reader checks that an amount looks like an amount and that the
// rare option is rare, and neither needs a hundred rows.
const maxPreviewRows = 25

// generateReq describes a dataset instead of listing one.
//
// It is the third way to put data into a run, beside a list sent inline and a CSV
// the server parses — and the only one that scales in both directions at once. A
// list of fifty thousand cases is not something anybody types or reviews, and an
// uploaded file cannot be stored in a scenario, so a run driven by one can never
// be repeated. Twenty lines describing the fields can be, which is what makes a
// generated run something a build can hold a process to.
type generateReq struct {
	// Count is how many cases to produce.
	Count int `json:"count"`
	// Fields are the start variables each case carries.
	Fields []fieldReq `json:"fields,omitempty"`
}

// fieldReq is one generated variable. Kind decides which of the rest are read;
// see [playground.Field] for what each of them means.
type fieldReq struct {
	Name string `json:"name"`
	// Kind is "int", "decimal", "bool", "choice", "constant", "sequence" or
	// "timestamp".
	Kind        string      `json:"kind"`
	Min         float64     `json:"min,omitempty"`
	Max         float64     `json:"max,omitempty"`
	Decimals    int         `json:"decimals,omitempty"`
	PercentTrue int         `json:"percentTrue,omitempty"`
	Choices     []choiceReq `json:"choices,omitempty"`
	Value       any         `json:"value,omitempty"`
	Prefix      string      `json:"prefix,omitempty"`
	// FromMillis and ToMillis bound a "timestamp" field, as offsets from the run's
	// own simulated start — negative for before it. They are relative because the
	// run happens on a virtual clock: a date typed in once is stale by the time the
	// scenario runs again, while "some time in the last thirty days" is not.
	FromMillis int64 `json:"fromMillis,omitempty"`
	ToMillis   int64 `json:"toMillis,omitempty"`
	OnlyDate   bool  `json:"onlyDate,omitempty"`
}

type choiceReq struct {
	Value  any `json:"value"`
	Weight int `json:"weight,omitempty"`
}

// toDataset converts the request into the description the engine draws from. An
// unknown kind is passed through rather than rejected here, so the one place that
// says which kinds exist is the one that implements them.
func (g generateReq) toDataset() playground.Dataset {
	out := playground.Dataset{Count: g.Count, Fields: make([]playground.Field, 0, len(g.Fields))}
	for _, f := range g.Fields {
		field := playground.Field{
			Name: f.Name, Kind: playground.FieldKind(f.Kind),
			Min: f.Min, Max: f.Max, Decimals: f.Decimals,
			PercentTrue: f.PercentTrue, Value: f.Value, Prefix: f.Prefix,
			MinOffset: time.Duration(f.FromMillis) * time.Millisecond,
			MaxOffset: time.Duration(f.ToMillis) * time.Millisecond,
			OnlyDate:  f.OnlyDate,
		}
		for _, c := range f.Choices {
			field.Choices = append(field.Choices, playground.Choice{Value: c.Value, Weight: c.Weight})
		}
		out.Fields = append(out.Fields, field)
	}
	return out
}

// columns are the field names in the order they were described, so a preview
// table reads down the list somebody typed rather than in map order.
func (g generateReq) columns() []string {
	out := make([]string, 0, len(g.Fields))
	for _, f := range g.Fields {
		out = append(out, f.Name)
	}
	return out
}

type previewResp struct {
	// Total is how many cases the run would carry, of which Rows are the first.
	Total   int              `json:"total"`
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
}

// HandleGeneratePreview shows the first cases a description would produce.
//
// They are the first cases the run will produce, not a sample of what one might
// look like: the preview draws on the same seed and the same simulated start, and
// every case draws on its own position, so nothing here depends on the rows that
// would follow. A preview that showed something other than what runs would be
// worse than showing nothing.
func (s *Service) HandleGeneratePreview(w http.ResponseWriter, r *http.Request) {
	var req generateReq
	if !decode(w, r, maxBodyBytes, &req) {
		return
	}
	sess, ok := s.session(w, r)
	if !ok {
		return
	}
	seed, start, ok := s.datasetDraw(w, sess)
	if !ok {
		return
	}
	limit := intParam(r, "limit", 10)
	if limit > maxPreviewRows {
		limit = maxPreviewRows
	}
	rows, err := req.toDataset().Preview(seed, start, limit)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, previewResp{Total: req.Count, Columns: req.columns(), Rows: rows})
}

// datasetDraw reads the two numbers a generated dataset is drawn from. They are
// read off the sandbox rather than taken from the request so that what a preview
// shows and what a run carries cannot come apart.
func (s *Service) datasetDraw(w http.ResponseWriter, sess *playground.Session) (int64, time.Time, bool) {
	var (
		seed  int64
		start time.Time
	)
	if !s.ok(w, sess.With(func(sb *playground.Sandbox) error {
		seed, start = sb.Seed(), sb.StartedAt()
		return nil
	})) {
		return 0, time.Time{}, false
	}
	return seed, start, true
}

// generatedCases draws the whole dataset for a run.
//
// The count is checked against the run ceiling before anything is drawn: building
// fifty thousand and one rows only to refuse them is work nobody asked for, and
// the refusal reads the same either way.
func (s *Service) generatedCases(w http.ResponseWriter, sess *playground.Session, g generateReq) ([]map[string]any, bool) {
	if g.Count > maxCasesPerRun {
		httpapi.Error(w, http.StatusBadRequest,
			fmt.Sprintf("a run holds at most %d cases; this description asks for %d", maxCasesPerRun, g.Count))
		return nil, false
	}
	seed, start, ok := s.datasetDraw(w, sess)
	if !ok {
		return nil, false
	}
	rows, err := g.toDataset().Generate(seed, start)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return rows, true
}
