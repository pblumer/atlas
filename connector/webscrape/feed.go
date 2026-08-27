package webscrape

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// FeedEntry is the stable structured result one RSS item or Atom entry produces
// (ADR-0190). Every field is always serialized; a source that omits one leaves it
// empty rather than making Atlas invent data.
type FeedEntry struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	Description string `json:"description"`
	Published   string `json:"published"`
}

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Published   string `xml:"pubDate"`
}

type atomDocument struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string     `xml:"title"`
	Links     []atomLink `xml:"link"`
	Summary   string     `xml:"summary"`
	Content   string     `xml:"content"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

func extractRSS(body io.Reader, maxItems int32) ([]FeedEntry, error) {
	var doc rssDocument
	if err := xml.NewDecoder(body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("webscrape: parse rss: %w", err)
	}
	limit := boundedLen(len(doc.Channel.Items), maxItems)
	out := make([]FeedEntry, 0, limit)
	for i := 0; i < limit; i++ {
		item := doc.Channel.Items[i]
		out = append(out, FeedEntry{
			Title:       strings.TrimSpace(item.Title),
			Link:        strings.TrimSpace(item.Link),
			Description: strings.TrimSpace(item.Description),
			Published:   strings.TrimSpace(item.Published),
		})
	}
	return out, nil
}

func extractAtom(body io.Reader, maxItems int32) ([]FeedEntry, error) {
	var doc atomDocument
	if err := xml.NewDecoder(body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("webscrape: parse atom: %w", err)
	}
	limit := boundedLen(len(doc.Entries), maxItems)
	out := make([]FeedEntry, 0, limit)
	for i := 0; i < limit; i++ {
		entry := doc.Entries[i]
		description := strings.TrimSpace(entry.Summary)
		if description == "" {
			description = strings.TrimSpace(entry.Content)
		}
		published := strings.TrimSpace(entry.Published)
		if published == "" {
			published = strings.TrimSpace(entry.Updated)
		}
		out = append(out, FeedEntry{
			Title:       strings.TrimSpace(entry.Title),
			Link:        atomAlternateLink(entry.Links),
			Description: description,
			Published:   published,
		})
	}
	return out, nil
}

func atomAlternateLink(links []atomLink) string {
	for _, link := range links {
		rel := strings.TrimSpace(link.Rel)
		if rel == "" || strings.EqualFold(rel, "alternate") {
			return strings.TrimSpace(link.Href)
		}
	}
	return ""
}

func boundedLen(length int, maxItems int32) int {
	if maxItems <= 0 || int64(maxItems) >= int64(length) {
		return length
	}
	return int(maxItems)
}
