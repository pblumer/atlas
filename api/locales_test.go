package api

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/userprefs"
)

// localeCatalogue returns one locale's message file as text. The tests that hold a
// screen to the catalogue look for `"key":` in it rather than parsing JavaScript: a
// Go test that had to parse JavaScript to check a convention would be the more
// fragile of the two. They run over every locale consoleLocales finds, so a language
// added later is held to the same keys without a test being touched.
func localeCatalogue(t *testing.T, locale string) string {
	t.Helper()
	body, err := os.ReadFile(path.Join(localeDir, locale+".js"))
	if err != nil {
		t.Fatalf("read the %q message file: %v", locale, err)
	}
	return string(body)
}

// localeEntry is one message line of a locale file: `"key": "value",`. Every message
// is one such line; TestEveryLocaleCarriesEveryMessageOfTheDefault refuses a file
// that has anything else outside comments, so this reading is the whole file.
var localeEntry = regexp.MustCompile(`^\s*"([^"\\]+)":\s*"((?:[^"\\]|\\.)*)",?\s*$`)

// localeMessages reads a locale file into key → value, and reports any line that is
// neither a message, a comment, blank, nor the module's opening and closing line.
func localeMessages(t *testing.T, locale string) (map[string]string, []string) {
	t.Helper()
	msgs := map[string]string{}
	var stray []string
	inBody := false
	for _, line := range strings.Split(localeCatalogue(t, locale), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "//"):
		case trimmed == "export default {":
			inBody = true
		case trimmed == "};":
			inBody = false
		case inBody && localeEntry.MatchString(line):
			m := localeEntry.FindStringSubmatch(line)
			if _, dup := msgs[m[1]]; dup {
				stray = append(stray, "duplicate key "+m[1])
			}
			msgs[m[1]] = m[2]
		default:
			stray = append(stray, line)
		}
	}
	return msgs, stray
}

var placeholder = regexp.MustCompile(`\{(\w+)\}`)

// placeholders is the set of {names} a message fills, sorted.
func placeholders(msg string) []string {
	var names []string
	for _, m := range placeholder.FindAllStringSubmatch(msg, -1) {
		if !slices.Contains(names, m[1]) {
			names = append(names, m[1])
		}
	}
	slices.Sort(names)
	return names
}

// pluralCategories are the forms Intl.PluralRules can answer besides one and other.
// A locale may carry them for a counted message the default has, because its
// language needs them; it must still carry the default's .one and .other.
var pluralCategories = []string{"zero", "two", "few", "many"}

// TestEveryLocaleCarriesEveryMessageOfTheDefault is what makes an incomplete language
// impossible to ship (ADR-draft-console-locales-one-file-each). A missing message
// renders as its key by design (ADR-0267), so a locale that lacks one would show ids
// on a screen; this refuses it instead. Every locale carries every key German does,
// with the same {placeholders} — a placeholder a translation dropped is a value the
// reader never sees, one it invented is printed raw — and no key German does not,
// except the extra plural forms its own language needs.
func TestEveryLocaleCarriesEveryMessageOfTheDefault(t *testing.T) {
	de, stray := localeMessages(t, "de")
	for _, s := range stray {
		t.Errorf("de: a line that is not a message, a comment or the module's frame: %q", s)
	}
	if len(de) < 1000 {
		t.Fatalf("read only %d German messages; the file format must have changed under this test", len(de))
	}
	if de["locale.name"] == "" {
		t.Error(`de: no "locale.name" — the picker lists a language by the name its file gives it`)
	}
	locales := consoleLocales()
	if !slices.Contains(locales, "de") || len(locales) < 2 {
		t.Fatalf("consoleLocales() = %v; want the German default and at least one more", locales)
	}
	for _, locale := range locales {
		if locale == "de" {
			continue
		}
		msgs, stray := localeMessages(t, locale)
		for _, s := range stray {
			t.Errorf("%s: a line that is not a message, a comment or the module's frame: %q", locale, s)
		}
		for key, want := range de {
			got, ok := msgs[key]
			if !ok {
				t.Errorf("%s: no message %q (German: %q)", locale, key, want)
				continue
			}
			if strings.TrimSpace(got) == "" {
				t.Errorf("%s: message %q is empty", locale, key)
			}
			if !slices.Equal(placeholders(got), placeholders(want)) {
				t.Errorf("%s: message %q fills %v, German fills %v", locale, key, placeholders(got), placeholders(want))
			}
		}
		for key, got := range msgs {
			if _, ok := de[key]; ok {
				continue
			}
			dot := strings.LastIndex(key, ".")
			stem, form := key[:max(dot, 0)], key[dot+1:]
			other, counted := de[stem+".other"]
			if !counted || !slices.Contains(pluralCategories, form) {
				t.Errorf("%s: message %q is not one German has", locale, key)
				continue
			}
			if !slices.Equal(placeholders(got), placeholders(other)) {
				t.Errorf("%s: plural form %q fills %v, German's %q fills %v", locale, key, placeholders(got), stem+".other", placeholders(other))
			}
		}
	}
}

// TestTheLocaleListIsTheLocaleFiles holds the three places a language is named to one
// another: the files under web/locales, the list the browser imports
// (locales/index.js), and the languages the server lets a person choose. A file the
// list does not import is a language nobody can pick; a list entry without a file is
// a blank console, because the browser refuses the whole module graph on a missing
// import.
func TestTheLocaleListIsTheLocaleFiles(t *testing.T) {
	files := consoleLocales()
	index, err := os.ReadFile(path.Join(localeDir, "index.js"))
	if err != nil {
		t.Fatalf("read locales/index.js: %v", err)
	}
	imports := regexp.MustCompile(`(?m)^import (\w+) from "\./(\w+)\.js";$`)
	var imported []string
	for _, m := range imports.FindAllStringSubmatch(string(index), -1) {
		if m[1] != m[2] {
			t.Errorf("locales/index.js imports %s.js as %q; name it after its file, which is the locale id", m[2], m[1])
		}
		imported = append(imported, m[2])
	}
	exported := regexp.MustCompile(`export const CATALOGS = \{([^}]*)\};`).FindStringSubmatch(string(index))
	if exported == nil {
		t.Fatal("locales/index.js does not export CATALOGS as an object of the imported locales")
	}
	var listed []string
	for _, id := range strings.Split(exported[1], ",") {
		if id = strings.TrimSpace(id); id != "" {
			listed = append(listed, id)
		}
	}
	sorted := func(s []string) []string { c := slices.Clone(s); slices.Sort(c); return c }
	if !slices.Equal(sorted(imported), files) {
		t.Errorf("locales/index.js imports %v; the directory holds %v", imported, files)
	}
	if !slices.Equal(sorted(listed), files) {
		t.Errorf("locales/index.js lists %v in CATALOGS; the directory holds %v", listed, files)
	}
	if len(listed) == 0 || listed[0] != "de" {
		t.Errorf("CATALOGS lists %v; the default, de, comes first", listed)
	}
	if !slices.Equal(userprefs.Languages, files) {
		t.Errorf("userprefs.Languages = %v; a person may choose exactly the locales the console carries, %v",
			userprefs.Languages, files)
	}
}

// i18nImport is a module's import of the catalogue's lookups, with any renaming:
// `import { t as tr, plural as trPlural } from "./i18n.js"`.
var i18nImport = regexp.MustCompile(`(?m)^import \{([^}]*)\} from "\./i18n\.js";`)

// TestEveryMessageTheConsoleLooksUpExists holds every module that imports the
// catalogue to it: each key it looks up by name, under whatever name it imported
// t and plural as, is a German message — or, for a plural, has German's .one and
// .other. The per-screen tests check keys a screen holds in tables too; this one
// makes sure no direct lookup anywhere renders as a raw id, so a screen translated
// later is covered the moment it imports the catalogue.
func TestEveryMessageTheConsoleLooksUpExists(t *testing.T) {
	de, _ := localeMessages(t, "de")
	files, err := filepath.Glob("web/*.js")
	if err != nil {
		t.Fatal(err)
	}
	importers, lookups := 0, 0
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(body)
		m := i18nImport.FindStringSubmatch(src)
		if m == nil {
			continue
		}
		importers++
		var direct, counted []string
		for _, spec := range strings.Split(m[1], ",") {
			fields := strings.Fields(spec)
			if len(fields) == 0 {
				continue
			}
			name, alias := fields[0], fields[len(fields)-1]
			switch name {
			case "t":
				direct = append(direct, alias)
			case "plural":
				counted = append(counted, alias)
			}
		}
		check := func(alias string, isPlural bool) {
			call := regexp.MustCompile(`(?:^|[^\w$.])` + regexp.QuoteMeta(alias) + `\("([^"]+)"`)
			for _, c := range call.FindAllStringSubmatch(src, -1) {
				key := c[1]
				// A key ending in a dot is the prefix of a family completed by an id
				// at run time (`t("feel.target." + kind)`); the screen's own test
				// holds those ids to the catalogue.
				if strings.HasSuffix(key, ".") {
					continue
				}
				lookups++
				if isPlural {
					if _, ok := de[key+".one"]; !ok {
						t.Errorf("%s counts with %q, which has no German .one", file, key)
					}
					if _, ok := de[key+".other"]; !ok {
						t.Errorf("%s counts with %q, which has no German .other", file, key)
					}
					continue
				}
				if _, ok := de[key]; !ok {
					t.Errorf("%s looks up %q, which German does not have", file, key)
				}
			}
		}
		for _, a := range direct {
			check(a, false)
		}
		for _, a := range counted {
			check(a, true)
		}
	}
	if importers < 20 || lookups < 500 {
		t.Fatalf("found %d modules importing the catalogue and %d lookups; the scan no longer matches how modules import it", importers, lookups)
	}
}
