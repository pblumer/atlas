package wikiwikibangbang

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/wikiwikibangbang/catalog"
)

// itemKeys are the keys of the item a model sees, every one present: what the wiki says
// about an item that a process acts on, and nothing it says for its own UI. The wiki may
// add fields to its answer; a model's item does not change with them.
var itemKeys = []string{"id", "key", "type", "space", "parent", "title", "state", "properties", "labels", "version"}

// summary projects the wiki's item onto [itemKeys], with its body when withBody.
func summary(it map[string]any, withBody bool) map[string]any {
	out := make(map[string]any, len(itemKeys)+1)
	for _, k := range itemKeys {
		v, ok := it[k]
		if !ok || v == nil {
			switch k {
			case "properties":
				v = map[string]any{}
			case "labels":
				v = []any{}
			case "version":
				v = 0
			default:
				v = ""
			}
		}
		out[k] = v
	}
	if withBody {
		body, _ := it["body"].([]any)
		if body == nil {
			body = []any{}
		}
		out["body"] = body
	}
	return out
}

// itemAnswer is the wiki's {"item": …} envelope.
type itemAnswer struct {
	Item map[string]any `json:"item"`
}

func (c *Client) getItem(ctx context.Context, ref string) (map[string]any, error) {
	var a itemAnswer
	if err := c.call(ctx, "read item "+ref, http.MethodGet, itemPath(ref), nil, nil, &a); err != nil {
		return nil, err
	}
	if a.Item == nil {
		return nil, fmt.Errorf("wikiwikibangbang: read item %s: the wiki's answer holds no item", ref)
	}
	return a.Item, nil
}

// checkJob checks every authored value an operation needs before the wiki is reached,
// so a refused job has changed nothing.
func checkJob(j Job, op catalog.Op) error {
	missing := func(what string) error {
		return fmt.Errorf("wikiwikibangbang: %s: the %s evaluated to nothing; set the variable it reads before this task", j.Operation, what)
	}
	if op.NeedsAttr(catalog.AttrItem) {
		if strings.TrimSpace(j.Item) == "" {
			return missing("item")
		}
		if err := catalog.CheckRef("item", strings.TrimSpace(j.Item)); err != nil {
			return err
		}
	}
	for _, v := range []struct{ attr, value, what string }{
		{catalog.AttrSpace, j.Space, "space"},
		{catalog.AttrItemType, j.ItemType, "item type"},
		{catalog.AttrTitle, j.Title, "title"},
		{catalog.AttrTransition, j.Transition, "transition"},
		{catalog.AttrComment, j.Comment, "comment"},
		{catalog.AttrQuery, j.Query, "query"},
	} {
		if op.NeedsAttr(v.attr) && strings.TrimSpace(v.value) == "" {
			return missing(v.what)
		}
	}
	if p := strings.TrimSpace(j.Parent); p != "" {
		if err := catalog.CheckRef("parent", p); err != nil {
			return err
		}
	}
	if j.Properties != nil {
		if err := catalog.CheckProperties(j.Properties, ""); err != nil {
			return err
		}
	}
	if j.MatchField != "" {
		if err := catalog.CheckFieldName(j.MatchField); err != nil {
			return err
		}
	}
	if j.Text != "" && j.Blocks != nil {
		return fmt.Errorf("wikiwikibangbang: %s: the task carries both text and blocks; a body is one or the other", j.Operation)
	}
	if j.Blocks != nil {
		if err := catalog.CheckBlocks(j.Blocks); err != nil {
			return err
		}
	}
	if op.NeedsAttr(catalog.AttrBody) && len(bodyOf(j)) == 0 {
		return missing("text or blocks")
	}
	if op.NeedsOneTaken && strings.TrimSpace(j.Title) == "" && j.Properties == nil && j.Labels == nil {
		return fmt.Errorf("wikiwikibangbang: %s: the task changes nothing; it needs a title, properties or labels", j.Operation)
	}
	return nil
}

// bodyOf is the blocks a job writes: its blocks, or its text made into paragraphs.
func bodyOf(j Job) []any {
	if j.Blocks != nil {
		return j.Blocks
	}
	return textToBlocks(j.Text)
}

func (c *Client) run(ctx context.Context, j Job, op catalog.Op, me string) (any, error) {
	switch j.Operation {
	case "get-item":
		it, err := c.getItem(ctx, strings.TrimSpace(j.Item))
		if err != nil {
			return nil, err
		}
		return summary(it, true), nil
	case "read-page":
		ref := strings.TrimSpace(j.Item)
		var md string
		if err := c.call(ctx, "read page "+ref, http.MethodGet, itemPath(ref, "export", "md"), nil, nil, &md); err != nil {
			return nil, err
		}
		return md, nil
	case "create-item":
		return c.create(ctx, j)
	case "update-item":
		return c.update(ctx, j)
	case "transition-item":
		return c.transition(ctx, j)
	case "add-comment":
		return c.comment(ctx, j, me)
	case "append-to-page":
		return c.appendBlocks(ctx, j)
	case "query-items":
		return c.query(ctx, j, op)
	}
	return nil, fmt.Errorf("wikiwikibangbang: operation %q is in the table but not implemented", j.Operation)
}

// sendable turns a properties object into what the wiki is sent: a null value is left
// out — an absent variable must not clear a due date — and "" is sent as null, which
// unsets the property; on a create, where there is nothing to unset, it is left out too.
func sendable(props map[string]any, create bool) map[string]any {
	out := make(map[string]any, len(props))
	for name, v := range props {
		switch {
		case v == nil:
			continue
		case v == "":
			if create {
				continue
			}
			out[name] = nil
		default:
			out[name] = v
		}
	}
	return out
}

func (c *Client) create(ctx context.Context, j Job) (any, error) {
	space, typ := strings.TrimSpace(j.Space), strings.TrimSpace(j.ItemType)
	props := sendable(j.Properties, true)
	var match string
	if j.MatchField != "" {
		v, ok := props[j.MatchField]
		s := ""
		if ok {
			s = fmt.Sprint(v)
		}
		if strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("wikiwikibangbang: create-item: matchField %q names a property the task sets to nothing; it is what finds the item, so it needs a value", j.MatchField)
		}
		match = s
		found, err := c.findBy(ctx, space, typ, j.MatchField, match)
		if err != nil || found != nil {
			return found, err
		}
		if c.afterFind != nil {
			c.afterFind()
		}
	}
	req := map[string]any{"space": space, "type": typ, "title": strings.TrimSpace(j.Title)}
	if p := strings.TrimSpace(j.Parent); p != "" {
		// The wiki takes a parent by its id (its D2); a model may know it by its key.
		parent, err := c.getItem(ctx, p)
		if err != nil {
			return nil, err
		}
		req["parent"] = parent["id"]
	}
	if len(props) > 0 {
		req["properties"] = props
	}
	if body := bodyOf(j); len(body) > 0 {
		req["body"] = body
	}
	var a itemAnswer
	err := c.call(ctx, "create item in "+space, http.MethodPost, "/api/items", nil, req, &a)
	var inv *invalid
	if errors.As(err, &inv) && match != "" {
		// Refused while matchField was set: the likeliest reason is the round this one
		// raced, whose item now holds the unique value. Answer with it if so.
		if found, ferr := c.findBy(ctx, space, typ, j.MatchField, match); ferr == nil && found != nil {
			return found, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return summary(a.Item, false), nil
}

// findBy is the item of that type in that space whose property has the value, nil when
// there is none, and an error when there is more than one: matchField that does not
// identify one item cannot say which a create meant.
func (c *Client) findBy(ctx context.Context, space, typ, field, value string) (map[string]any, error) {
	q := url.Values{"space": {space}, "type": {typ}, "where": {field + "=" + value}, "limit": {"2"}}
	var a struct {
		Items []map[string]any `json:"items"`
	}
	if err := c.call(ctx, "find item by "+field, http.MethodGet, "/api/items", q, nil, &a); err != nil {
		return nil, err
	}
	switch len(a.Items) {
	case 0:
		return nil, nil
	case 1:
		return summary(a.Items[0], false), nil
	}
	return nil, fmt.Errorf("wikiwikibangbang: create-item: more than one %s in %s has %s = %q; matchField must name a property that identifies one item", typ, space, field, value)
}

func (c *Client) update(ctx context.Context, j Job) (any, error) {
	ref := strings.TrimSpace(j.Item)
	var last map[string]any
	put := func(name, path string, body any) error {
		var a itemAnswer
		if err := c.call(ctx, name, http.MethodPut, path, nil, body, &a); err != nil {
			return err
		}
		last = a.Item
		return nil
	}
	if t := strings.TrimSpace(j.Title); t != "" {
		if err := put("set the title of "+ref, itemPath(ref, "title"), map[string]any{"title": t}); err != nil {
			return nil, err
		}
	}
	props := sendable(j.Properties, false)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := put("set "+name+" of "+ref, itemPath(ref, "properties", name), map[string]any{"value": props[name]}); err != nil {
			return nil, err
		}
	}
	if j.Labels != nil {
		labels := *j.Labels
		if labels == nil {
			labels = []string{}
		}
		if err := put("set the labels of "+ref, itemPath(ref, "labels"), map[string]any{"labels": labels}); err != nil {
			return nil, err
		}
	}
	if last == nil {
		// Every value evaluated to null: nothing to change, and the item as it is.
		it, err := c.getItem(ctx, ref)
		if err != nil {
			return nil, err
		}
		last = it
	}
	return summary(last, false), nil
}

// transition moves an item by the transition's name. A refusal is answered by reading
// the item and its type: if the item already stands in the state the transition leads
// to, the task succeeded — in an earlier round of this job, or by a person's hand.
func (c *Client) transition(ctx context.Context, j Job) (any, error) {
	ref, name := strings.TrimSpace(j.Item), strings.TrimSpace(j.Transition)
	var a itemAnswer
	err := c.call(ctx, "move "+ref+" by "+name, http.MethodPost, itemPath(ref, "transitions"), nil, map[string]any{"name": name}, &a)
	var inv *invalid
	if !errors.As(err, &inv) {
		if err != nil {
			return nil, err
		}
		return summary(a.Item, false), nil
	}
	it, gerr := c.getItem(ctx, ref)
	if gerr != nil {
		return nil, err
	}
	target, terr := c.transitionTarget(ctx, fmt.Sprint(it["type"]), name)
	if terr == nil && target != "" && it["state"] == target {
		return summary(it, false), nil
	}
	return nil, err
}

// transitionTarget is the state a type's transition of that name leads to, "" when the
// type has no such transition.
func (c *Client) transitionTarget(ctx context.Context, typ, name string) (string, error) {
	var a struct {
		Types []struct {
			Name     string `json:"name"`
			Workflow *struct {
				Transitions []struct {
					Name string `json:"name"`
					To   string `json:"to"`
				} `json:"transitions"`
			} `json:"workflow"`
		} `json:"types"`
	}
	if err := c.call(ctx, "read the types", http.MethodGet, "/api/types", nil, nil, &a); err != nil {
		return "", err
	}
	for _, t := range a.Types {
		if t.Name != typ || t.Workflow == nil {
			continue
		}
		for _, tr := range t.Workflow.Transitions {
			if tr.Name == name {
				return tr.To, nil
			}
		}
	}
	return "", nil
}

// commentKeys are the keys of the comment a model sees.
var commentKeys = []string{"id", "author", "created", "text"}

func commentOf(m map[string]any) map[string]any {
	out := make(map[string]any, len(commentKeys))
	for _, k := range commentKeys {
		v, ok := m[k]
		if !ok || v == nil {
			v = ""
		}
		out[k] = v
	}
	return out
}

// maxCommentPages bounds how far the redelivery check of add-comment reads to find the
// item's last comment. An item with more comments than that is written to, as a first
// delivery would be.
const maxCommentPages = 50

func (c *Client) comment(ctx context.Context, j Job, me string) (any, error) {
	ref := strings.TrimSpace(j.Item)
	if j.Redelivered {
		last, err := c.lastComment(ctx, ref)
		if err != nil {
			return nil, err
		}
		if last != nil && last["author"] == me && last["text"] == j.Comment {
			return commentOf(last), nil
		}
	}
	var a struct {
		Comment map[string]any `json:"comment"`
	}
	if err := c.call(ctx, "comment on "+ref, http.MethodPost, itemPath(ref, "comments"), nil, map[string]any{"text": j.Comment}, &a); err != nil {
		return nil, err
	}
	return commentOf(a.Comment), nil
}

func (c *Client) lastComment(ctx context.Context, ref string) (map[string]any, error) {
	var last map[string]any
	after := ""
	for page := 0; page < maxCommentPages; page++ {
		q := url.Values{"limit": {"100"}}
		if after != "" {
			q.Set("after", after)
		}
		var a struct {
			Comments []map[string]any `json:"comments"`
			Next     string           `json:"next"`
		}
		if err := c.call(ctx, "read the comments of "+ref, http.MethodGet, itemPath(ref, "comments"), q, nil, &a); err != nil {
			return nil, err
		}
		if n := len(a.Comments); n > 0 {
			last = a.Comments[n-1]
		}
		if a.Next == "" {
			return last, nil
		}
		after = a.Next
	}
	return nil, nil
}

// appendBlocks extends a page: it reads the body, and writes it back with the job's
// blocks after it. Every block read is carried back as it was read, so a kind this
// Worker does not know, or a field the wiki added since, is kept.
//
// A delivered-again job first checks whether its earlier round already wrote: the page
// ends with exactly these blocks. A first delivery never checks, so a process that
// appends the same line twice on purpose gets it twice.
func (c *Client) appendBlocks(ctx context.Context, j Job) (any, error) {
	ref := strings.TrimSpace(j.Item)
	it, err := c.getItem(ctx, ref)
	if err != nil {
		return nil, err
	}
	body, _ := it["body"].([]any)
	add := bodyOf(j)
	if j.Redelivered && endsWith(body, add) {
		return summary(it, false), nil
	}
	blocks := make([]any, 0, len(body)+len(add))
	blocks = append(append(blocks, body...), add...)
	var a itemAnswer
	if err := c.call(ctx, "append to "+ref, http.MethodPut, itemPath(ref, "body"), nil, map[string]any{"blocks": blocks}, &a); err != nil {
		return nil, err
	}
	return summary(a.Item, false), nil
}

// pageLimit is how many items one listing request asks for: the wiki's default page.
const pageLimit = 100

func (c *Client) query(ctx context.Context, j Job, op catalog.Op) (any, error) {
	limit := int(j.MaxResults)
	if limit <= 0 || limit > int(op.MaxResults.Max) {
		// The compiler applied the default and the ceiling; a hand-built job gets the
		// default rather than an unbounded read.
		limit = int(op.MaxResults.Default)
	}
	query := strings.TrimSpace(j.Query)
	tooMany := func() error {
		return fmt.Errorf("wikiwikibangbang: query-items: more than %d items match %q; narrow the query, or raise maxResults (at most %d) — a short list would be a wrong answer", limit, query, op.MaxResults.Max)
	}
	out := []any{}
	after := ""
	for {
		q := url.Values{"query": {query}, "limit": {strconv.Itoa(min(pageLimit, limit+1))}}
		if after != "" {
			q.Set("after", after)
		}
		var a struct {
			Items []map[string]any `json:"items"`
			Next  string           `json:"next"`
			Total struct {
				Exact bool `json:"exact"`
				Value int  `json:"value"`
			} `json:"total"`
		}
		if err := c.call(ctx, "query items", http.MethodGet, "/api/items", q, nil, &a); err != nil {
			return nil, err
		}
		if !c.ignoreTotal && a.Total.Exact && a.Total.Value > limit {
			return nil, tooMany()
		}
		for _, it := range a.Items {
			if len(out) == limit {
				return nil, tooMany()
			}
			out = append(out, summary(it, false))
		}
		if a.Next == "" || len(a.Items) == 0 {
			return out, nil
		}
		after = a.Next
	}
}
