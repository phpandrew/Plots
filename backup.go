package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A Plots export is the whole layout and every setting, wrapped with a small
// header so it can be recognised and checked on import.
type exportFile struct {
	Kind     string  `json:"kind"` // always "plots-layout"
	Format   int     `json:"format"`
	Version  string  `json:"plotsVersion"`
	Exported string  `json:"exported"`
	Config   *Config `json:"config"`
}

const exportKind = "plots-layout"

// ExportConfig serialises cfg for an export or backup. Machine-local state
// (seen-items list, update and backup timestamps) is left out.
func ExportConfig(cfg *Config, now time.Time) ([]byte, error) {
	c := *cfg
	c.Known = nil
	c.LastUpdateCheck = 0
	c.LastBackup = 0
	c.Hidden = false
	return json.MarshalIndent(exportFile{Kind: exportKind, Format: 1, Version: version, Exported: now.Format(time.RFC3339), Config: &c}, "", "  ")
}

// ParseExport reads an export file (or a plain config.json) and returns the
// configuration in it, checked and normalised.
func ParseExport(b []byte) (*Config, error) {
	var f exportFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, errors.New(T("this isn't a Plots layout file"))
	}
	cfg := f.Config
	if f.Kind != exportKind || cfg == nil {
		// a raw config.json works too
		var raw struct {
			Plots json.RawMessage `json:"plots"`
		}
		if json.Unmarshal(b, &raw) != nil || raw.Plots == nil {
			return nil, errors.New(T("this isn't a Plots layout file"))
		}
		cfg = DefaultConfig()
		if err := json.Unmarshal(b, cfg); err != nil {
			return nil, errors.New(T("this isn't a Plots layout file"))
		}
	}
	seen := map[string]bool{}
	out := cfg.Plots[:0]
	for _, p := range cfg.Plots {
		if p == nil {
			continue
		}
		p.Norm()
		if p.ID == "" || seen[p.ID] {
			p.ID = NewID()
		}
		seen[p.ID] = true
		if p.Opacity < 0 || p.Opacity > 100 {
			p.Opacity = 60
		}
		if p.W < 40 || p.H < 30 {
			p.W, p.H = 400, 240
		}
		out = append(out, p)
	}
	cfg.Plots = out
	if cfg.PollMs < 100 || cfg.PollMs > 500 {
		cfg.PollMs = 300
	}
	if cfg.TitleSize <= 0 {
		cfg.TitleSize = 11
	}
	return cfg, nil
}

// ApplyImport replaces cur's layout and settings with imp's, keeping what
// belongs to this PC: the seen-items list (so existing icons aren't treated as
// new), the backup schedule and timestamps, and how panels are drawn.
func ApplyImport(cur, imp *Config) {
	keep := *cur
	*cur = *imp
	cur.Known = keep.Known
	cur.BackupEvery, cur.BackupDir, cur.BackupKeep, cur.LastBackup = keep.BackupEvery, keep.BackupDir, keep.BackupKeep, keep.LastBackup
	cur.LastUpdateCheck = keep.LastUpdateCheck
	cur.PanelMode = keep.PanelMode
	// where downloads come from and whether Plots starts with Windows belong
	// to this PC, and an imported file shouldn't be able to point the
	// downloads mover at some other folder
	cur.MoveDownloads, cur.DownloadsDir, cur.StartWithWindows = keep.MoveDownloads, keep.DownloadsDir, keep.StartWithWindows
	cur.Hidden = false
	if cur.Plot(cur.InboxPlot) == nil {
		cur.InboxPlot = ""
	}
	if cur.BackupKeep <= 0 {
		cur.BackupKeep = 10
	}
}

// LocalFolder reports whether path is a plain folder on a local drive, like
// C:\Users\Me\Projects. Network (\\server\share), device (\\?\, \\.\) and
// relative paths aren't: opening a network folder from someone else's layout
// file would connect to their server and send it your Windows sign-in.
func LocalFolder(path string) bool {
	if len(path) < 3 || path[1] != ':' || (path[2] != '\\' && path[2] != '/') {
		return false
	}
	if c := path[0] | 0x20; c < 'a' || c > 'z' {
		return false
	}
	for _, part := range strings.FieldsFunc(path[3:], func(r rune) bool { return r == '\\' || r == '/' }) {
		if part == ".." {
			return false
		}
	}
	return !strings.ContainsAny(path[2:], "\x00*?\"<>|:") // a second ':' is an alternate data stream
}

// ImportFolders checks the live folders in an imported layout. Plots that
// point at anything but a local folder become ordinary plots; remote, if
// set, also catches drive letters mapped to network shares. It returns the
// live folders kept and the ones dropped.
func ImportFolders(cfg *Config, remote func(string) bool) (kept, dropped []string) {
	for _, p := range cfg.Plots {
		if p.Folder == "" {
			continue
		}
		if LocalFolder(p.Folder) && (remote == nil || !remote(p.Folder)) {
			kept = append(kept, p.Folder)
		} else {
			dropped = append(dropped, p.Folder)
			p.Folder = ""
		}
	}
	return
}

// ------------------------------------------------------------------ scheduled backups

var BackupSchedules = []struct{ Key, Label string }{
	{"", N("Off")},
	{"daily", N("Every day")},
	{"weekly", N("Every week")},
	{"monthly", N("Every month")},
}

// BackupDue reports whether a scheduled backup should run now.
func BackupDue(every string, last, now time.Time) bool {
	if last.IsZero() {
		return every != ""
	}
	switch every {
	case "daily":
		return !now.Before(last.AddDate(0, 0, 1))
	case "weekly":
		return !now.Before(last.AddDate(0, 0, 7))
	case "monthly":
		return !now.Before(last.AddDate(0, 1, 0))
	}
	return false
}

// LastBackupTime is the last scheduled backup (zero if none yet).
func (c *Config) LastBackupTime() time.Time {
	if c.LastBackup <= 0 {
		return time.Time{}
	}
	return time.Unix(c.LastBackup, 0)
}

// NextBackup is when the next scheduled backup will run (zero if off).
func NextBackup(every string, last, now time.Time) time.Time {
	if every == "" {
		return time.Time{}
	}
	if last.IsZero() {
		return now
	}
	switch every {
	case "daily":
		return last.AddDate(0, 0, 1)
	case "weekly":
		return last.AddDate(0, 0, 7)
	}
	return last.AddDate(0, 1, 0)
}

const backupPrefix, layoutExt = "Plots backup ", ".plots"

// BackupName is the file name for a backup taken at t. Names sort by time.
func BackupName(t time.Time) string {
	return backupPrefix + t.Format("2006-01-02 1504") + layoutExt
}

// WriteBackup saves a backup of cfg into dir and removes the oldest
// scheduled backups beyond keep. It returns the new file's path.
func WriteBackup(cfg *Config, dir string, keep int, now time.Time) (string, error) {
	if dir == "" {
		return "", errors.New(T("no backup folder chosen"))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	b, err := ExportConfig(cfg, now)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, BackupName(now))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	PruneBackups(dir, keep)
	return path, nil
}

// Backups lists the backups in dir, newest first.
func Backups(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, backupPrefix) && strings.HasSuffix(strings.ToLower(n), layoutExt) {
			out = append(out, filepath.Join(dir, n))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// PruneBackups deletes all but the newest keep backups in dir. Only files
// named like Plots' own backups are ever touched.
func PruneBackups(dir string, keep int) {
	if keep < 1 {
		keep = 1
	}
	list := Backups(dir)
	for i := keep; i < len(list); i++ {
		os.Remove(list[i])
	}
}

// DescribeBackup is a one-line status for the settings page.
func DescribeBackup(cfg *Config, now time.Time) string {
	if cfg.BackupEvery == "" {
		return T("Scheduled backups are off.")
	}
	last := T("never")
	if cfg.LastBackup > 0 {
		last = time.Unix(cfg.LastBackup, 0).Format("2006-01-02 15:04")
	}
	next := NextBackup(cfg.BackupEvery, cfg.LastBackupTime(), now)
	when := T("shortly")
	if cfg.LastBackup > 0 && next.After(now) {
		when = next.Format("2006-01-02")
	}
	return Tf("Last backup: %s. Next: %s.", last, when)
}
