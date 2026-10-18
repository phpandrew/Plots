//go:build windows

package main

var pGetUserDefaultUILanguage = kernel32.NewProc("GetUserDefaultUILanguage")

// systemLanguage is the Windows display language, as a Plots language code.
func systemLanguage() string {
	return LanguageForLangID(uint16(call(pGetUserDefaultUILanguage)))
}

// applyLanguage sets the UI language from the config ("" = follow Windows).
func (a *App) applyLanguage() {
	code := a.cfg.Language
	if code == "" {
		code = systemLanguage()
	}
	SetLanguage(code)
}

const msgReopenSettings = WM_APP + 34

// languageChanged rebuilds everything that shows text in the new language.
func (a *App) languageChanged(page string) {
	a.reopenPage = page
	if a.settings != nil && isWindow(a.settings.hwnd) {
		call(pDestroyWindow, a.settings.hwnd)
		postMessage(a.main, msgReopenSettings, 0, 0)
	}
	for _, pn := range a.panels {
		pn.key = "" // panel hints are translated too
	}
	if a.editing {
		a.exitEdit()
		a.enterEdit()
	}
	a.registerPeek()
	a.tick()
}

func (a *App) reopenSettings() {
	a.showSettings(a.selected)
	if s := a.settings; s != nil && a.reopenPage != "" && a.reopenPage != "plot" {
		s.showPage(a.reopenPage)
	}
	a.reopenPage = ""
}
