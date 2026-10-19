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
	{N("Documents"), "*.pdf;*.doc;*.docx;*.txt;*.rtf;*.odt;*.md"},
	{N("Spreadsheets"), "*.xls;*.xlsx;*.csv;*.ods"},
	{N("Images"), "*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp;*.svg;*.heic"},
	{N("Design files"), "*.psd;*.ai;*.eps;*.indd;*.xd;*.fig"},
	{N("Shortcuts"), "*.lnk;*.url"},
	{N("Archives"), "*.zip;*.rar;*.7z;*.gz;*.tar"},
	{N("Installers"), "*.exe;*.msi;*.msix"},
	{N("Video"), "*.mp4;*.mov;*.mkv;*.avi;*.webm"},
	{N("Audio"), "*.mp3;*.wav;*.flac;*.m4a"},
	{N("Folders"), "folder"},
}

// tabLabel is a tab's visible name.
func (p *Plot) tabLabel(i int) string {
	if n := strings.TrimSpace(p.Tabs[i].Name); n != "" {
		return n
	}
	if p.Title != "" {
		return p.Title
	}
	return T("Tab")
}

func uniqueName(name string, taken map[string]bool) string {
	n := name
	for i := 2; taken[strings.ToLower(n)]; i++ {
		n = name + " " + itoa(i)
	}
	taken[strings.ToLower(n)] = true
	return n
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

// CanMerge reports whether src can be dropped onto dst as tabs.
func (c *Config) CanMerge(srcID, dstID string) bool {
	src, dst := c.Plot(srcID), c.Plot(dstID)
	return src != nil && dst != nil && src != dst && src.Folder == "" && dst.Folder == "" && !src.Locked && !dst.Locked
}

// MergePlots moves every tab of src into dst (as extra tabs) and removes src.
// Rules and the new-items setting that pointed at src follow its icons.
func (c *Config) MergePlots(srcID, dstID string) bool {
	if !c.CanMerge(srcID, dstID) {
		return false
	}
	src, dst := c.Plot(srcID), c.Plot(dstID)
	src.Norm()
	dst.Norm()
	taken := map[string]bool{}
	for i := range dst.Tabs {
		dst.Tabs[i].Name = uniqueName(dst.tabLabel(i), taken)
	}
	first := len(dst.Tabs)
	rename := map[string]string{} // src tab name (lowercase, "" = first) -> new name
	for i, t := range src.Tabs {
		newName := uniqueName(src.tabLabel(i), taken)
		rename[strings.ToLower(t.Name)] = newName
		if i == 0 {
			rename[""] = newName
		}
		dst.Tabs = append(dst.Tabs, &Tab{Name: newName, Items: t.Items})
	}
	for _, r := range c.Rules {
		if r.PlotID == srcID {
			r.PlotID = dstID
			if n, ok := rename[strings.ToLower(r.Tab)]; ok {
				r.Tab = n
			} else {
				r.Tab = rename[""]
			}
		}
	}
	if c.InboxPlot == srcID {
		c.InboxPlot = dstID
	}
	dst.ActiveTab = first
	c.RemovePlot(srcID)
	return true
}

// SplitTab takes tab i out of plot id and returns it as a new plot (not yet
// placed: the caller sets its position). The source keeps its other tabs.
func (c *Config) SplitTab(id string, i int) *Plot {
	p := c.Plot(id)
	if p == nil || p.Folder != "" || p.Locked {
		return nil
	}
	p.Norm()
	if len(p.Tabs) < 2 || i < 0 || i >= len(p.Tabs) {
		return nil
	}
	t := p.Tabs[i]
	np := &Plot{ID: NewID(), Title: p.tabLabel(i), Color: p.Color, Opacity: p.Opacity, Monitor: p.Monitor,
		W: p.W, H: p.H, Tabs: []*Tab{{Items: t.Items}}}
	for _, r := range c.Rules {
		if r.PlotID == id && (strings.EqualFold(r.Tab, t.Name) || (r.Tab == "" && i == 0)) {
			r.PlotID, r.Tab = np.ID, ""
		}
	}
	p.Tabs = append(p.Tabs[:i], p.Tabs[i+1:]...)
	if p.ActiveTab >= i && p.ActiveTab > 0 {
		p.ActiveTab--
	}
	p.Norm()
	c.Plots = append(c.Plots, np)
	return np
}

// MoveTab reorders a plot's tabs, keeping the moved tab active.
func (p *Plot) MoveTab(from, to int) bool {
	p.Norm()
	if from == to || from < 0 || to < 0 || from >= len(p.Tabs) || to >= len(p.Tabs) {
		return false
	}
	t := p.Tabs[from]
	p.Tabs = append(p.Tabs[:from], p.Tabs[from+1:]...)
	p.Tabs = append(p.Tabs[:to], append([]*Tab{t}, p.Tabs[to:]...)...)
	p.ActiveTab = to
	return true
}
