//go:build windows

package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
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
			a.setUpdateStatus("Download failed: "+err.Error(), false)
			return
		}
		a.setUpdateStatus("Installing… Plots will restart in a few seconds.", false)
		a.checkUpdate() // swap right away instead of waiting for the next check
		return
	}
	if err != nil {
		log.Println("update check:", err)
		if manual {
			a.setUpdateStatus("Couldn't check: "+err.Error(), false)
		}
		return
	}
	if isNewer(r.Tag, version) && r.AssetURL() != "" {
		a.setUpdateStatus(fmt.Sprintf("%s is available (you have %s).", r.Tag, displayVersion()), true)
		if !manual {
			a.notify("Plots update available", fmt.Sprintf("%s is ready. Click to open Plots settings.", r.Tag))
		}
		return
	}
	if manual {
		a.setUpdateStatus(fmt.Sprintf("You're up to date (%s).", displayVersion()), false)
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

func (s *Settings) buildAbout(sc func(int) int) {
	const BTN = WS_TABSTOP | BS_PUSHBUTTON
	ab := func(h HWND) HWND { s.aboutPage = append(s.aboutPage, h); return h }
	w := colR - colL
	center := func(text string, y, h int, font uintptr, muted bool) HWND {
		c := ab(s.ctl(0, "STATIC", text, 0x1 /*SS_CENTER*/, sc(colL), sc(y), sc(w), sc(h), 0))
		if font != 0 {
			sendMessage(c, WM_SETFONT, font, 1)
		}
		if muted {
			s.muted = append(s.muted, c)
		}
		return c
	}
	dpi := dpiForWindow(s.hwnd)
	s.bigFont = call(pCreateFontW, uintptr(int32(-int(math.Round(22*float64(dpi)/72)))), 0, 0, 0, 700, 0, 0, 0, 1, 0, 0, 5, 0, up("Segoe UI"))

	ab(s.ctl(0, "STATIC", "", 0xD /*SS_OWNERDRAW*/, sc(colL+(w-84)/2), sc(20), sc(84), sc(84), idLogo))
	center("Plots", 108, 40, s.bigFont, false)
	center("Desktop groups for Windows — a lightweight Fences alternative.", 150, 20, 0, true)
	s.verLabel = center("Version "+displayVersion(), 176, 20, 0, false)
	center("Created by Andrew Lyle · @phpandrew", 200, 20, 0, true)

	bw := []int{170, 110, 140}
	gap := 10
	x := colL + (w-(bw[0]+bw[1]+bw[2]+2*gap))/2
	ab(s.ctl(0, "BUTTON", "View on GitHub", WS_TABSTOP|0xB /*BS_OWNERDRAW*/, sc(x), sc(232), sc(bw[0]), sc(34), idGitHub))
	ab(s.ctl(0, "BUTTON", "Releases", BTN, sc(x+bw[0]+gap), sc(232), sc(bw[1]), sc(34), idReleases))
	ab(s.ctl(0, "BUTTON", "Report an issue", BTN, sc(x+bw[0]+bw[1]+2*gap), sc(232), sc(bw[2]), sc(34), idIssues))
	link := ab(s.ctl(0, "STATIC", "github.com/"+repoSlug, 0x1|SS_NOTIFY, sc(colL), sc(276), sc(w), sc(20), idRepoLink))
	s.linkLabel = link

	h := ab(s.ctl(0, "STATIC", "Updates", SS_LEFT, sc(colL), sc(318), sc(w), sc(26), 0))
	sendMessage(h, WM_SETFONT, s.bold, 1)
	s.muted = append(s.muted, ab(s.ctl(0, "STATIC", "New versions are published on GitHub Releases.", SS_LEFT, sc(colL), sc(346), sc(w), sc(20), 0)))
	ab(s.ctl(0, "BUTTON", "Check for updates", BTN, sc(colL), sc(376), sc(150), sc(32), idCheckUpd))
	s.installBtn = ab(s.ctl(0, "BUTTON", "Download && install", BTN|BS_DEFPUSHBUTTON, sc(colL+160), sc(376), sc(160), sc(32), idInstall))
	s.updLabel = ab(s.ctl(0, "STATIC", "", SS_LEFT, sc(colL), sc(414), sc(w), sc(20), 0))
	s.muted = append(s.muted, s.updLabel)
	s.autoUpd = ab(s.ctl(0, "BUTTON", "Check for updates automatically", WS_TABSTOP|0xB, sc(colL), sc(440), sc(w), sc(32), idAutoUpd))
	toggleState[s.autoUpd] = false
}

func (s *Settings) refreshAbout() {
	if s == nil || !isWindow(s.hwnd) {
		return
	}
	a := s.a
	setCheck(s.autoUpd, a.cfg.AutoUpdateCheck)
	setWindowText(s.updLabel, a.updStatus)
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
		a.setUpdateStatus("Checking GitHub…", false)
		a.checkForUpdates(true)
	case idInstall:
		a.setUpdateStatus("Downloading…", false)
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
		call(pDrawTextW, di.HDC, up("View on GitHub"), ^uintptr(0), uintptr(unsafe.Pointer(&tr)), 0x4|0x20)
		call(pSelectObject, di.HDC, old)
		return true
	}
	return false
}
