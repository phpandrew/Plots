package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// sourceStrings returns every literal passed to T, Tf or N in the app's source.
func sourceStrings(t *testing.T) []string {
	files, _ := filepath.Glob("*.go")
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok || len(ce.Args) == 0 {
				return true
			}
			id, ok := ce.Fun.(*ast.Ident)
			if !ok || (id.Name != "T" && id.Name != "Tf" && id.Name != "N") {
				return true
			}
			if bl, ok := ce.Args[0].(*ast.BasicLit); ok && bl.Kind == token.STRING {
				if s, err := strconv.Unquote(bl.Value); err == nil && strings.TrimSpace(s) != "" {
					seen[s] = true
				}
			}
			return true
		})
	}
	var out []string
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

var verbRe = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

// TestTranslations checks every language file covers every string, has no
// stale entries, and keeps the same format verbs (%s, %d, ...) in order.
// Set PLOTS_I18N_DUMP=file to write the full list of strings as a template.
func TestTranslations(t *testing.T) {
	keys := sourceStrings(t)
	if len(keys) < 100 {
		t.Fatalf("only %d translatable strings found", len(keys))
	}
	if p := os.Getenv("PLOTS_I18N_DUMP"); p != "" {
		m := map[string]string{}
		for _, k := range keys {
			m[k] = ""
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		os.WriteFile(p, b, 0o644)
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	for _, lg := range Languages() {
		if lg.Code == "en" {
			continue
		}
		m, err := loadLanguage(lg.Code)
		if err != nil {
			t.Fatal(err)
		}
		var missing, stale []string
		for _, k := range keys {
			if strings.TrimSpace(m[k]) == "" {
				missing = append(missing, k)
			}
		}
		for k, v := range m {
			if !want[k] {
				stale = append(stale, k)
				continue
			}
			if a, b := verbRe.FindAllString(k, -1), verbRe.FindAllString(v, -1); strings.Join(a, " ") != strings.Join(b, " ") {
				t.Errorf("%s: format verbs differ for %q: %v vs %v", lg.Code, k, a, b)
			}
			if strings.Count(k, "&&") != strings.Count(v, "&&") {
				t.Errorf("%s: && (a literal &) count differs for %q", lg.Code, k)
			}
		}
		if len(missing) > 0 {
			t.Errorf("%s: %d untranslated strings, e.g. %q", lg.Code, len(missing), missing[:min(5, len(missing))])
		}
		if len(stale) > 0 {
			t.Errorf("%s: %d strings no longer used, e.g. %q", lg.Code, len(stale), stale[:min(5, len(stale))])
		}
	}
}

func TestLanguageFallback(t *testing.T) {
	SetLanguage("xx")
	if CurrentLanguage() != "en" || T("Done") != "Done" {
		t.Fatal("unknown language should fall back to English")
	}
	if LanguageForLangID(0x0409) != "en" {
		t.Fatal("en-US")
	}
	if LanguageForLangID(0x0c0c) != "fr" || LanguageForLangID(0x0c0a) != "es" {
		t.Fatal("fr-CA / es-ES")
	}
	SetLanguage("en")
}
