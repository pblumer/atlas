package api

import (
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/userprefs"
)

// localeDir is where the console keeps one message file per language
// (ADR-0497). index.js beside them is the list the
// browser imports, not a language.
const localeDir = "web/locales"

// consoleLocales lists the languages the embedded console carries a message file
// for, sorted. It reads the same directory the browser's catalogue is built from, so
// the language a person may choose is the language the console can show: adding a
// language is adding its file, and no list here has to be kept in step with it.
func consoleLocales() []string {
	entries, err := fs.ReadDir(webFS, localeDir)
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || path.Ext(name) != ".js" || name == "index.js" {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".js"))
	}
	sort.Strings(ids)
	return ids
}

// The languages a person may store as their choice are the console's locales. The
// preference package cannot see the embedded console, so it is told here, once,
// before any request is served; a build whose console carries no locale files keeps
// the package's own list rather than accepting none.
func init() {
	if ids := consoleLocales(); len(ids) > 0 {
		userprefs.Languages = ids
	}
}
