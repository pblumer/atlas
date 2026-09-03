package compiler

// WebScrapeFormat selects the document representation a web-scraping worker
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

// WebScrapeExtractionConfig is the ADR-0190 deploy-time configuration used by the
// XML compiler. It extends the original WebScrapeConfig without breaking callers of
// AddWebScrapeConnectorTask: Format is explicit and MaxItems is the deterministic
// first-N bound (0 = unlimited).
type WebScrapeExtractionConfig struct {
	Url       RestExpr
	Selector  RestExpr
	Attribute string
	Format    WebScrapeFormat
	MaxItems  int32
	Result    string
	Retries   int32
}

// AddWebScrapeExtractionTask adds a web-scraping task with the explicit
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
		Retries:         cfg.Retries,
	})
	return b.addNode(TypeConnectorTask, detail)
}
