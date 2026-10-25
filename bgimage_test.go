package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestBackgroundImage(t *testing.T) {
	dir := t.TempDir()
	src := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for x := 0; x < 200; x++ {
		for y := 0; y < 100; y++ {
			src.Set(x, y, color.RGBA{uint8(x), 0, 0, 255})
		}
	}
	path := filepath.Join(dir, "bg.png")
	f, _ := os.Create(path)
	png.Encode(f, src)
	f.Close()

	p := &Plot{Background: path}
	for _, fit := range []string{BgFill, BgFit, BgStretch, BgTile} {
		p.BgFit = fit
		img := LaidOutBackground(p, 120, 90)
		if img == nil || img.Bounds().Dx() != 120 || img.Bounds().Dy() != 90 {
			t.Fatalf("%q: bad layout %v", fit, img)
		}
	}
	// fit leaves bars above and below a wide image; fill doesn't
	p.BgFit = BgFit
	if _, _, _, a := LaidOutBackground(p, 120, 90).At(60, 2).RGBA(); a != 0 {
		t.Error("fit should letterbox")
	}
	p.BgFit = BgFill
	if _, _, _, a := LaidOutBackground(p, 120, 90).At(60, 2).RGBA(); a == 0 {
		t.Error("fill should cover")
	}

	bad := filepath.Join(dir, "notes.png")
	os.WriteFile(bad, []byte("not an image"), 0o644)
	if LaidOutBackground(&Plot{Background: bad}, 50, 50) != nil {
		t.Error("accepted a broken image")
	}
	if LaidOutBackground(&Plot{Background: filepath.Join(dir, "missing.png")}, 50, 50) != nil {
		t.Error("accepted a missing file")
	}
	if (&Plot{}).BgAlpha() != 100 || (&Plot{BgOpacity: 3}).BgAlpha() != 10 || (&Plot{BgOpacity: 60}).BgAlpha() != 60 {
		t.Error("BgAlpha")
	}
}
