package api

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// A model can name an Atlas extension element correctly and still bind it to the
// wrong namespace, and until the deploy says so nothing does. The compiler reads
// <extensionElements> children by local name alone (`xml:"extensionElements>discordConnector"`
// in compiler/parse.go), so <wrong:discordConnector> compiles, deploys, and runs
// exactly like the right one. The Modeler is not lenient in the same way: bpmn-moddle
// binds the atlas prefix to one uri, and an element outside it is not an Atlas element
// at all — it is dropped on read, or refuses to serialize with "no namespace uri given
// for prefix <ns0>" on save. The engine and the editor therefore disagree about the
// same file, and the author finds out at the moment they try to save work.
//
// Being lenient in the engine is still right: a deployed definition is recompiled on
// recovery, and tightening the parser would turn every model already deployed with a
// stray namespace into a process that no longer loads (I5 — validation gates deploying
// a model, not running one). So the leniency stays and the deploy warns, which is the
// moment somebody is looking (ADR-0158, the same reasoning as connectorWarnings).
//
// This is the runtime half of a rule the repository already enforces at CI time:
// examples/models_test.go's TestAtlasExtensionsUseTheModdleNamespace walks the .bpmn
// files under examples/, conformance/models and postman/ and fails the build on the
// same mistake, deriving the same two facts from the same moddle. That guard is why
// every model Atlas *ships* is bound correctly. It cannot see a model that arrives
// over the deploy endpoint, which is where this one stands — same rule, same source of
// truth, the half of the population the other cannot reach.

// moddleFacts is what the Modeler's own moddle says about Atlas extension elements:
// the namespace they live in, and the element names it can read there.
type moddleFacts struct {
	namespace string
	names     map[string]bool
}

// atlasModdleFile is the descriptor bpmn-js loads to understand Atlas' extensions.
// Deriving the check from this exact file is the point: the question the warning
// answers is "will the Modeler read this back", and this is what the Modeler reads.
const atlasModdleFile = "web/atlas-moddle.json"

// loadAtlasModdle parses the moddle once. It is embedded in the binary, so the read
// cannot start failing later; a failure here is a broken build, and every caller
// degrades to saying nothing rather than to failing a deploy.
var loadAtlasModdle = sync.OnceValues(func() (moddleFacts, error) {
	data, err := webFS.ReadFile(atlasModdleFile)
	if err != nil {
		return moddleFacts{}, fmt.Errorf("read %s: %w", atlasModdleFile, err)
	}
	var doc struct {
		URI string `json:"uri"`
		XML struct {
			TagAlias string `json:"tagAlias"`
		} `json:"xml"`
		Types []struct {
			Name string `json:"name"`
		} `json:"types"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return moddleFacts{}, fmt.Errorf("parse %s: %w", atlasModdleFile, err)
	}
	names := make(map[string]bool, len(doc.Types))
	for _, t := range doc.Types {
		if t.Name == "" {
			continue
		}
		names[moddleTagName(t.Name, doc.XML.TagAlias)] = true
	}
	return moddleFacts{namespace: doc.URI, names: names}, nil
})

// atlasModdle returns the namespace Atlas' extension elements belong in and the set
// of element names the Modeler can read there, both derived from the moddle rather
// than restated here. Add a type to the moddle and this check covers it; rename the
// moddle's uri and this check follows.
func atlasModdle() (string, map[string]bool, error) {
	f, err := loadAtlasModdle()
	return f.namespace, f.names, err
}

// moddleTagName is the XML element name a moddle type serializes as. The Atlas moddle
// declares `"xml": {"tagAlias": "lowerCase"}`, which writes a type with its first letter
// lowercased — DiscordConnector as <atlas:discordConnector>. Any other alias leaves the
// name as written, so changing that declaration changes this check with it rather than
// silently leaving it matching the old spelling.
func moddleTagName(typeName, tagAlias string) string {
	if tagAlias != "lowerCase" || typeName == "" {
		return typeName
	}
	r := []rune(typeName)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// foreignAtlasNamespaceWarnings reports the model's Atlas extension elements that are
// bound to some namespace other than Atlas' own — one sentence per offending namespace,
// listing the elements found in it. The deploy is not refused: the model runs, and the
// author is told the editor will not keep it.
//
// The prefix is bound once at the document root, so a mistake shows up on every element
// at once; grouping by namespace says it once with the full list instead of repeating
// itself per service task. Namespaces and names are sorted so a deploy response reads
// the same way twice.
//
// It is best-effort metadata about a deploy that has already succeeded: a token error
// ends the walk with what was found so far rather than failing anything. The compiler
// has already rejected a document that will not decode.
func foreignAtlasNamespaceWarnings(model []byte) []string {
	ns, names, err := atlasModdle()
	if err != nil || ns == "" || len(names) == 0 {
		return nil
	}
	found := map[string]map[string]bool{}
	dec := xml.NewDecoder(bytes.NewReader(model))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Space == ns || !names[start.Name.Local] {
			continue
		}
		if found[start.Name.Space] == nil {
			found[start.Name.Space] = map[string]bool{}
		}
		found[start.Name.Space][start.Name.Local] = true
	}
	if len(found) == 0 {
		return nil
	}
	spaces := make([]string, 0, len(found))
	for space := range found {
		spaces = append(spaces, space)
	}
	sort.Strings(spaces)
	out := make([]string, 0, len(spaces))
	for _, space := range spaces {
		elems := make([]string, 0, len(found[space]))
		for name := range found[space] {
			elems = append(elems, name)
		}
		sort.Strings(elems)
		out = append(out, foreignNamespaceSentence(space, ns, elems))
	}
	return out
}

// foreignNamespaceSentence words one finding: what is wrong, why it deployed anyway,
// what it costs, and the one edit that fixes it.
func foreignNamespaceSentence(found, want string, elems []string) string {
	where := "in namespace " + strconv.Quote(found)
	fix := "Correct the xmlns:atlas declaration in the model to " + strconv.Quote(want) + "."
	if found == "" {
		where = "in no namespace"
		fix = "Declare xmlns:atlas=" + strconv.Quote(want) + " on the model and prefix them with it."
	}
	return fmt.Sprintf("The model's Atlas extension elements (%s) are %s, not Atlas' own %s. "+
		"The engine reads them either way — it matches an extension element on its name alone — "+
		"but the Modeler does not: opening this process and saving it drops them and everything "+
		"configured on them. %s",
		strings.Join(elems, ", "), where, strconv.Quote(want), fix)
}
