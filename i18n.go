package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Translations. Every piece of text Plots shows goes through T (or Tf for
// formats). The English text is the key; lang/<code>.json maps it to the
// translation. Anything missing from a language falls back to English, so a
// half-finished translation never breaks the app.

//go:embed lang/*.json
var langFS embed.FS

type Language struct {
	Code string // e.g. "fr", "pt-BR"
	Name string // in its own language
}

// allLanguages, in the order the Language setting lists them.
var allLanguages = []Language{
	{"en", "English"},
	{"fr", "Français"},
	{"es", "Español"},
	{"pt-BR", "Português (Brasil)"},
	{"de", "Deutsch"},
	{"zh-CN", "中文（简体）"},
	{"ja", "日本語"},
	{"ru", "Русский"},
}

var (
	langMu   sync.RWMutex
	langCode = "en"
	langMap  map[string]string
)

// Languages returns the languages Plots has translations for.
func Languages() []Language {
	var out []Language
	for _, l := range allLanguages {
		if l.Code == "en" || hasLanguage(l.Code) {
			out = append(out, l)
		}
	}
	return out
}

func hasLanguage(code string) bool {
	_, err := langFS.Open("lang/" + code + ".json")
	return err == nil
}

func loadLanguage(code string) (map[string]string, error) {
	b, err := langFS.ReadFile("lang/" + code + ".json")
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("lang/%s.json: %w", code, err)
	}
	return m, nil
}

// SetLanguage switches the UI language; unknown codes mean English.
func SetLanguage(code string) {
	m, err := loadLanguage(code)
	langMu.Lock()
	defer langMu.Unlock()
	if err != nil || code == "en" {
		langCode, langMap = "en", nil
		return
	}
	langCode, langMap = code, m
}

// CurrentLanguage is the code of the language in use.
func CurrentLanguage() string {
	langMu.RLock()
	defer langMu.RUnlock()
	return langCode
}

// T translates s.
func T(s string) string {
	langMu.RLock()
	defer langMu.RUnlock()
	if t, ok := langMap[s]; ok && strings.TrimSpace(t) != "" {
		return t
	}
	return s
}

// N marks a string for translation without translating it (for tables that
// are translated where they are shown).
func N(s string) string { return s }

// Tf translates a format string, then formats it.
func Tf(format string, args ...any) string { return fmt.Sprintf(T(format), args...) }

// LanguageForLangID maps a Windows LANGID (GetUserDefaultUILanguage) to the
// closest language Plots has, or "en".
func LanguageForLangID(id uint16) string {
	code := map[uint16]string{
		0x0c: "fr", 0x0a: "es", 0x16: "pt-BR", 0x07: "de",
		0x04: "zh-CN", 0x11: "ja", 0x19: "ru",
	}[id&0x3ff]
	if code != "" && hasLanguage(code) {
		return code
	}
	return "en"
}
