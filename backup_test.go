package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExportRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	p := &Plot{ID: "a", Title: "Work", Color: "#112233", Opacity: 40, W: 300, H: 200}
	p.Norm()
	p.Tabs[0].Items = []string{`C:\x.txt`}
	cfg.Plots = []*Plot{p}
	cfg.InboxPlot = "a"
	cfg.Known = []string{"secret"}
	b, err := ExportConfig(cfg, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseExport(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Plots) != 1 || got.Plots[0].Title != "Work" || got.InboxPlot != "a" || len(got.Known) != 0 {
		t.Fatalf("bad round trip: %+v", got)
	}
	if got.Plots[0].Tabs[0].Items[0] != `C:\x.txt` {
		t.Fatal("items lost")
	}

	cur := DefaultConfig()
	cur.Known = []string{"k1"}
	cur.BackupEvery, cur.BackupDir = "weekly", `D:\bk`
	ApplyImport(cur, got)
	if cur.Known[0] != "k1" || cur.BackupEvery != "weekly" || cur.BackupDir != `D:\bk` || cur.Plots[0].Title != "Work" {
		t.Fatalf("import kept/replaced the wrong things: %+v", cur)
	}

	if _, err := ParseExport([]byte(`{"hello":1}`)); err == nil {
		t.Fatal("accepted a non-Plots file")
	}
	// a plain config.json is accepted
	raw := []byte(`{"plots":[{"id":"x","title":"Raw","w":300,"h":200}]}`)
	if c, err := ParseExport(raw); err != nil || c.Plots[0].Title != "Raw" {
		t.Fatalf("raw config: %v %+v", err, c)
	}
}

func TestBackupDue(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.Local)
	cases := []struct {
		every string
		last  time.Time
		want  bool
	}{
		{"", time.Time{}, false},
		{"daily", time.Time{}, true},
		{"daily", now.Add(-23 * time.Hour), false},
		{"daily", now.Add(-24 * time.Hour), true},
		{"weekly", now.AddDate(0, 0, -6), false},
		{"weekly", now.AddDate(0, 0, -7), true},
		{"monthly", now.AddDate(0, 0, -20), false},
		{"monthly", now.AddDate(0, -1, 0), true},
	}
	for _, c := range cases {
		if got := BackupDue(c.every, c.last, now); got != c.want {
			t.Errorf("BackupDue(%q, %v) = %v", c.every, c.last, got)
		}
	}
}

func TestWriteAndPruneBackups(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "keep me.txt"), []byte("x"), 0o644)
	cfg := DefaultConfig()
	t0 := time.Date(2026, 1, 1, 8, 0, 0, 0, time.Local)
	for i := 0; i < 5; i++ {
		if _, err := WriteBackup(cfg, dir, 3, t0.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	list := Backups(dir)
	if len(list) != 3 || filepath.Base(list[0]) != BackupName(t0.AddDate(0, 0, 4)) {
		t.Fatalf("got %v", list)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep me.txt")); err != nil {
		t.Fatal("pruned a file that isn't a backup")
	}
}

func TestImportFolders(t *testing.T) {
	ok := []string{`C:\Users\Me\Projects`, `d:/photos`, `E:\`}
	bad := []string{`\\server\share`, `\\?\C:\x`, `\\.\pipe\x`, `Projects`, `C:Projects`, `C:\a\..\..\Windows`, `C:\x\file:stream`, `C:\x?`, `1:\x`, ``}
	for _, p := range ok {
		if !LocalFolder(p) {
			t.Errorf("LocalFolder(%q) = false", p)
		}
	}
	for _, p := range bad {
		if LocalFolder(p) {
			t.Errorf("LocalFolder(%q) = true", p)
		}
	}
	cfg := &Config{Plots: []*Plot{{Folder: `C:\Work`, Background: `C:\Pics\a.jpg`}, {Folder: `\\evil\share`}, {Background: `\\evil\x.png`}, {Folder: `Z:\Shared`}}}
	kept, dropped := ImportFolders(cfg, func(p string) bool { return p[0] == 'Z' })
	if len(kept) != 1 || len(dropped) != 3 || cfg.Plots[2].Background != "" || cfg.Plots[0].Background == "" || cfg.Plots[1].Folder != "" || cfg.Plots[3].Folder != "" || cfg.Plots[0].Folder != `C:\Work` {
		t.Errorf("kept %v dropped %v", kept, dropped)
	}
}

func TestImportKeepsLocalSettings(t *testing.T) {
	cur := &Config{MoveDownloads: true, DownloadsDir: `C:\Users\Me\Downloads`, StartWithWindows: true}
	imp := &Config{MoveDownloads: true, DownloadsDir: `C:\Users\Me\Documents`}
	ApplyImport(cur, imp)
	if cur.DownloadsDir != `C:\Users\Me\Downloads` || !cur.StartWithWindows {
		t.Errorf("import changed this PC's settings: %+v", cur)
	}
}
