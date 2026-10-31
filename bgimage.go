package main

import (
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Background images for plots. Images are decoded once, shrunk to at most
// bgMaxSide, and laid out per plot size on demand; both steps are cached.

const (
	bgMaxSide   = 2560
	bgBudget    = 48 << 20   // decoded pictures kept, in bytes
	bgMaxPixels = 40_000_000 // refuse anything bigger before decoding it
	bgMaxBytes  = 100 << 20
)

type bgEntry struct {
	mod     time.Time
	size    int64
	img     image.Image // nil if the file couldn't be used
	checked time.Time
}

var (
	bgMu     sync.Mutex
	bgCache  = map[string]*bgEntry{}
	bgLayout = map[string]*image.RGBA{}
)

// DecodeBackground reads an image file for use as a plot background.
func DecodeBackground(path string) (image.Image, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() || st.Size() > bgMaxBytes {
		return nil, errors.New("not an image file, or too large")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > bgMaxPixels {
		return nil, fmt.Errorf("image is too large (%d x %d)", cfg.Width, cfg.Height)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if long := max(b.Dx(), b.Dy()); long > bgMaxSide {
		k := float64(bgMaxSide) / float64(long)
		img = FitImage(img, max(1, int(float64(b.Dx())*k)), max(1, int(float64(b.Dy())*k)), BgStretch)
	}
	return img, nil
}

// backgroundImage is the decoded image at path (nil if unusable). The file
// is re-checked every few seconds so an edited image shows up.
func backgroundImage(path string) (image.Image, time.Time) {
	bgMu.Lock()
	defer bgMu.Unlock()
	e := bgCache[path]
	if e != nil && time.Since(e.checked) < 3*time.Second {
		return e.img, e.mod
	}
	st, err := os.Stat(path)
	if err != nil {
		if e == nil || e.img != nil {
			log.Printf("background image %q: %v", path, err)
		}
		bgCache[path] = &bgEntry{checked: time.Now()}
		return nil, time.Time{}
	}
	if e != nil && e.mod.Equal(st.ModTime()) && e.size == st.Size() {
		e.checked = time.Now()
		return e.img, e.mod
	}
	img, err := DecodeBackground(path)
	if err != nil {
		log.Printf("background image %q: %v", path, err)
	}
	total := 0
	for _, e := range bgCache {
		total += imageBytes(e.img)
	}
	if total+imageBytes(img) > bgBudget {
		bgCache = map[string]*bgEntry{}
	}
	bgCache[path] = &bgEntry{mod: st.ModTime(), size: st.Size(), img: img, checked: time.Now()}
	releaseMemory() // the full-size decode was much bigger than what's kept
	return img, st.ModTime()
}

// LaidOutBackground is p's background image laid out at w x h (nil if none).
func LaidOutBackground(p *Plot, w, h int) *image.RGBA {
	if p.Background == "" || w <= 0 || h <= 0 {
		return nil
	}
	img, mod := backgroundImage(p.Background)
	if img == nil {
		return nil
	}
	key := fmt.Sprintf("%s|%s|%d|%dx%d|%s", p.ID, p.Background, mod.UnixNano(), w, h, p.BgFit)
	bgMu.Lock()
	out := bgLayout[key]
	bgMu.Unlock()
	if out != nil {
		return out
	}
	out = FitImage(img, w, h, p.BgFit)
	bgMu.Lock()
	// one laid-out picture per plot: drop the one for its old size or picture
	prefix := p.ID + "|"
	for k := range bgLayout {
		if strings.HasPrefix(k, prefix) {
			delete(bgLayout, k)
		}
	}
	bgLayout[key] = out
	bgMu.Unlock()
	return out
}

func imageBytes(img image.Image) int {
	if img == nil {
		return 0
	}
	b := img.Bounds()
	return b.Dx() * b.Dy() * 4
}

// BgAlpha is the background image's opacity, 10-100.
func (p *Plot) BgAlpha() int {
	if p.BgOpacity <= 0 || p.BgOpacity > 100 {
		return 100
	}
	return max(10, p.BgOpacity)
}
