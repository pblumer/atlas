package compiler

// WebScrapeFormat selects the document representation a web-scraping connector
// extracts (ADR-0190). It is compiled from the model and carried as a compact value
// in ConnectorTaskDetail so the worker never infers a representation from runtime
// content. Zero is HTML deliberately: ConnectorTaskDetail values produced by code
// written before ADR-0190 therefore keep their existing HTML semantics.
type WebScrapeFormat uint8

const (
	// WebScrapeFormatHTML preserves ADR-0118's CSS-selector extraction.
	WebScrapeFormatHTML WebScrapeFormat = iota
	// WebScrapeFormatRSS extracts RSS 2.0 items into structured feed entries.
	WebScrapeFormatRSS
	// WebScrapeFormatAtom extracts Atom entries into structured feed entries.
	WebScrapeFormatAtom
)

// String returns the model token for a compiled web-scrape format.
func (f WebScrapeFormat) String() string {
	switch f {
	case WebScrapeFormatHTML:
		return "html"
	case WebScrapeFormatRSS:
		return "rss"
	case WebScrapeFormatAtom:
		return "atom"
	default:
		return ""
	}
}

// WebScrapeFieldConfig is one authored field of a structured HTML scrape
// (ADR-0231). Name is required and unique within a
// task; Selector is optional and relative to the matched item (empty = the item
// itself); Attribute is optional (empty = the element's text).
type WebScrapeFieldConfig struct {
	Name      string
	Selector  string
	Attribute string
}

// WebScrapeExtractionConfig is the ADR-0190 deploy-time configuration used by the
// XML compiler. It extends the original WebScrapeConfig without breaking callers of
// AddWebScrapeConnectorTask: Format is explicit and MaxItems is the deterministic
// first-N bound (0 = unlimited). Fields, AbsoluteLinks and PlainText are the
// structured-extraction additions (ADR-0231); their
// zero values are exactly the pre-existing behavior.
type WebScrapeExtractionConfig struct {
	Url           RestExpr
	Selector      RestExpr
	Attribute     string
	Format        WebScrapeFormat
	MaxItems      int32
	Fields        []WebScrapeFieldConfig
	AbsoluteLinks bool
	PlainText     bool
	Result        string
	Retries       int32
}

// AddWebScrapeExtractionTask adds a web-scraping connector task with the explicit
// extraction format and bound defined by ADR-0190. Existing callers may keep using
// AddWebScrapeConnectorTask, whose zero-value format remains HTML with no bound.
func (b *Builder) AddWebScrapeExtractionTask(cfg WebScrapeExtractionConfig) int32 {
	detail := int32(len(b.connectorTasks))
	b.connectorTasks = append(b.connectorTasks, ConnectorTaskDetail{
		JobType:         b.intern(WebScrapeJobType),
		Connector:       -1,
		Subject:         -1,
		EventType:       -1,
		ClioQuery:       -1,
		ReduceSpec:      -1,
		Method:          -1,
		ResultVar:       b.intern(cfg.Result),
		Auth:            -1,
		Url:             cfg.Url,
		ScrapeSelector:  cfg.Selector,
		ScrapeAttribute: b.intern(cfg.Attribute),
		ScrapeFormat:    cfg.Format,
		ScrapeMaxItems:  cfg.MaxItems,
		// Interned here rather than at the call site so the compiled detail holds
		// indices only: a field list is deploy-time structure, and the worker reads
		// it back through CompiledProcess.Intern like every other scrape string.
		ScrapeFields:        b.internScrapeFields(cfg.Fields),
		ScrapeAbsoluteLinks: cfg.AbsoluteLinks,
		ScrapePlainText:     cfg.PlainText,
		Retries:             cfg.Retries,
	})
	return b.addNode(TypeConnectorTask, detail)
}

// internScrapeFields interns one authored field list. Nil in, nil out: a task with no
// fields keeps the pre-ADR-0231 string-array result.
func (b *Builder) internScrapeFields(fields []WebScrapeFieldConfig) []ScrapeField {
	if len(fields) == 0 {
		return nil
	}
	out := make([]ScrapeField, 0, len(fields))
	for _, f := range fields {
		out = append(out, ScrapeField{
			Name:      b.intern(f.Name),
			Selector:  b.intern(f.Selector),
			Attribute: b.intern(f.Attribute),
		})
	}
	return out
}
