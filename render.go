package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

var (
	fontMu    sync.Mutex
	fonts     = map[string]*opentype.Font{}
	faceCache = map[string]font.Face{}
)

// loadFontFile parses a .ttf/.otf/.ttc file ("" = default Segoe UI Semibold).
func loadFontFile(path string) *opentype.Font {
	if f, ok := fonts[path]; ok {
		return f
	}
	var f *opentype.Font
	try := func(p string) {
		if f != nil || p == "" {
			return
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return
		}
		if ff, err := opentype.Parse(b); err == nil {
			f = ff
			return
		}
		if c, err := opentype.ParseCollection(b); err == nil && c.NumFonts() > 0 {
			f, _ = c.Font(0)
		}
	}
	try(path)
	win := os.Getenv("WINDIR")
	if win == "" {
		win = `C:\Windows`
	}
	for _, name := range []string{"seguisb.ttf", "segoeuib.ttf", "segoeui.ttf"} {
		try(filepath.Join(win, "Fonts", name))
	}
	if f == nil {
		f, _ = opentype.Parse(gobold.TTF)
	}
	fonts[path] = f
	return f
}

func faceFor(path string, px float64) font.Face {
	fontMu.Lock()
	defer fontMu.Unlock()
	px = math.Round(px*2) / 2
	key := fmt.Sprintf("%s|%.1f", path, px)
	if f, ok := faceCache[key]; ok {
		return f
	}
	ff, err := opentype.NewFace(loadFontFile(path), &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil
	}
	faceCache[key] = &fallbackFace{Face: ff, px: px}
	return ff
}

func face(px float64) font.Face { return faceFor("", px) }

// fallbackFaceFiles are Windows fonts tried, in order, for characters the
// chosen font doesn't have (Chinese, Japanese, Korean, symbols), so titles,
// file names and translated text never show as empty boxes.
var fallbackFaceFiles = []string{"msyh.ttc", "YuGothM.ttc", "meiryo.ttc", "malgun.ttf", "seguisym.ttf"}

var (
	fallbackOnce  sync.Once
	fallbackFonts []*opentype.Font
)

func loadFallbackFonts() {
	win := os.Getenv("WINDIR")
	if win == "" {
		win = `C:\Windows`
	}
	for _, name := range fallbackFaceFiles {
		b, err := os.ReadFile(filepath.Join(win, "Fonts", name))
		if err != nil {
			continue
		}
		if f, err := opentype.Parse(b); err == nil {
			fallbackFonts = append(fallbackFonts, f)
		} else if c, err := opentype.ParseCollection(b); err == nil && c.NumFonts() > 0 {
			if f, err := c.Font(0); err == nil {
				fallbackFonts = append(fallbackFonts, f)
			}
		}
	}
}

// fallbackFace draws with Face and borrows missing glyphs from fallback fonts.
type fallbackFace struct {
	font.Face
	px    float64
	extra []font.Face // created on first miss
	tried bool
}

func (f *fallbackFace) pick(r rune) font.Face {
	if _, ok := f.Face.GlyphAdvance(r); ok || r < 0x80 {
		return f.Face
	}
	if !f.tried {
		f.tried = true
		fallbackOnce.Do(loadFallbackFonts)
		for _, ff := range fallbackFonts {
			if fc, err := opentype.NewFace(ff, &opentype.FaceOptions{Size: f.px, DPI: 72, Hinting: font.HintingFull}); err == nil {
				f.extra = append(f.extra, fc)
			}
		}
	}
	for _, e := range f.extra {
		if _, ok := e.GlyphAdvance(r); ok {
			return e
		}
	}
	return f.Face
}

func (f *fallbackFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	return f.pick(r).Glyph(dot, r)
}

func (f *fallbackFace) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	return f.pick(r).GlyphBounds(r)
}

func (f *fallbackFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	return f.pick(r).GlyphAdvance(r)
}

func (f *fallbackFace) Kern(r0, r1 rune) fixed.Int26_6 {
	if f.pick(r0) != f.Face || f.pick(r1) != f.Face {
		return 0
	}
	return f.Face.Kern(r0, r1)
}

// Header styles
const (
	HeaderShade  = 0 // darker shade of the plot colour
	HeaderSame   = 1 // same as the plot body
	HeaderCustom = 2 // HeaderColor
	HeaderNone   = 3 // no band, title only
)

type RenderOpts struct {
	W, H       int
	Scale      float64
	TitleH     int
	Title      string
	Color      color.NRGBA
	Opacity    int // 0-100
	TitlePt    int
	FontFile   string
	Header     int
	HeaderCol  color.NRGBA
	TitleCol   color.NRGBA
	Grip       bool // show the "drag to move" indicator
	Radius     int  // corner radius in px at 100% scale; -1 = default (8)
	Collapsed  bool
	Chevron    bool // draw the roll-up button
	TabNames   []string
	ActiveTab  int
	TabRects   []Rect // relative to the plot
	TabBar     Rect
	ScrollPos  int // first visible row
	ScrollMax  int
	Rows       int
	Inbox      bool
	Edit       bool // edit-mode overlay look
	Selected   bool
	EmptyHint  bool
	DropTarget bool // another plot or tab is being dropped here
}

const kappa = 0.5522848

func roundRectPath(z *vector.Rasterizer, x0, y0, x1, y1, rt, rb float32, ccw bool) {
	if !ccw {
		z.MoveTo(x0+rt, y0)
		z.LineTo(x1-rt, y0)
		z.CubeTo(x1-rt+rt*kappa, y0, x1, y0+rt-rt*kappa, x1, y0+rt)
		z.LineTo(x1, y1-rb)
		z.CubeTo(x1, y1-rb+rb*kappa, x1-rb+rb*kappa, y1, x1-rb, y1)
		z.LineTo(x0+rb, y1)
		z.CubeTo(x0+rb-rb*kappa, y1, x0, y1-rb+rb*kappa, x0, y1-rb)
		z.LineTo(x0, y0+rt)
		z.CubeTo(x0, y0+rt-rt*kappa, x0+rt-rt*kappa, y0, x0+rt, y0)
		z.ClosePath()
		return
	}
	z.MoveTo(x0+rt, y0)
	z.CubeTo(x0+rt-rt*kappa, y0, x0, y0+rt-rt*kappa, x0, y0+rt)
	z.LineTo(x0, y1-rb)
	z.CubeTo(x0, y1-rb+rb*kappa, x0+rb-rb*kappa, y1, x0+rb, y1)
	z.LineTo(x1-rb, y1)
	z.CubeTo(x1-rb+rb*kappa, y1, x1, y1-rb+rb*kappa, x1, y1-rb)
	z.LineTo(x1, y0+rt)
	z.CubeTo(x1, y0+rt-rt*kappa, x1-rt+rt*kappa, y0, x1-rt, y0)
	z.ClosePath()
}

func fill(dst *image.RGBA, c color.NRGBA, build func(z *vector.Rasterizer)) {
	b := dst.Bounds()
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	build(z)
	z.Draw(dst, b, image.NewUniform(c), image.Point{})
}

func shade(c color.NRGBA, f float64) color.NRGBA {
	m := func(v uint8) uint8 { return uint8(math.Max(0, math.Min(255, float64(v)*f))) }
	return color.NRGBA{m(c.R), m(c.G), m(c.B), c.A}
}

func alpha(c color.NRGBA, a float64) color.NRGBA {
	c.A = uint8(math.Max(0, math.Min(255, a*255)))
	return c
}

// RenderPlot draws a plot panel into a premultiplied RGBA image.
func RenderPlot(o RenderOpts) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, o.W, o.H))
	s := o.Scale
	w, h := float32(o.W), float32(o.H)
	r := float32(float64(o.Radius) * s)
	if o.Radius < 0 {
		r = float32(8 * s)
	}
	if maxR := float32(math.Min(float64(o.W), float64(o.H))) / 2; r > maxR {
		r = maxR
	}
	op := float64(o.Opacity) / 100
	if o.Edit {
		op = math.Max(op, 0.35) * 0.85
	}
	th := float32(o.TitleH)
	hb := float32(0) // header bottom radius
	if o.Collapsed {
		hb = r
	}

	// body
	fill(img, alpha(o.Color, op), func(z *vector.Rasterizer) { roundRectPath(z, 0, 0, w, h, r, r, false) })
	// title band
	switch o.Header {
	case HeaderShade:
		tc := shade(o.Color, 0.72)
		fill(img, alpha(tc, math.Min(1, op*0.6+0.25)), func(z *vector.Rasterizer) { roundRectPath(z, 0, 0, w, th, r, hb, false) })
	case HeaderCustom:
		fill(img, alpha(o.HeaderCol, math.Min(1, op*0.6+0.25)), func(z *vector.Rasterizer) { roundRectPath(z, 0, 0, w, th, r, hb, false) })
	}
	// border ring
	bw := float32(math.Max(1, math.Round(1*s)))
	bc := color.NRGBA{255, 255, 255, 46}
	if o.Edit {
		bw = float32(math.Max(2, math.Round(2*s)))
		bc = color.NRGBA{120, 200, 255, 230}
		if o.Selected {
			bc = color.NRGBA{255, 210, 90, 240}
		}
	}
	if o.DropTarget {
		bw = float32(math.Max(3, math.Round(3*s)))
		bc = color.NRGBA{79, 195, 247, 255}
	}
	fill(img, bc, func(z *vector.Rasterizer) {
		roundRectPath(z, 0, 0, w, h, r, r, false)
		roundRectPath(z, bw, bw, w-bw, h-bw, max(0, r-bw), max(0, r-bw), true)
	})

	// title text
	f := faceFor(o.FontFile, float64(o.TitlePt)*96/72*s)
	if f != nil {
		title := o.Title
		maxW := fixed.I(o.W) - fixed.I(int(60*s)) - fixed.I(o.TitleH)
		d := &font.Drawer{Dst: img, Face: f}
		if d.MeasureString(title) > maxW {
			rs := []rune(title)
			for len(rs) > 0 && d.MeasureString(string(rs)+"…") > maxW {
				rs = rs[:len(rs)-1]
			}
			title = string(rs) + "…"
		}
		m := f.Metrics()
		tw := d.MeasureString(title)
		x := (fixed.I(o.W) - tw) / 2
		y := (fixed.I(o.TitleH)-(m.Ascent+m.Descent))/2 + m.Ascent
		d.Src = image.NewUniform(color.NRGBA{0, 0, 0, 110})
		d.Dot = fixed.Point26_6{X: x + fixed.I(1), Y: y + fixed.I(1)}
		d.DrawString(title)
		tcol := o.TitleCol
		if tcol.A == 0 {
			tcol = color.NRGBA{255, 255, 255, 245}
		}
		d.Src = image.NewUniform(tcol)
		d.Dot = fixed.Point26_6{X: x, Y: y}
		d.DrawString(title)
	}

	// move indicator: four-way arrow at the title's left
	if o.Grip {
		cx, cy := float32(16*s), th/2
		u := float32(s)
		col := color.NRGBA{255, 255, 255, 170}
		a, l, t := 6*u, 2.6*u, 0.9*u // arm length, head size, shaft half-width
		fill(img, col, func(z *vector.Rasterizer) {
			// horizontal shaft
			z.MoveTo(cx-a+l, cy-t)
			z.LineTo(cx+a-l, cy-t)
			z.LineTo(cx+a-l, cy+t)
			z.LineTo(cx-a+l, cy+t)
			z.ClosePath()
			// vertical shaft
			z.MoveTo(cx-t, cy-a+l)
			z.LineTo(cx+t, cy-a+l)
			z.LineTo(cx+t, cy+a-l)
			z.LineTo(cx-t, cy+a-l)
			z.ClosePath()
			// heads
			z.MoveTo(cx+a, cy)
			z.LineTo(cx+a-l, cy-l)
			z.LineTo(cx+a-l, cy+l)
			z.ClosePath()
			z.MoveTo(cx-a, cy)
			z.LineTo(cx-a+l, cy+l)
			z.LineTo(cx-a+l, cy-l)
			z.ClosePath()
			z.MoveTo(cx, cy-a)
			z.LineTo(cx+l, cy-a+l)
			z.LineTo(cx-l, cy-a+l)
			z.ClosePath()
			z.MoveTo(cx, cy+a)
			z.LineTo(cx-l, cy+a-l)
			z.LineTo(cx+l, cy+a-l)
			z.ClosePath()
		})
	}

	// roll-up chevron: ^ collapses, v expands
	if o.Chevron {
		cx, cy := w-th/2, th/2
		u := float32(s)
		a, sw := 5*u, 1.7*u
		col := color.NRGBA{255, 255, 255, 200}
		dir := float32(1) // pointing up
		if o.Collapsed {
			dir = -1
		}
		fill(img, col, func(z *vector.Rasterizer) {
			y0 := cy + dir*a/2
			y1 := cy - dir*a/2
			z.MoveTo(cx-a, y0)
			z.LineTo(cx, y1)
			z.LineTo(cx+a, y0)
			z.LineTo(cx+a, y0+dir*sw)
			z.LineTo(cx, y1+dir*sw)
			z.LineTo(cx-a, y0+dir*sw)
			z.ClosePath()
		})
	}

	// tab strip
	if len(o.TabNames) > 1 && o.TabBar.H() > 0 && !o.Collapsed {
		tb := o.TabBar
		band := shade(o.Color, 0.82)
		fill(img, alpha(band, math.Min(1, op*0.5+0.18)), func(z *vector.Rasterizer) {
			z.MoveTo(0, float32(tb.T))
			z.LineTo(w, float32(tb.T))
			z.LineTo(w, float32(tb.B))
			z.LineTo(0, float32(tb.B))
			z.ClosePath()
		})
		tf := faceFor(o.FontFile, float64(max(8, o.TitlePt-1))*96/72*s)
		for i, tr := range o.TabRects {
			if i >= len(o.TabNames) {
				break
			}
			active := i == o.ActiveTab
			if active {
				fill(img, color.NRGBA{255, 255, 255, 34}, func(z *vector.Rasterizer) {
					roundRectPath(z, float32(tr.L)+2, float32(tr.T)+3, float32(tr.R)-2, float32(tr.B), float32(5*s), 0, false)
				})
				ul := float32(math.Max(2, math.Round(2*s)))
				fill(img, color.NRGBA{255, 255, 255, 230}, func(z *vector.Rasterizer) {
					roundRectPath(z, float32(tr.L)+float32(8*s), float32(tr.B)-ul, float32(tr.R)-float32(8*s), float32(tr.B), ul/2, ul/2, false)
				})
			}
			if tf == nil {
				continue
			}
			name := o.TabNames[i]
			d := &font.Drawer{Dst: img, Face: tf}
			maxTW := fixed.I(tr.W() - int(12*s))
			if d.MeasureString(name) > maxTW {
				rs := []rune(name)
				for len(rs) > 0 && d.MeasureString(string(rs)+"…") > maxTW {
					rs = rs[:len(rs)-1]
				}
				name = string(rs) + "…"
			}
			m := tf.Metrics()
			tw := d.MeasureString(name)
			alphaT := uint8(150)
			if active {
				alphaT = 250
			}
			d.Src = image.NewUniform(color.NRGBA{255, 255, 255, alphaT})
			d.Dot = fixed.Point26_6{X: fixed.I(tr.L) + (fixed.I(tr.W())-tw)/2, Y: fixed.I(tr.T) + (fixed.I(tr.H())-(m.Ascent+m.Descent))/2 + m.Ascent}
			d.DrawString(name)
		}
	}

	// scroll bar
	if o.ScrollMax > 0 && !o.Collapsed && o.Rows > 0 {
		top := float32(o.TitleH + o.TabBar.H() + int(6*s))
		bot := h - float32(8*s)
		x1 := w - float32(4*s)
		x0 := x1 - float32(math.Max(3, 4*s))
		total := float32(o.Rows + o.ScrollMax)
		th0 := (bot - top) * float32(o.Rows) / total
		ty := top + (bot-top-th0)*float32(o.ScrollPos)/float32(o.ScrollMax)
		rr := (x1 - x0) / 2
		fill(img, color.NRGBA{255, 255, 255, 40}, func(z *vector.Rasterizer) { roundRectPath(z, x0, top, x1, bot, rr, rr, false) })
		fill(img, color.NRGBA{255, 255, 255, 190}, func(z *vector.Rasterizer) { roundRectPath(z, x0, ty, x1, ty+th0, rr, rr, false) })
	}

	// inbox marker: a small "download" arrow in the title's right corner
	if o.Inbox {
		cx, cy := w-float32(18*s), th/2
		if o.Chevron {
			cx -= th - float32(6*s)
		}
		u := float32(s)
		col := color.NRGBA{255, 255, 255, 200}
		fill(img, col, func(z *vector.Rasterizer) {
			z.MoveTo(cx-1.5*u, cy-7*u)
			z.LineTo(cx+1.5*u, cy-7*u)
			z.LineTo(cx+1.5*u, cy-1*u)
			z.LineTo(cx+4.5*u, cy-1*u)
			z.LineTo(cx, cy+4*u)
			z.LineTo(cx-4.5*u, cy-1*u)
			z.LineTo(cx-1.5*u, cy-1*u)
			z.ClosePath()
			z.MoveTo(cx-6*u, cy+5.5*u)
			z.LineTo(cx+6*u, cy+5.5*u)
			z.LineTo(cx+6*u, cy+7*u)
			z.LineTo(cx-6*u, cy+7*u)
			z.ClosePath()
		})
	}

	if o.Edit {
		// resize grip (bottom-right)
		g := float32(14 * s)
		gc := color.NRGBA{255, 255, 255, 170}
		for i := float32(0); i < 3; i++ {
			off := (i*4 + 3) * float32(s)
			fill(img, gc, func(z *vector.Rasterizer) {
				z.MoveTo(w-off-bw, h-bw-1)
				z.LineTo(w-bw-1, h-off-bw)
				z.LineTo(w-bw-1, h-off-bw+1.6*float32(s))
				z.LineTo(w-off-bw+1.6*float32(s), h-bw-1)
				z.ClosePath()
			})
		}
		_ = g
		hf := face(9 * 96 / 72 * s)
		if hf != nil {
			lines := []string{T("Drag to move · drag edges to resize"), T("Double-click to edit · right-click for options")}
			d := &font.Drawer{Dst: img, Face: hf, Src: image.NewUniform(color.NRGBA{255, 255, 255, 220})}
			m := hf.Metrics()
			lh := (m.Ascent + m.Descent) * 5 / 4
			y := fixed.I(o.TitleH) + (fixed.I(o.H-o.TitleH)-lh*2)/2 + m.Ascent
			for _, ln := range lines {
				tw := d.MeasureString(ln)
				if tw > fixed.I(o.W-8) {
					y += lh
					continue
				}
				d.Dot = fixed.Point26_6{X: (fixed.I(o.W) - tw) / 2, Y: y}
				d.DrawString(ln)
				y += lh
			}
		}
	}
	return img
}

// ToBGRA converts premultiplied RGBA to premultiplied BGRA in place.
func ToBGRA(img *image.RGBA) []byte {
	p := img.Pix
	for i := 0; i+3 < len(p); i += 4 {
		p[i], p[i+2] = p[i+2], p[i]
	}
	return p
}

var _ = draw.Over

// RenderToggleRow draws a white row with a Windows 11 style toggle switch
// right-aligned (the label is drawn by GDI on top).
func RenderToggleRow(w, h int, s float64, on, enabled bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{255, 255, 255, 255}), image.Point{}, draw.Src)
	tw, th := float32(40*s), float32(20*s)
	x1 := float32(w) - float32(4*s)
	x0 := x1 - tw
	y0 := (float32(h) - th) / 2
	y1 := y0 + th
	rr := th / 2
	accent := color.NRGBA{0, 103, 192, 255}
	grey := color.NRGBA{120, 120, 120, 255}
	if !enabled {
		accent = color.NRGBA{170, 190, 215, 255}
		grey = color.NRGBA{190, 190, 190, 255}
	}
	kr := float32(6 * s)
	if on {
		fill(img, accent, func(z *vector.Rasterizer) { roundRectPath(z, x0, y0, x1, y1, rr, rr, false) })
		circle(img, x1-rr, y0+rr, kr+float32(0.5*s), color.NRGBA{255, 255, 255, 255})
	} else {
		bw := float32(math.Max(1, math.Round(s)))
		fill(img, grey, func(z *vector.Rasterizer) {
			roundRectPath(z, x0, y0, x1, y1, rr, rr, false)
			roundRectPath(z, x0+bw, y0+bw, x1-bw, y1-bw, rr-bw, rr-bw, true)
		})
		circle(img, x0+rr, y0+rr, kr-float32(0.5*s), grey)
	}
	return img
}

func circle(img *image.RGBA, cx, cy, r float32, c color.NRGBA) {
	fill(img, c, func(z *vector.Rasterizer) { roundRectPath(z, cx-r, cy-r, cx+r, cy+r, r, r, false) })
}

// FolderCell is one entry drawn inside a live-folder plot (coords relative to the plot).
type FolderCell struct {
	Icon     image.Image
	Name     string
	X, Y     int // cell top-left
	W, H     int // cell size
	Selected bool
}

// DrawFolderCells paints icons and two-line labels, Explorer style.
func DrawFolderCells(img *image.RGBA, cells []FolderCell, iconPx int, scale float64) {
	f := labelFace(9 * 96 / 72 * scale)
	for _, c := range cells {
		if c.Selected {
			fill(img, color.NRGBA{255, 255, 255, 60}, func(z *vector.Rasterizer) {
				roundRectPath(z, float32(c.X+2), float32(c.Y), float32(c.X+c.W-2), float32(c.Y+c.H-4), float32(4*scale), float32(4*scale), false)
			})
		}
		ix := c.X + (c.W-iconPx)/2
		iy := c.Y + int(4*scale)
		if c.Icon != nil {
			b := c.Icon.Bounds()
			dst := image.Rect(ix, iy, ix+iconPx, iy+iconPx)
			if b.Dx() != iconPx || b.Dy() != iconPx {
				// keep aspect ratio (thumbnails)
				s := math.Min(float64(iconPx)/float64(b.Dx()), float64(iconPx)/float64(b.Dy()))
				w, h := int(float64(b.Dx())*s), int(float64(b.Dy())*s)
				dst = image.Rect(ix+(iconPx-w)/2, iy+(iconPx-h), ix+(iconPx-w)/2+w, iy+iconPx)
				xdrawScale(img, dst, c.Icon)
			} else {
				draw.Draw(img, dst, c.Icon, b.Min, draw.Over)
			}
		}
		if f == nil {
			continue
		}
		lines := wrapLabel(f, c.Name, fixed.I(c.W-int(6*scale)), 2)
		m := f.Metrics()
		lh := m.Ascent + m.Descent
		y := fixed.I(iy+iconPx+int(4*scale)) + m.Ascent
		for _, ln := range lines {
			d := &font.Drawer{Dst: img, Face: f}
			tw := d.MeasureString(ln)
			x := fixed.I(c.X) + (fixed.I(c.W)-tw)/2
			d.Src = image.NewUniform(color.NRGBA{0, 0, 0, 150})
			d.Dot = fixed.Point26_6{X: x + fixed.I(1), Y: y + fixed.I(1)}
			d.DrawString(ln)
			d.Src = image.NewUniform(color.NRGBA{255, 255, 255, 250})
			d.Dot = fixed.Point26_6{X: x, Y: y}
			d.DrawString(ln)
			y += lh
		}
	}
}

// wrapLabel splits a file name into at most n lines that fit maxW, with an
// ellipsis on the last line when it doesn't fit.
func wrapLabel(f font.Face, s string, maxW fixed.Int26_6, n int) []string {
	d := &font.Drawer{Face: f}
	var lines []string
	rs := []rune(s)
	for len(rs) > 0 && len(lines) < n {
		if len(lines) == n-1 || d.MeasureString(string(rs)) <= maxW {
			line := string(rs)
			if d.MeasureString(line) > maxW {
				for len(rs) > 0 && d.MeasureString(string(rs)+"…") > maxW {
					rs = rs[:len(rs)-1]
				}
				line = string(rs) + "…"
			}
			lines = append(lines, line)
			break
		}
		// longest prefix that fits, preferring a break at a space / . / - / _
		cut := 1
		for cut < len(rs) && d.MeasureString(string(rs[:cut+1])) <= maxW {
			cut++
		}
		brk := cut
		for i := cut; i > cut/2; i-- {
			if strings.ContainsRune(" ._-", rs[i-1]) {
				brk = i
				break
			}
		}
		lines = append(lines, strings.TrimSpace(string(rs[:brk])))
		rs = rs[brk:]
	}
	return lines
}

func xdrawScale(dst *image.RGBA, r image.Rectangle, src image.Image) {
	xdraw.ApproxBiLinear.Scale(dst, r, src, src.Bounds(), draw.Over, nil)
}

// labelFace is the regular-weight UI font used for icon labels.
func labelFace(px float64) font.Face {
	win := os.Getenv("WINDIR")
	if win == "" {
		win = `C:\Windows`
	}
	return faceFor(filepath.Join(win, "Fonts", "segoeui.ttf"), px)
}

// TintImage recolours img towards c (strength 0-100), keeping its light and
// shade so the icon stays recognisable. Alpha is untouched.
func TintImage(img image.Image, c color.NRGBA, strength int) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	if strength <= 0 {
		return out
	}
	k := float64(min(strength, 100)) / 100
	tr, tg, tb := float64(c.R), float64(c.G), float64(c.B)
	for i := 0; i+3 < len(out.Pix); i += 4 {
		a := float64(out.Pix[i+3])
		if a == 0 {
			continue
		}
		r, g, bl := float64(out.Pix[i]), float64(out.Pix[i+1]), float64(out.Pix[i+2])
		// premultiplied: luminance in 0..a
		l := (0.299*r + 0.587*g + 0.114*bl) / a
		// map dark->deep tint, light->pale tint
		lift := func(t float64) float64 {
			if l < 0.5 {
				return t * l * 2
			}
			return t + (255-t)*(l-0.5)*2
		}
		nr, ng, nb := lift(tr)*a/255, lift(tg)*a/255, lift(tb)*a/255
		out.Pix[i] = uint8(r + (nr-r)*k)
		out.Pix[i+1] = uint8(g + (ng-g)*k)
		out.Pix[i+2] = uint8(bl + (nb-bl)*k)
	}
	return out
}

// FadeImage scales a premultiplied image's strength (0..1).
func FadeImage(img *image.RGBA, k float64) *image.RGBA {
	out := image.NewRGBA(img.Bounds())
	f := uint32(math.Round(math.Max(0, math.Min(1, k)) * 256))
	for i, v := range img.Pix {
		out.Pix[i] = uint8(uint32(v) * f >> 8)
	}
	return out
}
