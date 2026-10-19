package main

import (
	"image"
	"image/color"
	"image/draw"
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

func TestAutoTitleContrast(t *testing.T) {
	// a white, fully opaque plot needs dark text; a dark one needs white text
	light := RenderOpts{W: 300, H: 120, Scale: 1, TitleH: 30, Title: "Web", Color: color.NRGBA{250, 250, 250, 255}, Opacity: 100, TitlePt: 11, TitleAuto: true, Header: HeaderSame}
	img := RenderPlot(light)
	if l := Lum(img, nil, image.Point{}, image.Rect(40, 0, 260, 30)); l < 0.55 {
		t.Fatalf("light plot measured dark: %.2f", l)
	}
	if ink, _ := Ink(0.9); ink.R > 100 {
		t.Fatal("light background should get dark text")
	}
	if ink, _ := Ink(0.1); ink.R < 200 {
		t.Fatal("dark background should get white text")
	}
	// a see-through plot over a white wallpaper is light too
	wall := image.NewRGBA(image.Rect(0, 0, 400, 200))
	draw.Draw(wall, wall.Bounds(), image.NewUniform(color.RGBA{240, 240, 240, 255}), image.Point{}, draw.Src)
	clear := light
	clear.Color, clear.Opacity = color.NRGBA{30, 30, 30, 255}, 10
	img = RenderPlot(clear)
	if l := Lum(img, wall, image.Pt(20, 20), image.Rect(40, 0, 260, 30)); l < 0.55 {
		t.Fatalf("see-through plot over a white wallpaper measured dark: %.2f", l)
	}
}
