//go:build windows

package main

import (
	"encoding/xml"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// DLWatcher moves files that newly finish downloading into the Downloads
// folder onto the desktop, where the engine routes them to the inbox plot.
type DLWatcher struct {
	cfg      *Config
	baseline map[string]bool
	pending  map[string]*dlState
}

type dlState struct {
	size  int64
	seen  int
	tries int
}

var tempExt = []string{".crdownload", ".part", ".partial", ".tmp", ".download", ".opdownload", ".!ut", ".aria2", ".ini"}

func NewDLWatcher(cfg *Config) *DLWatcher { return &DLWatcher{cfg: cfg} }

func (w *DLWatcher) Dir() string {
	if w.cfg.DownloadsDir != "" {
		return w.cfg.DownloadsDir
	}
	p, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, 0)
	if err != nil {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Downloads")
	}
	return p
}

func desktopDir() string {
	p, err := windows.KnownFolderPath(windows.FOLDERID_Desktop, 0)
	if err != nil {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Desktop")
	}
	return p
}

func (w *DLWatcher) Reset() { w.baseline = nil; w.pending = nil }

func (w *DLWatcher) Scan() {
	dir := w.Dir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if w.baseline == nil {
		// only files that arrive after the option is switched on are moved
		w.baseline = map[string]bool{}
		w.pending = map[string]*dlState{}
		for _, e := range entries {
			w.baseline[e.Name()] = true
		}
		return
	}
	present := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		present[name] = true
		if w.baseline[name] || e.IsDir() || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~$") {
			continue
		}
		low := strings.ToLower(name)
		temp := false
		for _, ext := range tempExt {
			if strings.HasSuffix(low, ext) {
				temp = true
			}
		}
		if temp {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		st := w.pending[name]
		if st == nil || st.size != info.Size() {
			w.pending[name] = &dlState{size: info.Size()}
			continue
		}
		st.seen++
		if st.seen < 2 { // size stable for two scans (~4s)
			continue
		}
		dst := uniquePath(desktopDir(), name)
		if err := os.Rename(filepath.Join(dir, name), dst); err != nil {
			st.tries++
			if st.tries > 10 {
				log.Println("downloads: giving up on", name, err)
				w.baseline[name] = true
			}
			continue
		}
		log.Println("downloads: moved", name, "->", dst)
		delete(w.pending, name)
	}
	for name := range w.baseline {
		if !present[name] {
			delete(w.baseline, name)
		}
	}
}

func uniquePath(dir, name string) string {
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
	}
}

// ------------------------------------------------------------------ autostart

// Plots starts from a Task Scheduler logon task rather than the Run key:
// Run-key apps are held back by Windows' startup delay, while a logon task
// fires as soon as you sign in. Plots then waits for Explorer by itself.

const taskXML = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>Plots - desktop groups</Description></RegistrationInfo>
  <Triggers><LogonTrigger><Enabled>true</Enabled><UserId>%s</UserId></LogonTrigger></Triggers>
  <Principals><Principal id="Author"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>4</Priority>
  </Settings>
  <Actions Context="Author"><Exec><Command>%s</Command></Exec></Actions>
</Task>`

// schtasks is the full System32 path, so a schtasks.exe earlier on PATH is never run.
func schtasks() string {
	if dir, err := windows.GetSystemDirectory(); err == nil && dir != "" {
		return filepath.Join(dir, "schtasks.exe")
	}
	return "schtasks"
}

func runHidden(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /*CREATE_NO_WINDOW*/}
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("%s %v: %v %s", name, args, err, out)
	}
	return err
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (a *App) applyAutostart() {
	// clean up the older Run-key entry
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE); err == nil {
		k.DeleteValue("Plots")
		k.Close()
	}
	if !a.cfg.StartWithWindows {
		runHidden(schtasks(), "/Delete", "/TN", "Plots", "/F")
		return
	}
	exe, _ := os.Executable()
	user := os.Getenv("USERDOMAIN") + `\` + os.Getenv("USERNAME")
	body := fmt.Sprintf(taskXML, xmlEscape(user), xmlEscape(user), xmlEscape(exe))
	// schtasks wants UTF-16 with a BOM
	u := utf16.Encode([]rune(body))
	buf := make([]byte, 2+len(u)*2)
	buf[0], buf[1] = 0xFF, 0xFE
	for i, c := range u {
		buf[2+i*2], buf[3+i*2] = byte(c), byte(c>>8)
	}
	tmp := filepath.Join(os.TempDir(), "plots-task.xml")
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return
	}
	defer os.Remove(tmp)
	if runHidden(schtasks(), "/Create", "/TN", "Plots", "/XML", tmp, "/F") != nil {
		// fall back to the Run key if Task Scheduler refuses
		if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE); err == nil {
			k.SetStringValue("Plots", `"`+exe+`"`)
			k.Close()
		}
	}
}
