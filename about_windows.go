//go:build windows

package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"strings"
	"sync"
	"time"
	"unsafe"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/vector"
)

//go:embed winres/icon.png
var logoPNG []byte

//go:embed winres/github-white.png
var githubPNG []byte

var (
	logoImg, githubImg image.Image
	imgOnce            sync.Once
	pShellExecuteW     = shell32.NewProc("ShellExecuteW")
)

func loadImages() {
	imgOnce.Do(func() {
		logoImg, _ = png.Decode(bytes.NewReader(logoPNG))
		githubImg, _ = png.Decode(bytes.NewReader(githubPNG))
	})
}

func openURL(url string) {
	call(pShellExecuteW, 0, up("open"), up(url), 0, 0, 1)
}

const (
	idAboutNav = 1500 + iota
	idLogo
	idGitHub
	idReleases
	idIssues
	idRepoLink
	idCheckUpd
	idAutoUpd
	idInstall
)

const (
	msgUpdateResult = WM_APP + 30
	clrAccent       = uint32(0xF7C34F) // BGR of #4FC3F7
)

// ------------------------------------------------------------------ update state (shared with the checker goroutine)

var (
	updMu      sync.Mutex
	updLatest  *Release
	updErr     error
	updBusy    bool
	updManual  bool
	updInstall bool
)

// checkForUpdates runs in the background and posts msgUpdateResult when done.
func (a *App) checkForUpdates(manual bool) {
	updMu.Lock()
	if updBusy {
		updMu.Unlock()
		return
	}
	updBusy, updManual = true, manual
	updMu.Unlock()
	go func() {
		r, err := latestRelease()
		updMu.Lock()
		updLatest, updErr, updBusy = r, err, false
		updMu.Unlock()
		postMessage(a.main, msgUpdateResult, 0, 0)
	}()
}

func (a *App) installUpdate() {
	updMu.Lock()
	r := updLatest
	if r == nil || updBusy {
		updMu.Unlock()
		return
	}
	updBusy, updInstall = true, true
	updMu.Unlock()
	go func() {
		err := downloadUpdate(r.AssetURL())
		updMu.Lock()
		updErr, updBusy = err, false
		updMu.Unlock()
		postMessage(a.main, msgUpdateResult, 1, 0)
	}()
}

// onUpdateResult runs on the UI thread.
func (a *App) onUpdateResult(installDone bool) {
	updMu.Lock()
	r, err, manual := updLatest, updErr, updManual
	updMu.Unlock()
	a.cfg.LastUpdateCheck = time.Now().Unix()
	a.markDirty()
	if installDone {
		if err != nil {
			a.setUpdateStatus(Tf("Download failed: %s", err.Error()), false)
			return
		}
		a.setUpdateStatus(T("Installing… Plots will restart in a few seconds."), false)
		a.checkUpdate() // swap right away instead of waiting for the next check
		return
	}
	if err != nil {
		log.Println("update check:", err)
		if manual {
			a.setUpdateStatus(Tf("Couldn't check: %s", err.Error()), false)
		}
		return
	}
	if isNewer(r.Tag, version) && r.AssetURL() != "" {
		a.setUpdateStatus(Tf("%s is available (you have %s).", r.Tag, displayVersion()), true)
		if !manual {
			a.notify(T("Plots update available"), Tf("%s is ready. Click to open Plots settings.", r.Tag))
		}
		return
	}
	if manual {
		a.setUpdateStatus(Tf("You're up to date (%s).", displayVersion()), false)
	}
}

func (a *App) setUpdateStatus(text string, canInstall bool) {
	a.updStatus, a.updAvailable = text, canInstall
	if s := a.settings; s != nil && isWindow(s.hwnd) {
		s.refreshAbout()
	}
}

// maybeAutoCheck checks GitHub at start-up and then every 6 hours.
func (a *App) maybeAutoCheck() {
	if !a.cfg.AutoUpdateCheck {
		return
	}
	if time.Since(time.Unix(a.cfg.LastUpdateCheck, 0)) > 6*time.Hour || !a.startupChecked {
		a.startupChecked = true
		a.checkForUpdates(false)
	}
}

// ------------------------------------------------------------------ about page

func (s *Settings) buildAbout() {
	l := s.newPage("about")
	top := l.cardBox(300)
	center := func(text string, y, h int, font uintptr, muted bool) HWND {
		c := l.add(0, "STATIC", text, 0x1 /*SS_CENTER*/ |0x80, cardL+16, y, cardW-32, h, 0)
		if font != 0 {
			sendMessage(c, WM_SETFONT, font, 1)
		}
		s.muted[c] = muted
		return c
	}
	l.add(0, "STATIC", "", 0xD /*SS_OWNERDRAW*/, cardL+(cardW-84)/2, top+20, 84, 84, idLogo)
	center("Plots", top+108, 40, s.bigFont, false)
	center(T("Desktop groups for Windows. A lightweight Fences alternative."), top+150, 20, 0, true)
	s.verLabel = center(Tf("Version %s", displayVersion()), top+174, 20, 0, false)
	center(Tf("Created by %s", "Andrew Lyle · @phpandrew"), top+196, 20, 0, true)
	bw := []int{170, 110, 140}
	gap := 10
	x := cardL + (cardW-(bw[0]+bw[1]+bw[2]+2*gap))/2
	l.add(0, "BUTTON", T("View on GitHub"), WS_TABSTOP|0xB /*BS_OWNERDRAW*/, x, top+226, bw[0], 34, idGitHub)
	l.add(0, "BUTTON", T("Releases"), WS_TABSTOP|BS_PUSHBUTTON, x+bw[0]+gap, top+226, bw[1], 34, idReleases)
	l.add(0, "BUTTON", T("Report an issue"), WS_TABSTOP|BS_PUSHBUTTON, x+bw[0]+bw[1]+2*gap, top+226, bw[2], 34, idIssues)
	s.linkLabel = center("github.com/"+repoSlug, top+268, 20, 0, false)
	call(pSetWindowLongPtrW, s.linkLabel, ^uintptr(15), WS_CHILD|0x1|0x80|SS_NOTIFY)
	call(pSetWindowLongPtrW, s.linkLabel, ^uintptr(11) /*GWLP_ID*/, idRepoLink)

	l.section(T("Updates"))
	cy, d := l.row(T("Check for updates"), strings.Repeat(" ", 70), 318)
	s.updLabel = d
	s.installBtn = l.button(T("Download && install"), cy, ctlR-160-8-150, 160, idInstall)
	l.button(T("Check now"), cy, ctlR-150, 150, idCheckUpd)
	cy, _ = l.row(T("Check for updates automatically"), T("Plots looks for a new release at start-up and every 6 hours."), 56)
	s.autoUpd = l.toggle(cy, idAutoUpd)
	l.done()
}

func (s *Settings) refreshAbout() {
	if s == nil || !isWindow(s.hwnd) {
		return
	}
	a := s.a
	setCheck(s.autoUpd, a.cfg.AutoUpdateCheck)
	st := a.updStatus
	if st == "" {
		st = Tf("You have %s.", displayVersion())
	}
	setWindowText(s.updLabel, st)
	show := uintptr(SW_HIDE)
	if a.updAvailable && s.page == "about" {
		show = SW_SHOWNA
	}
	call(pShowWindow, s.installBtn, show)
}

func (s *Settings) aboutCommand(id int) bool {
	a := s.a
	switch id {
	case idAboutNav:
		s.showPage("about")
	case idGitHub, idRepoLink:
		openURL(repoURL)
	case idReleases:
		openURL(releaseURL)
	case idIssues:
		openURL(issuesURL)
	case idCheckUpd:
		a.setUpdateStatus(T("Checking GitHub…"), false)
		a.checkForUpdates(true)
	case idInstall:
		a.setUpdateStatus(T("Downloading…"), false)
		a.installUpdate()
	case idAutoUpd:
		a.cfg.AutoUpdateCheck = checked(s.autoUpd)
		a.markDirty()
		if a.cfg.AutoUpdateCheck {
			a.checkForUpdates(false)
		}
	default:
		return false
	}
	return true
}

// blit draws an RGBA image at r on hdc.
func blit(hdc uintptr, r RECT, img *image.RGBA) {
	bmp := makeBitmap(img)
	mem := call(pCreateCompatibleDC, hdc)
	old := call(pSelectObject, mem, bmp)
	call(pBitBlt, hdc, uintptr(r.Left), uintptr(r.Top), uintptr(img.Bounds().Dx()), uintptr(img.Bounds().Dy()), mem, 0, 0, SRCCOPY)
	call(pSelectObject, mem, old)
	call(pDeleteDC, mem)
	call(pDeleteObject, bmp)
}

func bgr(c uint32) color.NRGBA {
	return color.NRGBA{uint8(c), uint8(c >> 8), uint8(c >> 16), 255}
}

// logoTile renders the Plots logo at size on a solid background.
func logoTile(size int, bg uint32) *image.RGBA {
	loadImages()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.NewUniform(bgr(bg)), image.Point{}, draw.Src)
	if logoImg != nil {
		xdraw.CatmullRom.Scale(img, img.Bounds(), logoImg, logoImg.Bounds(), draw.Over, nil)
	}
	return img
}

func (s *Settings) drawAbout(di *DRAWITEMSTRUCT) bool {
	r := di.RcItem
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	switch di.CtlID {
	case idLogo:
		blit(di.HDC, r, logoTile(min(w, h), clrWhite))
		return true
	case idGitHub:
		loadImages()
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{255, 255, 255, 255}), image.Point{}, draw.Src)
		acc := bgr(clrAccent)
		if di.ItemState&1 != 0 { // pressed
			acc = shade(acc, 0.88)
		}
		rr := float32(6 * s.scale)
		fill(img, acc, func(z *vector.Rasterizer) { roundRectPath(z, 0, 0, float32(w), float32(h), rr, rr, false) })
		ic := int(18 * s.scale)
		ix, iy := int(14*s.scale), (h-ic)/2
		if githubImg != nil {
			// the mark is white; draw it dark for contrast on the light accent
			dark := image.NewRGBA(image.Rect(0, 0, ic, ic))
			xdraw.CatmullRom.Scale(dark, dark.Bounds(), githubImg, githubImg.Bounds(), draw.Src, nil)
			for i := 0; i+3 < len(dark.Pix); i += 4 {
				a := dark.Pix[i+3]
				dark.Pix[i], dark.Pix[i+1], dark.Pix[i+2] = a/10, a/10, a/10
			}
			draw.Draw(img, image.Rect(ix, iy, ix+ic, iy+ic), dark, image.Point{}, draw.Over)
		}
		blit(di.HDC, r, img)
		call(pSetBkMode, di.HDC, 1)
		old := call(pSelectObject, di.HDC, s.font)
		call(pSetTextColor, di.HDC, 0x1A1A1A)
		tr := RECT{r.Left + int32(ix+ic) + int32(8*s.scale), r.Top, r.Right, r.Bottom}
		call(pDrawTextW, di.HDC, up(T("View on GitHub")), ^uintptr(0), uintptr(unsafe.Pointer(&tr)), 0x4|0x20)
		call(pSelectObject, di.HDC, old)
		return true
	}
	return false
}
