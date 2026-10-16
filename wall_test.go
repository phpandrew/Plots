package main

import (
	"image"
	"image/color"
	"testing"
)

func TestWallCompose(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3840, 2160))
	for i := range src.Pix {
		src.Pix[i] = uint8(i)
		if i%4 == 3 {
			src.Pix[i] = 255
		}
	}
	for _, pos := range []int{WPFill, WPFit, WPStretch, WPCenter, WPTile, WPSpan} {
		mon := Rect{-2560, 0, 0, 1440}
		bg := RenderWallpaper(WallSpec{Position: pos, BG: color.RGBA{1, 2, 3, 255}, Area: mon}, src, mon)
		plot := RenderPlot(RenderOpts{W: 300, H: 200, Scale: 1, TitleH: 30, Title: "x", Color: ParseHex("#336699"), Opacity: 60, TitlePt: 11})
		out := ComposePanel(bg, Point{-2560, 0}, RectWH(-908, 24, 300, 200), plot)
		if out.Bounds().Dx() != 300 || out.Pix[3] != 255 {
			t.Fatal("bad compose", pos)
		}
	}
}
