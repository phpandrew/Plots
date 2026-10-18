package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
)

// Plot is one desktop group. Its rectangle is stored relative to the
// monitor it lives on, so plots survive monitor re-arrangement.
// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

type Plot struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Color      string   `json:"color"`   // #RRGGBB
	Opacity    int      `json:"opacity"` // 0-100, background opacity
	Monitor    string   `json:"monitor"` // device name, e.g. \\.\DISPLAY1
	X          int      `json:"x"`       // physical px, relative to monitor top-left
	Y          int      `json:"y"`
	W          int      `json:"w"`
	H          int      `json:"h"`
	Items      []string `json:"items,omitempty"` // legacy single list; migrated into Tabs
	Tabs       []*Tab   `json:"tabs"`
	ActiveTab  int      `json:"activeTab"`
	Collapsed  bool     `json:"collapsed"`            // rolled up to the title bar
	Scroll     int      `json:"scroll"`               // first visible row when the plot scrolls
	Folder     string   `json:"folder,omitempty"`     // live folder: show this folder's contents
	FolderSort string   `json:"folderSort,omitempty"` // "name" (default), "date", "type", "size"
	IconSize   int      `json:"iconSize,omitempty"`   // live folder icon size in px at 100% (0 = same as the desktop)
}

type Config struct {
	Version            int      `json:"version"`
	Plots              []*Plot  `json:"plots"`
	InboxPlot          string   `json:"inboxPlot"` // plot that receives new items
	DoubleClickHide    bool     `json:"doubleClickHide"`
	StartWithWindows   bool     `json:"startWithWindows"`
	MoveDownloads      bool     `json:"moveDownloads"`
	DownloadsDir       string   `json:"downloadsDir"`
	ManageDesktopFlags bool     `json:"manageDesktopFlags"` // keep auto-arrange / align-to-grid off
	IconOffsetX        int      `json:"iconOffsetX"`
	IconOffsetY        int      `json:"iconOffsetY"`
	TitleSize          int      `json:"titleSize"` // title font size in pt
	TitleFont          string   `json:"titleFont"` // font display name ("" = Segoe UI Semibold)
	TitleColor         string   `json:"titleColor"`
	HeaderStyle        int      `json:"headerStyle"` // HeaderShade, HeaderSame, HeaderCustom, HeaderNone
	HeaderColor        string   `json:"headerColor"`
	UniformLook        bool     `json:"uniformLook"` // same colour & opacity for every plot
	UniformColor       string   `json:"uniformColor"`
	UniformOpacity     int      `json:"uniformOpacity"`
	Hidden             bool     `json:"hidden"`
	CornerRadius       int      `json:"cornerRadius"` // px at 100% scale, 0 = square
	Padding            int      `json:"padding"`      // icon inset inside plots, px at 100% scale
	SnapToGrid         bool     `json:"snapToGrid"`
	GrowToFit          bool     `json:"growToFit"` // grow plots instead of scrolling
	Rules              []*Rule  `json:"rules"`
	AutoUpdateCheck    bool     `json:"autoUpdateCheck"`
	LastUpdateCheck    int64    `json:"lastUpdateCheck"`
	PollMs             int      `json:"pollMs"`      // how often the desktop is checked, 100-500 ms
	DirectEdit         bool     `json:"directEdit"`  // move/resize plots without edit mode
	PanelMode          string   `json:"panelMode"`   // "", "slwa", "plain", "ulw" (how panels are drawn on the desktop layer)
	Known              []string `json:"known"`       // every desktop item seen, to detect new ones across restarts
	BackupEvery        string   `json:"backupEvery"` // "", "daily", "weekly", "monthly"
	BackupDir          string   `json:"backupDir"`
	BackupKeep         int      `json:"backupKeep"` // how many scheduled backups to keep
	LastBackup         int64    `json:"lastBackup"` // unix seconds
	PeekEnabled        bool     `json:"peekEnabled"`
	PeekKey            int      `json:"peekKey"`  // virtual-key code
	PeekMods           int      `json:"peekMods"` // MOD_ALT 1, MOD_CONTROL 2, MOD_SHIFT 4, MOD_WIN 8
	TintMode           int      `json:"tintMode"` // live-folder icon tint: TintOff, TintPlot, TintCustom
	TintColor          string   `json:"tintColor"`
	TintStrength       int      `json:"tintStrength"` // 0-100
	FadeIdle           bool     `json:"fadeIdle"`     // plots rest faded and come into focus on hover
	FadeLevel          int      `json:"fadeLevel"`    // resting strength, 10-90 %
	FadeIcons          bool     `json:"fadeIcons"`    // fade the desktop icons too (all together)
	Language           string   `json:"language"`     // UI language code, "" = follow Windows
}

const (
	TintOff = iota
	TintPlot
	TintCustom
)

func DefaultConfig() *Config {
	return &Config{
		Version:            1,
		ManageDesktopFlags: true,
		SnapToGrid:         true,
		Padding:            10,
		CornerRadius:       8,
		TitleColor:         "#FFFFFF",
		HeaderColor:        "#202020",
		UniformColor:       "#2B2B2B",
		UniformOpacity:     60,
		DirectEdit:         true,
		PollMs:             300,
		AutoUpdateCheck:    true,
		TitleSize:          11,
		BackupKeep:         10,
		PeekEnabled:        true,
		PeekKey:            0x20,  // Space
		PeekMods:           2 | 1, // Ctrl+Alt
		TintColor:          "#4FC3F7",
		TintStrength:       60,
		FadeLevel:          35,
		FadeIcons:          true,
	}
}

func (c *Config) Plot(id string) *Plot {
	for _, p := range c.Plots {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (c *Config) RemovePlot(id string) {
	out := c.Plots[:0]
	for _, p := range c.Plots {
		if p.ID != id {
			out = append(out, p)
		}
	}
	c.Plots = out
	if c.InboxPlot == id {
		c.InboxPlot = ""
	}
}

func NewID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

var palette = []string{"#1E6B1E", "#3B2A8C", "#6B1E1E", "#3A3A3A", "#0E5A7A", "#7A4A0E", "#5A1E6B", "#1E5A5A"}

func (c *Config) NextColor() string {
	return palette[len(c.Plots)%len(palette)]
}

func ParseHex(s string) color.NRGBA {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	var r, g, b uint8
	if len(s) == 6 {
		fmt.Sscanf(s, "%02x%02x%02x", &r, &g, &b)
	} else {
		r, g, b = 40, 40, 40
	}
	return color.NRGBA{r, g, b, 255}
}

func ToHex(c color.NRGBA) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, cfg); err != nil {
		// keep a copy of the broken file so nothing is lost
		os.WriteFile(path+".broken", b, 0o644)
		return DefaultConfig(), err
	}
	if cfg.PollMs < 100 || cfg.PollMs > 500 {
		cfg.PollMs = 300
	}
	if cfg.PeekKey == 0 {
		cfg.PeekKey, cfg.PeekMods = 'P', 3
	}
	if cfg.FadeLevel < 10 || cfg.FadeLevel > 90 {
		cfg.FadeLevel = 35
	}
	if cfg.TintColor == "" {
		cfg.TintColor = "#4FC3F7"
	}
	if cfg.BackupKeep <= 0 {
		cfg.BackupKeep = 10
	}
	if cfg.TitleSize <= 0 {
		cfg.TitleSize = 11
	}
	for _, p := range cfg.Plots {
		p.Norm()
		if p.ID == "" {
			p.ID = NewID()
		}
		if p.Opacity < 0 || p.Opacity > 100 {
			p.Opacity = 60
		}
	}
	return cfg, nil
}

func SaveConfig(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
