package main

import (
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Plot panels are drawn opaque on the desktop layer (Explorer does not
// reliably composite per-pixel-alpha child windows), so translucency is
// produced by blending the panel over the same wallpaper Explorer shows.

const (
	WPCenter  = 0
	WPTile    = 1
	WPStretch = 2
	WPFit     = 3
	WPFill    = 4
	WPSpan    = 5
)

type WallSpec struct {
	Path     string
	Position int
	BG       color.RGBA
	Area     Rect // the screen area the image is laid out in (monitor, or virtual screen for span)
}

func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// RenderWallpaper lays out the wallpaper for spec.Area and returns an image
// covering `out` (screen coords) — normally one monitor.
func RenderWallpaper(spec WallSpec, src image.Image, out Rect) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, out.W(), out.H()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(spec.BG), image.Point{}, draw.Src)
	if src == nil {
		return dst
	}
	a := spec.Area
	aw, ah := a.W(), a.H()
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if aw <= 0 || ah <= 0 || sw <= 0 || sh <= 0 {
		return dst
	}
	// target rect in screen coords
	var tr Rect
	switch spec.Position {
	case WPStretch:
		tr = a
	case WPFit:
		s := min(float64(aw)/float64(sw), float64(ah)/float64(sh))
		w, h := int(float64(sw)*s+0.5), int(float64(sh)*s+0.5)
		tr = RectWH(a.L+(aw-w)/2, a.T+(ah-h)/2, w, h)
	case WPCenter:
		tr = RectWH(a.L+(aw-sw)/2, a.T+(ah-sh)/2, sw, sh)
	case WPTile:
		for y := a.T; y < a.B; y += sh {
			for x := a.L; x < a.R; x += sw {
				r := image.Rect(x-out.L, y-out.T, x-out.L+sw, y-out.T+sh)
				draw.Draw(dst, r, src, src.Bounds().Min, draw.Src)
			}
		}
		return dst
	default: // fill, span
		s := max(float64(aw)/float64(sw), float64(ah)/float64(sh))
		w, h := int(float64(sw)*s+0.5), int(float64(sh)*s+0.5)
		tr = RectWH(a.L+(aw-w)/2, a.T+(ah-h)/2, w, h)
	}
	// only scale the visible part
	r := image.Rect(tr.L-out.L, tr.T-out.T, tr.R-out.L, tr.B-out.T)
	xdraw.ApproxBiLinear.Scale(dst, r, src, src.Bounds(), draw.Src, nil)
	return dst
}

// ComposePanel draws the plot over the wallpaper crop beneath it.
func ComposePanel(bg *image.RGBA, bgOrigin Point, rect Rect, plot *image.RGBA) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, rect.W(), rect.H()))
	if bg != nil {
		draw.Draw(out, out.Bounds(), bg, image.Pt(rect.L-bgOrigin.X, rect.T-bgOrigin.Y), draw.Src)
	} else {
		draw.Draw(out, out.Bounds(), image.NewUniform(color.RGBA{0, 0, 0, 255}), image.Point{}, draw.Src)
	}
	draw.Draw(out, out.Bounds(), plot, image.Point{}, draw.Over)
	return out
}
