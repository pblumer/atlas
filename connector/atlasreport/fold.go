package atlasreport

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The answers of the read routes, as far as a report reads them. They are declared
// here rather than imported, because the worker imports nothing of the server; a field
// the server renames reads as zero and the fold tests, which use recorded answers, say
// so.

// InstanceSummaryRow is one row of GET /api/v1/instances/summary.
type InstanceSummaryRow struct {
	ProcessDefKey uint64 `json:"processDefKey"`
	ProcessID     string `json:"processId"`
	Version       int32  `json:"version"`
	Active        int    `json:"active"`
	Completed     int    `json:"completed"`
}

// StatsSummary is one measured time of GET /api/v1/processes/{key}/statistics.
type StatsSummary struct {
	Count     int   `json:"count"`
	P50Millis int64 `json:"p50Millis"`
	P90Millis int64 `json:"p90Millis"`
}

// StatsAnswer is GET /api/v1/processes/{key}/statistics.
type StatsAnswer struct {
	From      int64 `json:"from"`
	To        int64 `json:"to"`
	Instances struct {
		Started  int           `json:"started"`
		Finished int           `json:"finished"`
		Running  int           `json:"running"`
		Duration *StatsSummary `json:"duration"`
	} `json:"instances"`
	Elements []struct {
		ElementID string        `json:"elementId"`
		Runs      int           `json:"runs"`
		Duration  *StatsSummary `json:"duration"`
	} `json:"elements"`
	Scanned   int  `json:"scanned"`
	Truncated bool `json:"truncated"`
}

// IncidentSummaryAnswer is GET /api/v1/incidents/summary. Only the counts are read: an
// incident's message is free text from a Worker, and a report addressed to a group is
// not where it goes.
type IncidentSummaryAnswer struct {
	Total  int `json:"total"`
	Groups []struct {
		ProcessID string `json:"processId"`
		ElementID string `json:"elementId"`
		Count     int    `json:"count"`
	} `json:"groups"`
	GroupsTruncated bool `json:"groupsTruncated"`
	Ungrouped       int  `json:"ungrouped"`
}

// DeliveryAnswer is GET /api/v1/catalog-products/delivery-report: counts and durations
// per product, without names of people (ADR-0353).
type DeliveryAnswer struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Products []struct {
		ItemID         string   `json:"itemId"`
		Name           string   `json:"name"`
		Met            int      `json:"met"`
		Missed         int      `json:"missed"`
		Overdue        int      `json:"overdue"`
		Open           int      `json:"open"`
		LeadMedianDays *float64 `json:"leadMedianDays"`
	} `json:"products"`
	Note string `json:"note"`
}

// words are a report's series names and notes, in the language the task asks for.
type words struct {
	active, completed, started, finished, running, p50, p90, open string
	met, missed, overdue, openLine, leadMedian, other             string
	foldedFmt, truncatedFmt, versionFmt, ungroupedFmt             string
}

var lang = map[string]words{
	"de": {
		active: "laufend", completed: "beendet", started: "gestartet", finished: "beendet", running: "laufend",
		p50: "Median", p90: "90. Perzentil", open: "offen",
		met: "eingehalten", missed: "verpasst", overdue: "überfällig", openLine: "offen", leadMedian: "Median Lieferzeit",
		other:        "Übrige",
		foldedFmt:    "%d weitere sind unter «Übrige» zusammengefasst.",
		truncatedFmt: "Die Statistik ist auf die neuesten %d Instanzen eines Zeitraums begrenzt; mindestens ein Zeitraum hatte mehr.",
		versionFmt:   "Gemessen an Version %d von %s.",
		ungroupedFmt: "%d Incidents sind keinem Prozess zugeordnet, weil die Übersicht ihre Gruppengrenze erreichte; sie sind in der Summe enthalten.",
	},
	"en": {
		active: "running", completed: "finished", started: "started", finished: "finished", running: "running",
		p50: "median", p90: "90th percentile", open: "open",
		met: "met", missed: "missed", overdue: "overdue", openLine: "open", leadMedian: "median lead time",
		other:        "Other",
		foldedFmt:    "%d more are summed up under “Other”.",
		truncatedFmt: "The statistics are capped at the newest %d instances of a period; at least one period held more.",
		versionFmt:   "Measured on version %d of %s.",
		ungroupedFmt: "%d incidents are under no process because the summary reached its group cap; they are in the total.",
	},
}

func wordsFor(language string) words {
	if w, ok := lang[language]; ok {
		return w
	}
	return lang["de"]
}

func newReport(title string, now time.Time) Report {
	return Report{Schema: Schema, Title: title, GeneratedAt: now.UTC().Format(time.RFC3339), Datasets: map[string]Dataset{}}
}

// FoldInstancesSummary answers instances-summary: the running and finished instances per
// process — every version of a process summed — or of one process's versions.
func FoldInstancesSummary(rows []InstanceSummaryRow, process, title, language string, now time.Time) Report {
	w := wordsFor(language)
	r := newReport(title, now)
	var out []row
	var active, completed float64
	if process == "" {
		byProc := map[string]*row{}
		for _, x := range rows {
			rw := byProc[x.ProcessID]
			if rw == nil {
				rw = &row{label: x.ProcessID, values: []*float64{num(0), num(0)}}
				byProc[x.ProcessID] = rw
			}
			*rw.values[0] += float64(x.Active)
			*rw.values[1] += float64(x.Completed)
			active += float64(x.Active)
			completed += float64(x.Completed)
		}
		for _, rw := range byProc {
			out = append(out, *rw)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].label < out[j].label })
	} else {
		for _, x := range rows {
			if x.ProcessID != process {
				continue
			}
			out = append(out, row{label: fmt.Sprintf("v%d", x.Version), values: []*float64{num(float64(x.Active)), num(float64(x.Completed))}})
			active += float64(x.Active)
			completed += float64(x.Completed)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].label < out[j].label })
	}
	d, folded := table("count", []string{w.active, w.completed}, out, sortWhenAll(process), w.other, true)
	r.Datasets["byProcess"] = d
	r.Datasets["active"] = valueSet("count", active)
	r.Datasets["completed"] = valueSet("count", completed)
	if folded > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf(w.foldedFmt, folded))
	}
	return r
}

// sortWhenAll sorts the processes by their running instances when all are shown, and
// keeps one process's versions in their order.
func sortWhenAll(process string) int {
	if process == "" {
		return 0
	}
	return -1
}

// LatestVersion is the definition a process-statistics task measures: the newest
// version of the process.
func LatestVersion(rows []InstanceSummaryRow, process string) (InstanceSummaryRow, bool) {
	var best InstanceSummaryRow
	found := false
	for _, x := range rows {
		if x.ProcessID == process && (!found || x.Version > best.Version) {
			best, found = x, true
		}
	}
	return best, found
}

// Window is one period a statistics read covers, with its label.
type Window struct {
	From, To time.Time
	Label    string
}

// Windows cuts the period ending at `to` into n buckets of one day, week or month, the
// oldest first. A week starts on Monday and is labelled with its ISO week; a month with
// its year and month; a day with its date.
func Windows(to time.Time, n int, bucket, language string) []Window {
	end := to
	out := make([]Window, n)
	for i := n - 1; i >= 0; i-- {
		var start time.Time
		switch bucket {
		case "day":
			start = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
			if !start.Before(end) {
				start = start.AddDate(0, 0, -1)
			}
		case "month":
			start = time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, end.Location())
			if !start.Before(end) {
				start = start.AddDate(0, -1, 0)
			}
		default: // week
			day := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
			offset := (int(day.Weekday()) + 6) % 7
			start = day.AddDate(0, 0, -offset)
			if !start.Before(end) {
				start = start.AddDate(0, 0, -7)
			}
		}
		out[i] = Window{From: start, To: end, Label: label(start, bucket, language)}
		end = start
	}
	return out
}

func label(t time.Time, bucket, language string) string {
	switch bucket {
	case "day":
		return t.Format("2006-01-02")
	case "month":
		return t.Format("2006-01")
	default:
		_, wk := t.ISOWeek()
		if language == "en" {
			return fmt.Sprintf("W%02d", wk)
		}
		return fmt.Sprintf("KW %02d", wk)
	}
}

// FoldStatistics answers process-statistics. Over one period it reports the instances
// started, finished and running and each element's median and 90th-percentile duration;
// cut into buckets it reports the instances started and finished per bucket.
func FoldStatistics(def InstanceSummaryRow, windows []Window, answers []StatsAnswer, title, language string, now time.Time) Report {
	w := wordsFor(language)
	r := newReport(title, now)
	if len(answers) == 0 {
		return r
	}
	r.Period = &Period{
		From: time.Unix(0, answers[0].From).UTC().Format(time.RFC3339),
		To:   time.Unix(0, answers[len(answers)-1].To).UTC().Format(time.RFC3339),
	}
	r.Notes = append(r.Notes, fmt.Sprintf(w.versionFmt, def.Version, def.ProcessID))
	truncated := false
	for _, a := range answers {
		truncated = truncated || a.Truncated
	}
	if len(windows) > 1 {
		rows := make([]row, len(answers))
		for i, a := range answers {
			rows[i] = row{label: windows[i].Label, values: []*float64{num(float64(a.Instances.Started)), num(float64(a.Instances.Finished))}}
		}
		d, _ := table("count", []string{w.started, w.finished}, rows, -1, w.other, true)
		r.Datasets["overTime"] = d
		last := answers[len(answers)-1]
		started := valueSet("count", float64(last.Instances.Started))
		if len(answers) > 1 {
			started.Previous = num(float64(answers[len(answers)-2].Instances.Started))
		}
		r.Datasets["started"] = started
	} else {
		a := answers[0]
		r.Datasets["started"] = valueSet("count", float64(a.Instances.Started))
		r.Datasets["finished"] = valueSet("count", float64(a.Instances.Finished))
	}
	last := answers[len(answers)-1]
	r.Datasets["running"] = valueSet("count", float64(last.Instances.Running))
	var rows []row
	for _, e := range last.Elements {
		if e.Duration == nil || e.Duration.Count == 0 {
			continue
		}
		rows = append(rows, row{label: e.ElementID, values: []*float64{num(float64(e.Duration.P50Millis)), num(float64(e.Duration.P90Millis))}})
	}
	if len(rows) > 0 {
		// Durations are not summed: an "other" row of them would be a number nobody
		// measured, so the folded row holds no value and the note says how many.
		d, folded := table("ms", []string{w.p50, w.p90}, rows, 1, w.other, false)
		r.Datasets["durationByElement"] = d
		if folded > 0 {
			r.Notes = append(r.Notes, fmt.Sprintf(w.foldedFmt, folded))
		}
	}
	if truncated {
		r.Notes = append(r.Notes, fmt.Sprintf(w.truncatedFmt, 10000))
	}
	return r
}

// FoldIncidentSummary answers incident-summary: the open incidents per process, or per
// element of one process.
func FoldIncidentSummary(a IncidentSummaryAnswer, process, title, language string, now time.Time) Report {
	w := wordsFor(language)
	r := newReport(title, now)
	counts := map[string]float64{}
	total := 0.0
	for _, g := range a.Groups {
		key := g.ProcessID
		if process != "" {
			if g.ProcessID != process {
				continue
			}
			key = g.ElementID
		}
		counts[key] += float64(g.Count)
		total += float64(g.Count)
	}
	if process == "" {
		total = float64(a.Total)
	}
	rows := make([]row, 0, len(counts))
	for k, v := range counts {
		rows = append(rows, row{label: k, values: []*float64{num(v)}})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].label < rows[j].label })
	name := "byProcess"
	if process != "" {
		name = "byElement"
	}
	if len(rows) > 0 {
		d, folded := table("count", []string{w.open}, rows, 0, w.other, true)
		r.Datasets[name] = d
		if folded > 0 {
			r.Notes = append(r.Notes, fmt.Sprintf(w.foldedFmt, folded))
		}
	}
	r.Datasets["openIncidents"] = valueSet("count", total)
	if a.GroupsTruncated && process == "" && a.Ungrouped > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf(w.ungroupedFmt, a.Ungrouped))
	}
	return r
}

// FoldDelivery answers delivery-report: the positions per product that met, missed, are
// overdue on or are still open against their delivery promise, and the median lead time.
func FoldDelivery(a DeliveryAnswer, title, language string, now time.Time) Report {
	w := wordsFor(language)
	r := newReport(title, now)
	if a.From != "" || a.To != "" {
		r.Period = &Period{From: a.From, To: a.To}
	}
	var counts, lead []row
	var met, measured float64
	for _, p := range a.Products {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			name = p.ItemID
		}
		counts = append(counts, row{label: name, values: []*float64{
			num(float64(p.Met)), num(float64(p.Missed)), num(float64(p.Overdue)), num(float64(p.Open)),
		}})
		lead = append(lead, row{label: name, values: []*float64{p.LeadMedianDays}})
		met += float64(p.Met)
		measured += float64(p.Met + p.Missed + p.Overdue)
	}
	sort.SliceStable(counts, func(i, j int) bool { return counts[i].label < counts[j].label })
	sort.SliceStable(lead, func(i, j int) bool { return lead[i].label < lead[j].label })
	if len(counts) > 0 {
		d, folded := table("count", []string{w.met, w.missed, w.overdue, w.openLine}, counts, -1, w.other, true)
		r.Datasets["deliveryByProduct"] = d
		dl, _ := table("days", []string{w.leadMedian}, lead, -1, w.other, false)
		r.Datasets["leadTimeByProduct"] = dl
		if folded > 0 {
			r.Notes = append(r.Notes, fmt.Sprintf(w.foldedFmt, folded))
		}
	}
	if measured > 0 {
		share := valueSet("percent", float64(int(met/measured*1000+0.5))/10)
		r.Datasets["metShare"] = share
	}
	if strings.TrimSpace(a.Note) != "" {
		r.Notes = append(r.Notes, strings.TrimSpace(a.Note))
	}
	return r
}
