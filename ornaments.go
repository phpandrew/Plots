package main

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/vector"
)

// Seasonal ornaments, drawn as vectors so they stay sharp at any DPI. Each
// is drawn opaque into its own layer, then blended in with one alpha, so
// overlapping parts don't show seams.

type pt struct{ x, y float32 }

// oval adds an ellipse to z.
func oval(z *vector.Rasterizer, cx, cy, rx, ry float32) {
	kx, ky := rx*kappa, ry*kappa
	z.MoveTo(cx+rx, cy)
	z.CubeTo(cx+rx, cy+ky, cx+kx, cy+ry, cx, cy+ry)
	z.CubeTo(cx-kx, cy+ry, cx-rx, cy+ky, cx-rx, cy)
	z.CubeTo(cx-rx, cy-ky, cx-kx, cy-ry, cx, cy-ry)
	z.CubeTo(cx+kx, cy-ry, cx+rx, cy-ky, cx+rx, cy)
	z.ClosePath()
}

// bar adds a straight stroke w wide from (x0,y0) to (x1,y1).
func bar(z *vector.Rasterizer, x0, y0, x1, y1, w float32) {
	dx, dy := x1-x0, y1-y0
	l := float32(math.Hypot(float64(dx), float64(dy)))
	if l == 0 {
		return
	}
	nx, ny := -dy/l*w/2, dx/l*w/2
	z.MoveTo(x0+nx, y0+ny)
	z.LineTo(x1+nx, y1+ny)
	z.LineTo(x1-nx, y1-ny)
	z.LineTo(x0-nx, y0-ny)
	z.ClosePath()
}

func poly(z *vector.Rasterizer, ps []pt) {
	z.MoveTo(ps[0].x, ps[0].y)
	for _, p := range ps[1:] {
		z.LineTo(p.x, p.y)
	}
	z.ClosePath()
}

func rot(x, y, a float32) (float32, float32) {
	s, c := math.Sincos(float64(a))
	return x*float32(c) - y*float32(s), x*float32(s) + y*float32(c)
}

// heartPath: tip at the bottom, r is about half the width.
func heartPath(z *vector.Rasterizer, cx, cy, r, angle float32) {
	p := func(x, y float32) (float32, float32) { x, y = rot(x*r, y*r, angle); return cx + x, cy + y }
	x0, y0 := p(0, 0.9)
	z.MoveTo(x0, y0)
	a1, b1 := p(-1.35, -0.05)
	a2, b2 := p(-0.65, -1.15)
	a3, b3 := p(0, -0.42)
	z.CubeTo(a1, b1, a2, b2, a3, b3)
	c1, d1 := p(0.65, -1.15)
	c2, d2 := p(1.35, -0.05)
	z.CubeTo(c1, d1, c2, d2, x0, y0)
	z.ClosePath()
}

var mapleLeaf = []pt{
	{0, -1}, {0.14, -0.72}, {0.34, -0.8}, {0.27, -0.34}, {0.56, -0.64}, {0.63, -0.46}, {0.92, -0.53},
	{0.8, -0.22}, {0.96, -0.12}, {0.55, 0.2}, {0.62, 0.38}, {0.07, 0.3}, {0.07, 0.78},
	{-0.07, 0.78}, {-0.07, 0.3}, {-0.62, 0.38}, {-0.55, 0.2}, {-0.96, -0.12}, {-0.8, -0.22},
	{-0.92, -0.53}, {-0.63, -0.46}, {-0.56, -0.64}, {-0.27, -0.34}, {-0.34, -0.8}, {-0.14, -0.72},
}

func scaled(ps []pt, cx, cy, r float32) []pt {
	out := make([]pt, len(ps))
	for i, p := range ps {
		out[i] = pt{cx + p.x*r, cy + p.y*r}
	}
	return out
}

func starPts(cx, cy, r, inner float32) []pt {
	var ps []pt
	for i := 0; i < 10; i++ {
		rr := r
		if i%2 == 1 {
			rr = r * inner
		}
		a := float64(i)*math.Pi/5 - math.Pi/2
		ps = append(ps, pt{cx + rr*float32(math.Cos(a)), cy + rr*float32(math.Sin(a))})
	}
	return ps
}

// drawOrnament paints ornament kind into layer, centred on (cx,cy) with radius r.
func drawOrnament(layer *image.RGBA, kind string, cx, cy, r float32) {
	f := func(c string, build func(z *vector.Rasterizer)) { fill(layer, ParseHex(c), build) }
	switch kind {
	case "pumpkin":
		f("#C95F12", func(z *vector.Rasterizer) {
			oval(z, cx-0.45*r, cy+0.1*r, 0.55*r, 0.72*r)
			oval(z, cx+0.45*r, cy+0.1*r, 0.55*r, 0.72*r)
		})
		f("#F28C28", func(z *vector.Rasterizer) { oval(z, cx, cy+0.1*r, 0.52*r, 0.78*r) })
		f("#3E7B27", func(z *vector.Rasterizer) { bar(z, cx, cy-0.55*r, cx+0.18*r, cy-0.98*r, 0.2*r) })
		f("#3A1A00", func(z *vector.Rasterizer) {
			poly(z, []pt{{cx - 0.5*r, cy - 0.05*r}, {cx - 0.2*r, cy - 0.05*r}, {cx - 0.35*r, cy - 0.3*r}})
			poly(z, []pt{{cx + 0.2*r, cy - 0.05*r}, {cx + 0.5*r, cy - 0.05*r}, {cx + 0.35*r, cy - 0.3*r}})
			poly(z, []pt{{cx - 0.5*r, cy + 0.25*r}, {cx - 0.25*r, cy + 0.38*r}, {cx, cy + 0.27*r}, {cx + 0.25*r, cy + 0.38*r},
				{cx + 0.5*r, cy + 0.25*r}, {cx + 0.3*r, cy + 0.55*r}, {cx - 0.3*r, cy + 0.55*r}})
		})
	case "snowflake":
		f("#FFFFFF", func(z *vector.Rasterizer) {
			for i := 0; i < 6; i++ {
				a := float32(i) * math.Pi / 3
				ex, ey := rot(0, -r, a)
				bar(z, cx, cy, cx+ex, cy+ey, 0.14*r)
				for _, side := range []float32{-1, 1} {
					bx, by := rot(0, -0.55*r, a)
					tx, ty := rot(side*0.28*r, -0.85*r, a)
					bar(z, cx+bx, cy+by, cx+tx, cy+ty, 0.11*r)
				}
			}
			oval(z, cx, cy, 0.16*r, 0.16*r)
		})
	case "heart":
		f("#E8335F", func(z *vector.Rasterizer) { heartPath(z, cx, cy+0.05*r, 0.95*r, 0) })
		f("#FF9DB5", func(z *vector.Rasterizer) { oval(z, cx-0.42*r, cy-0.3*r, 0.13*r, 0.2*r) })
	case "shamrock":
		f("#4CC96E", func(z *vector.Rasterizer) {
			for i := 0; i < 3; i++ {
				a := float32(i)*2*math.Pi/3 + math.Pi
				hx, hy := rot(0, 0.42*r, a)
				heartPath(z, cx+hx, cy-0.12*r+hy, 0.52*r, a+math.Pi) // tips meet in the middle
			}
			bar(z, cx, cy, cx+0.3*r, cy+0.95*r, 0.13*r)
		})
	case "egg":
		f("#F7C6D9", func(z *vector.Rasterizer) {
			z.MoveTo(cx, cy-r)
			z.CubeTo(cx+0.55*r, cy-r, cx+0.78*r, cy+0.05*r, cx+0.72*r, cy+0.35*r)
			z.CubeTo(cx+0.62*r, cy+0.85*r, cx+0.3*r, cy+r, cx, cy+r)
			z.CubeTo(cx-0.3*r, cy+r, cx-0.62*r, cy+0.85*r, cx-0.72*r, cy+0.35*r)
			z.CubeTo(cx-0.78*r, cy+0.05*r, cx-0.55*r, cy-r, cx, cy-r)
			z.ClosePath()
		})
		f("#7FC4F0", func(z *vector.Rasterizer) {
			var ps []pt
			for i := 0; i <= 6; i++ {
				x := cx - 0.66*r + float32(i)*0.22*r
				y := cy + 0.02*r
				if i%2 == 1 {
					y -= 0.14 * r
				}
				ps = append(ps, pt{x, y})
			}
			for i := 6; i >= 0; i-- {
				ps = append(ps, pt{ps[i].x, ps[i].y + 0.2*r})
			}
			poly(z, ps)
		})
		f("#B79CED", func(z *vector.Rasterizer) {
			for _, d := range []pt{{-0.3, -0.45}, {0.15, -0.6}, {0.35, -0.3}, {-0.35, 0.55}, {0.05, 0.6}, {0.38, 0.5}} {
				oval(z, cx+d.x*r, cy+d.y*r, 0.08*r, 0.08*r)
			}
		})
	case "maple-white", "maple-orange", "maple-red":
		c := map[string]string{"maple-white": "#FFFFFF", "maple-orange": "#F7A33A", "maple-red": "#D52B1E"}[kind]
		f(c, func(z *vector.Rasterizer) { poly(z, scaled(mapleLeaf, cx, cy, r)) })
	case "star-white", "star-gold":
		c := map[string]string{"star-white": "#FFFFFF", "star-gold": "#F2C94C"}[kind]
		f(c, func(z *vector.Rasterizer) { poly(z, starPts(cx, cy+0.08*r, r, 0.42)) })
	case "lantern":
		f("#C7A23A", func(z *vector.Rasterizer) { bar(z, cx, cy-r, cx, cy-0.6*r, 0.08*r) })
		f("#D7261E", func(z *vector.Rasterizer) { oval(z, cx, cy, 0.78*r, 0.6*r) })
		f("#F2C14E", func(z *vector.Rasterizer) {
			roundRectPath(z, cx-0.36*r, cy-0.72*r, cx+0.36*r, cy-0.5*r, 0.06*r, 0.06*r, false)
			roundRectPath(z, cx-0.36*r, cy+0.5*r, cx+0.36*r, cy+0.72*r, 0.06*r, 0.06*r, false)
			bar(z, cx, cy+0.72*r, cx, cy+r, 0.12*r)
		})
		f("#A3121B", func(z *vector.Rasterizer) {
			oval(z, cx, cy, 0.3*r, 0.58*r)
			oval(z, cx, cy, 0.22*r, 0.52*r)
		})
	case "tree":
		f("#6B4423", func(z *vector.Rasterizer) { bar(z, cx, cy+0.6*r, cx, cy+r, 0.24*r) })
		f("#2E9E48", func(z *vector.Rasterizer) {
			poly(z, []pt{{cx, cy - 0.78*r}, {cx + 0.48*r, cy - 0.1*r}, {cx - 0.48*r, cy - 0.1*r}})
			poly(z, []pt{{cx, cy - 0.45*r}, {cx + 0.68*r, cy + 0.32*r}, {cx - 0.68*r, cy + 0.32*r}})
			poly(z, []pt{{cx, cy - 0.12*r}, {cx + 0.85*r, cy + 0.68*r}, {cx - 0.85*r, cy + 0.68*r}})
		})
		f("#E8423A", func(z *vector.Rasterizer) {
			oval(z, cx-0.25*r, cy+0.05*r, 0.09*r, 0.09*r)
			oval(z, cx+0.35*r, cy+0.45*r, 0.09*r, 0.09*r)
		})
		f("#4FA3F7", func(z *vector.Rasterizer) { oval(z, cx+0.15*r, cy-0.25*r, 0.08*r, 0.08*r) })
		f("#F2C94C", func(z *vector.Rasterizer) {
			oval(z, cx-0.4*r, cy+0.5*r, 0.08*r, 0.08*r)
			poly(z, starPts(cx, cy-0.82*r, 0.24*r, 0.45))
		})
	case "sun":
		f("#F7A928", func(z *vector.Rasterizer) {
			for i := 0; i < 8; i++ {
				a := float32(i) * math.Pi / 4
				x0, y0 := rot(-0.14*r, -0.62*r, a)
				x1, y1 := rot(0.14*r, -0.62*r, a)
				x2, y2 := rot(0, -r, a)
				poly(z, []pt{{cx + x0, cy + y0}, {cx + x2, cy + y2}, {cx + x1, cy + y1}})
			}
		})
		f("#FFCE3D", func(z *vector.Rasterizer) { oval(z, cx, cy, 0.52*r, 0.52*r) })
	case "flower":
		f("#F49AC2", func(z *vector.Rasterizer) {
			for i := 0; i < 5; i++ {
				x, y := rot(0, -0.5*r, float32(i)*2*math.Pi/5)
				oval(z, cx+x, cy+y, 0.4*r, 0.4*r)
			}
		})
		f("#FFD84D", func(z *vector.Rasterizer) { oval(z, cx, cy, 0.3*r, 0.3*r) })
	}
}

// DrawOrnament blends an ornament onto dst at (cx,cy), r in px, with
// opacity a (0..1).
func DrawOrnament(dst *image.RGBA, kind string, cx, cy, r, a float32) {
	if kind == "" || r < 3 || a <= 0 {
		return
	}
	pad := int(math.Ceil(float64(r) * 1.25))
	box := image.Rect(int(cx)-pad, int(cy)-pad, int(cx)+pad, int(cy)+pad).Intersect(dst.Bounds())
	if box.Empty() {
		return
	}
	layer := image.NewRGBA(image.Rect(0, 0, box.Dx(), box.Dy()))
	drawOrnament(layer, kind, cx-float32(box.Min.X), cy-float32(box.Min.Y), r)
	draw.DrawMask(dst, box, layer, image.Point{}, image.NewUniform(color.Alpha{uint8(min(255, a*255))}), image.Point{}, draw.Over)
}

// Background image fit modes.
const (
	BgFill    = "" // cover the plot, cropping the edges
	BgFit     = "fit"
	BgStretch = "stretch"
	BgTile    = "tile"
)

var BgFits = []struct{ Key, Label string }{
	{BgFill, N("Fill (crop to fit)")},
	{BgFit, N("Fit (show all of it)")},
	{BgStretch, N("Stretch")},
	{BgTile, N("Tile")},
}

// FitImage lays src out in a w x h image the way mode says.
func FitImage(src image.Image, w, h int, mode string) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if src == nil || w <= 0 || h <= 0 {
		return dst
	}
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw <= 0 || sh <= 0 {
		return dst
	}
	switch mode {
	case BgStretch:
		xdraw.BiLinear.Scale(dst, dst.Bounds(), src, sb, draw.Src, nil)
	case BgTile:
		for y := 0; y < h; y += sh {
			for x := 0; x < w; x += sw {
				draw.Draw(dst, image.Rect(x, y, x+sw, y+sh), src, sb.Min, draw.Src)
			}
		}
	case BgFit:
		k := math.Min(float64(w)/float64(sw), float64(h)/float64(sh))
		dw, dh := int(math.Round(float64(sw)*k)), int(math.Round(float64(sh)*k))
		r := image.Rect((w-dw)/2, (h-dh)/2, (w-dw)/2+dw, (h-dh)/2+dh)
		xdraw.BiLinear.Scale(dst, r, src, sb, draw.Src, nil)
	default: // fill
		k := math.Max(float64(w)/float64(sw), float64(h)/float64(sh))
		cw, ch := int(math.Round(float64(w)/k)), int(math.Round(float64(h)/k))
		cr := image.Rect(sb.Min.X+(sw-cw)/2, sb.Min.Y+(sh-ch)/2, 0, 0)
		cr.Max = cr.Min.Add(image.Pt(cw, ch))
		xdraw.BiLinear.Scale(dst, dst.Bounds(), src, cr.Intersect(sb), draw.Src, nil)
	}
	return dst
}
