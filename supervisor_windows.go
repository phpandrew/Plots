//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"time"

	"golang.org/x/sys/windows"
)

// Plots runs as two processes: a small supervisor (what you start) and the
// app itself (started with --child). If the app ever crashes, the supervisor
// writes it to crash.log and starts it again, so plots don't silently vanish.

const childFlag = "--child"

func plotsDir() string {
	appData, _ := os.UserConfigDir()
	dir := filepath.Join(appData, "Plots")
	os.MkdirAll(dir, 0o755)
	return dir
}

// crashLog appends a line to crash.log.
func crashLog(format string, args ...any) {
	f, err := os.OpenFile(filepath.Join(plotsDir(), "crash.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	f.Close()
}

// captureCrashes sends the Go runtime's report of a fatal error (panic,
// stack traces) to crash.log instead of nowhere.
func captureCrashes() {
	f, err := os.OpenFile(filepath.Join(plotsDir(), "crash.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	debug.SetCrashOutput(f, debug.CrashOptions{})
}

// supervise starts the app and restarts it after a crash. It returns when the
// app exits normally (Exit Plots, or restarting into an update).
func supervise(afterUpdate bool) {
	// one supervisor at a time
	for tries := 0; ; tries++ {
		h, _, err := pCreateMutexW.Call(0, 0, up("Local\\PlotsSupervisor"))
		if h == 0 || err != windows.ERROR_ALREADY_EXISTS {
			break
		}
		windows.CloseHandle(windows.Handle(h))
		if afterUpdate && tries < 50 {
			time.Sleep(200 * time.Millisecond) // the previous version is still exiting
			continue
		}
		if w := findWindow("PlotsMain"); w != 0 {
			postMessage(w, msgShowSettings, 0, 0) // already running: open settings
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		runApp(afterUpdate) // can't supervise; just run
		return
	}
	var crashes []time.Time
	for {
		args := []string{childFlag}
		if afterUpdate {
			args = append(args, "--after-update")
		}
		afterUpdate = false
		cmd := exec.Command(exe, args...)
		cmd.Dir = filepath.Dir(exe)
		if err := cmd.Run(); err == nil {
			return // normal exit
		} else if call(pGetSystemMetrics, 0x2000 /*SM_SHUTTINGDOWN*/) != 0 {
			return // Windows is shutting down or signing out
		} else if ee, ok := err.(*exec.ExitError); !ok {
			crashLog("could not start Plots: %v", err)
			return
		} else {
			crashLog("Plots stopped unexpectedly (exit code %d); restarting", ee.ExitCode())
		}
		// restart, but don't loop forever on a crash at start-up
		now := time.Now()
		crashes = append(crashes, now)
		recent := crashes[:0]
		for _, t := range crashes {
			if now.Sub(t) < 10*time.Minute {
				recent = append(recent, t)
			}
		}
		crashes = recent
		if len(crashes) >= 5 {
			crashLog("Plots crashed 5 times in 10 minutes; not restarting")
			messageBox(0, T("Plots keeps stopping unexpectedly, so it won't restart on its own.\n\nThe details are in crash.log in %APPDATA%\\Plots. Please report it on GitHub."), "Plots", MB_OK|MB_ICONWARNING)
			return
		}
		time.Sleep(time.Duration(len(crashes)) * 2 * time.Second)
	}
}
