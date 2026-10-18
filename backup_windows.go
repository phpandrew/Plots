//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	pGetSaveFileNameW = comdlg32.NewProc("GetSaveFileNameW")
	pGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
)

type openFileName struct {
	StructSize    uint32
	Owner         HWND
	Instance      uintptr
	Filter        *uint16
	CustomFilter  *uint16
	MaxCustFilter uint32
	FilterIndex   uint32
	File          *uint16
	MaxFile       uint32
	FileTitle     *uint16
	MaxFileTitle  uint32
	InitialDir    *uint16
	Title         *uint16
	Flags         uint32
	FileOffset    uint16
	FileExtension uint16
	DefExt        *uint16
	CustData      uintptr
	Hook          uintptr
	TemplateName  *uint16
	Reserved      uintptr
	Reserved2     uint32
	FlagsEx       uint32
}

// fileDialog shows the standard Open or Save As dialog for .plots files.
func fileDialog(owner HWND, save bool, title, dir, name string) string {
	filter := u16z(T("Plots layout") + " (*.plots)\x00*.plots\x00" + T("Plots config") + " (*.json)\x00*.json\x00" + T("All files") + "\x00*.*\x00\x00")
	buf := make([]uint16, 1024)
	copy(buf[:len(buf)-1], utf16.Encode([]rune(name)))
	ofn := openFileName{Owner: owner, Filter: &filter[0], FilterIndex: 1, File: &buf[0], MaxFile: uint32(len(buf)),
		Title: u16(title), DefExt: u16("plots"), Flags: 0x80000 /*EXPLORER*/ | 0x8 /*NOCHANGEDIR*/ | 0x800 /*PATHMUSTEXIST*/}
	if dir != "" {
		ofn.InitialDir = u16(dir)
	}
	ofn.StructSize = uint32(unsafe.Sizeof(ofn))
	proc := pGetOpenFileNameW
	if save {
		ofn.Flags |= 0x2 // OVERWRITEPROMPT
		proc = pGetSaveFileNameW
	} else {
		ofn.Flags |= 0x1000 // FILEMUSTEXIST
	}
	if call(proc, uintptr(unsafe.Pointer(&ofn))) == 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}

// u16z is s as UTF-16, keeping embedded NULs (for filter strings).
func u16z(s string) []uint16 { return append(utf16.Encode([]rune(s)), 0) }

// defaultBackupDir is Documents\Plots Backups.
func defaultBackupDir() string {
	docs, err := windows.KnownFolderPath(windows.FOLDERID_Documents, 0)
	if err != nil || docs == "" {
		docs = filepath.Join(os.Getenv("USERPROFILE"), "Documents")
	}
	return filepath.Join(docs, "Plots Backups")
}

func (a *App) backupDir() string {
	if a.cfg.BackupDir != "" {
		return a.cfg.BackupDir
	}
	return defaultBackupDir()
}

// exportLayout saves every plot and setting to a file the user picks.
func (a *App) exportLayout(owner HWND) {
	path := fileDialog(owner, true, T("Export plots"), a.backupDir(), T("Plots layout")+" "+time.Now().Format("2006-01-02")+layoutExt)
	if path == "" {
		return
	}
	b, err := ExportConfig(a.cfg, time.Now())
	if err == nil {
		err = os.WriteFile(path, b, 0o644)
	}
	if err != nil {
		messageBox(owner, T("Couldn't export:")+"\n\n"+err.Error(), "Plots", MB_OK|MB_ICONWARNING)
		return
	}
	messageBox(owner, Tf("Exported %d plot(s) and your settings to:\n\n%s", len(a.cfg.Plots), path), "Plots", MB_OK|MB_ICONINFO)
}

// importLayout replaces the current plots and settings with a file's.
func (a *App) importLayout(owner HWND) {
	path := fileDialog(owner, false, T("Import plots"), a.backupDir(), "")
	if path == "" {
		return
	}
	a.importFrom(owner, path)
}

func (a *App) importFrom(owner HWND, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		messageBox(owner, T("Couldn't read the file:")+"\n\n"+err.Error(), "Plots", MB_OK|MB_ICONWARNING)
		return
	}
	imp, err := ParseExport(b)
	if err != nil {
		messageBox(owner, Tf("Couldn't import %s: %s.", filepath.Base(path), err.Error()), "Plots", MB_OK|MB_ICONWARNING)
		return
	}
	msg := Tf("Replace your current %d plot(s) and settings with the %d plot(s) in \"%s\"?\n\nA backup of your current layout is saved first, so you can undo this by importing it.",
		len(a.cfg.Plots), len(imp.Plots), filepath.Base(path))
	if messageBox(owner, msg, "Plots", MB_YESNO|MB_ICONWARNING) != IDYES {
		return
	}
	// safety net: keep the layout we're about to replace
	if _, err := WriteBackup(a.cfg, a.backupDir(), max(a.cfg.BackupKeep, 1)+1, time.Now()); err != nil {
		log.Println("pre-import backup:", err)
	}
	if a.editing {
		a.exitEdit()
	}
	ApplyImport(a.cfg, imp)
	a.selected = ""
	a.markDirty()
	a.save(true)
	a.eng.Relayout()
	a.applyAutostart()
	a.publishHookState()
	a.tick()
	if a.settings != nil {
		a.settings.reloadAll()
	}
	log.Printf("imported %d plots from %s", len(a.cfg.Plots), path)
}

// backupNow writes a backup immediately (the "Back up now" button).
func (a *App) backupNow(owner HWND) {
	path, err := WriteBackup(a.cfg, a.backupDir(), a.cfg.BackupKeep, time.Now())
	if err != nil {
		messageBox(owner, T("Backup failed:")+"\n\n"+err.Error(), "Plots", MB_OK|MB_ICONWARNING)
		return
	}
	if a.cfg.BackupEvery != "" {
		a.cfg.LastBackup = time.Now().Unix()
		a.markDirty()
	}
	a.backupMsg = Tf("Saved %s", filepath.Base(path))
}

var nextBackupCheck time.Time

// scheduledBackup runs from the tick; it checks about once a minute.
func (a *App) scheduledBackup() {
	now := time.Now()
	if now.Before(nextBackupCheck) {
		return
	}
	nextBackupCheck = now.Add(time.Minute)
	if !BackupDue(a.cfg.BackupEvery, a.cfg.LastBackupTime(), now) || len(a.cfg.Plots) == 0 {
		return
	}
	path, err := WriteBackup(a.cfg, a.backupDir(), a.cfg.BackupKeep, now)
	if err != nil {
		log.Println("scheduled backup:", err)
		a.backupMsg = Tf("Last scheduled backup failed: %s", err.Error())
		nextBackupCheck = now.Add(time.Hour) // don't retry every minute
		return
	}
	log.Println("scheduled backup:", path)
	a.backupMsg = ""
	a.cfg.LastBackup = now.Unix()
	a.markDirty()
	a.save(true)
	if a.settings != nil && a.settings.page == "backup" {
		a.settings.refreshBackup()
	}
}
