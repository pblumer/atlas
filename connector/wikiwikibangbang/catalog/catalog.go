// Package catalog is the wikiwikibangbang Worker's operation table
// (ADR-draft-wikiwikibangbang-worker): which values each operation needs and takes, what
// it returns, and the checks a deploy and a run share.
//
// It imports nothing of atlas's, for the reason connector/carddav/catalog gives: the
// compiler reads these rows to refuse a model at deploy, and connector/wikiwikibangbang
// reads the same rows to build its calls, so there is one table and nothing to keep in
// step.
//
// # The operations
//
// They are spelled like the Jira Worker's (ADR-0201) where the two systems mean the same
// thing — create, get, update, transition, comment, query — and two belong to the wiki
// alone: read a page as Markdown, and extend a page at its end. That is not a claim that
// the two Worker Types are interchangeable; ADR-0480 keeps such a claim for a contract
// that has been shown, and the record lists where they differ.
package catalog

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Result is what an operation hands back to a result variable.
type Result int

const (
	// ResultNone marks an operation that returns nothing worth keeping. No operation
	// of this Worker Type is one today; the value is the zero of the type.
	ResultNone Result = iota
	// ResultItem is one item: id, key, type, space, parent, title, state, properties,
	// labels and version — and its body, for get-item only.
	ResultItem
	// ResultItems is a list of items without their bodies, empty when nothing matched.
	ResultItems
	// ResultText is a page as Markdown text.
	ResultText
	// ResultComment is one comment: id, author, created and text.
	ResultComment
)

// Areas are the groups the Modeler lists operations under, in display order.
var Areas = []string{"item", "page"}

// The values a model authors, by the attribute that carries them. Two of them stand for
// a pair of attributes, of which a model sets one: properties is the inline object or
// propertiesVariable, and body is text or blocks.
const (
	AttrItem       = "item"
	AttrSpace      = "space"
	AttrItemType   = "itemType"
	AttrParent     = "parent"
	AttrTitle      = "title"
	AttrProperties = "properties"
	AttrMatchField = "matchField"
	AttrLabels     = "labels"
	AttrBody       = "body"
	AttrTransition = "transition"
	AttrComment    = "comment"
	AttrQuery      = "query"
)

// Attributes lists every value an operation may need or take, in the order a message
// names them.
var Attributes = []string{
	AttrItem, AttrSpace, AttrItemType, AttrParent, AttrTitle, AttrProperties, AttrMatchField,
	AttrLabels, AttrBody, AttrTransition, AttrComment, AttrQuery,
}

// Bounds holds an operation's maxResults: the value a model gets when it authors none,
// and the most it may author.
type Bounds struct {
	Default int32
	Max     int32
}

// Op is one operation a model can author.
type Op struct {
	// Area groups the operation in the Modeler (one of [Areas]).
	Area string
	// Label describes the operation in an error message.
	Label string
	// Needs are the values the operation cannot run without, Takes the ones it may use.
	Needs []string
	Takes []string
	// NeedsOneTaken marks update-item: it needs at least one of the values it takes,
	// because an update that changes nothing is a model mistake.
	NeedsOneTaken bool
	// MaxResults bounds the operation's list, and is the zero value on every operation
	// that returns none.
	MaxResults Bounds
	// Result is what the operation returns; NeedsResult that it is a read whose answer
	// a model must keep.
	Result      Result
	NeedsResult bool
}

// ops is the operation table. There is no assign-item: in the wiki the person a task is
// assigned to is a property of its type, under the type's own name, so assigning is
// update-item. Spaces, types, permissions, attachments and live editing are not steps a
// process takes.
var ops = map[string]Op{
	"create-item": {
		Area: "item", Label: "create an item",
		Needs:  []string{AttrSpace, AttrItemType, AttrTitle},
		Takes:  []string{AttrParent, AttrProperties, AttrMatchField, AttrBody},
		Result: ResultItem,
	},
	"get-item": {
		Area: "item", Label: "read an item",
		Needs:  []string{AttrItem},
		Result: ResultItem, NeedsResult: true,
	},
	"update-item": {
		Area: "item", Label: "update an item",
		Needs:         []string{AttrItem},
		Takes:         []string{AttrTitle, AttrProperties, AttrLabels},
		NeedsOneTaken: true,
		Result:        ResultItem,
	},
	"transition-item": {
		Area: "item", Label: "move an item through its workflow",
		Needs:  []string{AttrItem, AttrTransition},
		Result: ResultItem,
	},
	"add-comment": {
		Area: "item", Label: "comment on an item",
		Needs:  []string{AttrItem, AttrComment},
		Result: ResultComment,
	},
	"query-items": {
		Area: "item", Label: "query items",
		Needs:      []string{AttrQuery},
		MaxResults: Bounds{Default: 100, Max: 1000},
		Result:     ResultItems, NeedsResult: true,
	},
	"read-page": {
		Area: "page", Label: "read a page as Markdown",
		Needs:  []string{AttrItem},
		Result: ResultText, NeedsResult: true,
	},
	"append-to-page": {
		Area: "page", Label: "append to a page",
		Needs:  []string{AttrItem, AttrBody},
		Result: ResultItem,
	},
}

// Lookup returns the operation of that name.
func Lookup(name string) (Op, bool) {
	op, ok := ops[name]
	return op, ok
}

// Names lists the operations, sorted, for the messages that have to say what was
// expected.
func Names() []string {
	out := make([]string, 0, len(ops))
	for name := range ops {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TakesResult reports whether a result variable may be authored on the operation.
func (o Op) TakesResult() bool { return o.Result != ResultNone }

// NeedsAttr reports whether the operation cannot run without the value.
func (o Op) NeedsAttr(attr string) bool { return slices.Contains(o.Needs, attr) }

// TakesAttr reports whether the operation may use the value without needing it.
func (o Op) TakesAttr(attr string) bool { return slices.Contains(o.Takes, attr) }

// Uses reports whether the operation needs or takes the value.
func (o Op) Uses(attr string) bool { return o.NeedsAttr(attr) || o.TakesAttr(attr) }

// IsExpression reports whether a template leaf is a FEEL expression the compiler
// evaluates at call time, whose shape a deploy therefore cannot see.
func IsExpression(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(strings.TrimSpace(s), "=")
}

// refRe is what an item's id or key may look like on its way into a path: the
// characters the wiki mints them from, and nothing a path could be confused by. It is
// the shape api/contentsource holds a reference to for the same wiki.
var refRe = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,200}$`)

// CheckRef checks an item's id or key — the item an operation acts on, or a parent.
// A value with a slash or a dot-dot would address another route of the wiki's API.
func CheckRef(what, ref string) error {
	if !refRe.MatchString(ref) || ref == "." || ref == ".." {
		return fmt.Errorf("wikiwikibangbang: the %s %q is not an item id or key (e.g. OPS-12)", what, clip(ref))
	}
	return nil
}

// CheckFieldName checks a property's name as matchField names it, or as a key of the
// properties object: the wiki's where addresses a property as <name>=<value>, so a
// name holding "=" or a line break cannot be addressed.
func CheckFieldName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("wikiwikibangbang: a property name is empty")
	}
	if len(name) > 200 || strings.ContainsAny(name, "=\r\n") {
		return fmt.Errorf("wikiwikibangbang: the property name %q cannot be addressed: at most 200 characters, no '=' and no line break", clip(name))
	}
	return nil
}

// CheckProperties checks a properties object: at deploy the literal template, where a
// value may still be a FEEL expression, and at call time the evaluated object. Values
// are the wiki's to check against the item's type; here only the names are checked,
// and that matchField, when set, names one of them — a find on a property the task does
// not set would look for an item with no value.
func CheckProperties(props map[string]any, matchField string) error {
	for name := range props {
		if err := CheckFieldName(name); err != nil {
			return err
		}
	}
	if matchField != "" {
		if _, ok := props[matchField]; !ok {
			return fmt.Errorf("wikiwikibangbang: matchField %q names a property the task does not set; set it in properties, it is what finds the item", clip(matchField))
		}
	}
	return nil
}

// CheckBlocks checks a body given as blocks: a non-empty list of objects, each with a
// kind. What a kind may hold is the wiki's to check, against the version it runs.
func CheckBlocks(v any) error {
	list, ok := v.([]any)
	if !ok {
		return fmt.Errorf("wikiwikibangbang: blocks must be a list of blocks, e.g. [{\"kind\": \"paragraph\", \"text\": \"…\"}]")
	}
	if len(list) == 0 {
		return fmt.Errorf("wikiwikibangbang: blocks is an empty list; it would write nothing")
	}
	for i, b := range list {
		obj, ok := b.(map[string]any)
		if !ok {
			return fmt.Errorf("wikiwikibangbang: block %d is not an object with a kind", i+1)
		}
		kind, ok := obj["kind"].(string)
		if !ok || strings.TrimSpace(kind) == "" {
			return fmt.Errorf("wikiwikibangbang: block %d has no kind (paragraph, heading, bullet, …)", i+1)
		}
	}
	return nil
}

// SplitLabels reads labels written as one comma-separated text. Empty entries are
// dropped; the wiki normalises the rest.
func SplitLabels(s string) []string {
	out := []string{}
	for _, l := range strings.Split(s, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// clip shortens a value quoted in an error message.
func clip(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
