package main

import (
	"path/filepath"
	"strings"
)

// Tab is one page of a plot. Every plot has at least one tab; a plot with a
// single tab simply shows no tab bar.
type Tab struct {
	Name  string   `json:"name"`
	Items []string `json:"items"`
}

// Rule sends new desktop items whose name matches Pattern to a plot (and tab).
type Rule struct {
	Pattern string `json:"pattern"` // e.g. "*.pdf;*.docx" or "folder"
	PlotID  string `json:"plot"`
	Tab     string `json:"tab,omitempty"` // tab name; "" = first tab
}

// Norm migrates old single-list plots to tabs and clamps indexes.
func (p *Plot) Norm() {
	if len(p.Tabs) == 0 {
		p.Tabs = []*Tab{{Items: p.Items}}
		p.Items = nil
	}
	if p.ActiveTab < 0 || p.ActiveTab >= len(p.Tabs) {
		p.ActiveTab = 0
	}
	if p.Scroll < 0 {
		p.Scroll = 0
	}
}

func (p *Plot) Cur() *Tab {
	p.Norm()
	return p.Tabs[p.ActiveTab]
}

// AllItems returns the items of every tab.
func (p *Plot) AllItems() []string {
	p.Norm()
	var out []string
	for _, t := range p.Tabs {
		out = append(out, t.Items...)
	}
	return out
}

func (p *Plot) TabByName(name string) *Tab {
	p.Norm()
	for _, t := range p.Tabs {
		if strings.EqualFold(t.Name, name) {
			return t
		}
	}
	return p.Tabs[0]
}

// TabNames is the list of tab names, "" for unnamed tabs.
func (p *Plot) TabNames() []string {
	p.Norm()
	out := make([]string, len(p.Tabs))
	for i, t := range p.Tabs {
		out[i] = t.Name
		if out[i] == "" {
			out[i] = p.Title
		}
	}
	return out
}

// SetTabNames renames/adds/removes tabs by position. Items of removed tabs move
// to the last remaining tab so nothing is lost.
func (p *Plot) SetTabNames(names []string) {
	p.Norm()
	var clean []string
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			clean = append(clean, n)
		}
	}
	if len(clean) <= 1 {
		// back to a single tab: merge everything
		all := p.AllItems()
		name := ""
		if len(clean) == 1 {
			name = clean[0]
		}
		p.Tabs = []*Tab{{Name: name, Items: all}}
		p.ActiveTab = 0
		return
	}
	for len(p.Tabs) < len(clean) {
		p.Tabs = append(p.Tabs, &Tab{})
	}
	if len(p.Tabs) > len(clean) {
		keep := p.Tabs[:len(clean)]
		last := keep[len(keep)-1]
		for _, t := range p.Tabs[len(clean):] {
			last.Items = append(last.Items, t.Items...)
		}
		p.Tabs = keep
	}
	for i, n := range clean {
		p.Tabs[i].Name = n
	}
	if p.ActiveTab >= len(p.Tabs) {
		p.ActiveTab = len(p.Tabs) - 1
	}
}

// Match reports whether a desktop item matches the rule's pattern.
func (r *Rule) Match(path string, isDir bool) bool {
	name := strings.ToLower(filepath.Base(path))
	for _, pat := range strings.FieldsFunc(r.Pattern, func(c rune) bool { return c == ';' || c == ',' || c == ' ' }) {
		pat = strings.ToLower(strings.TrimSpace(pat))
		if pat == "" {
			continue
		}
		if pat == "folder" || pat == "folders" || pat == "<folder>" {
			if isDir {
				return true
			}
			continue
		}
		if !strings.ContainsAny(pat, "*?[") && strings.HasPrefix(pat, ".") {
			pat = "*" + pat // ".pdf" means "*.pdf"
		}
		if ok, _ := filepath.Match(pat, name); ok {
			return true
		}
	}
	return false
}

// RulePresets are offered in the settings window.
var RulePresets = []struct{ Name, Pattern string }{
	{"Documents", "*.pdf;*.doc;*.docx;*.txt;*.rtf;*.odt;*.md"},
	{"Spreadsheets", "*.xls;*.xlsx;*.csv;*.ods"},
	{"Images", "*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp;*.svg;*.heic"},
	{"Design files", "*.psd;*.ai;*.eps;*.indd;*.xd;*.fig"},
	{"Shortcuts", "*.lnk;*.url"},
	{"Archives", "*.zip;*.rar;*.7z;*.gz;*.tar"},
	{"Installers", "*.exe;*.msi;*.msix"},
	{"Video", "*.mp4;*.mov;*.mkv;*.avi;*.webm"},
	{"Audio", "*.mp3;*.wav;*.flac;*.m4a"},
	{"Folders", "folder"},
}
