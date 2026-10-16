package main

import (
	"math"
	"sort"
	"time"
)

// The engine is platform independent: it receives desktop icon positions in
// screen coordinates and returns the moves needed to keep every plot tidy.

type Point struct{ X, Y int }
type Rect struct{ L, T, R, B int }

func (r Rect) W() int                { return r.R - r.L }
func (r Rect) H() int                { return r.B - r.T }
func (r Rect) Contains(p Point) bool { return p.X >= r.L && p.X < r.R && p.Y >= r.T && p.Y < r.B }
func (r Rect) Center() Point         { return Point{(r.L + r.R) / 2, (r.T + r.B) / 2} }
func (p Point) Add(q Point) Point    { return Point{p.X + q.X, p.Y + q.Y} }
func (p Point) Sub(q Point) Point    { return Point{p.X - q.X, p.Y - q.Y} }
func dist(a, b Point) float64        { return math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y)) }
func RectWH(x, y, w, h int) Rect     { return Rect{x, y, x + w, y + h} }

type Item struct {
	Path string
	Pos  Point // screen coords of the item's top-left
}

type Move struct {
	Path string
	To   Point
}

// PlotView is the computed on-screen geometry of a plot for the current tick.
type PlotView struct {
	ID    string
	Rect  Rect // effective rect (grown to fit icons), screen coords
	Title Rect
	Grid  Point // top-left of first icon cell
	Cols  int
	Scale float64
	Count int // icons present
	Paths []string
}

type ClickHint struct {
	Pt  Point
	At  time.Time
	TTL time.Duration
}

type Env interface {
	// PlotRect returns the configured screen rect and DPI scale for a plot.
	PlotRect(p *Plot) (Rect, float64)
	// FileGone reports whether a desktop item path no longer exists at all.
	FileGone(path string) bool
}

type placement struct {
	target, actual Point
	pending        bool
}

type Engine struct {
	Cfg     *Config
	Env     Env
	Spacing Point // icon cell size
	IconW   int   // icon image size in px
	Now     func() time.Time
	Dirty   bool

	views   []*PlotView
	last    map[string]Point
	placed  map[string]*placement
	started bool
	Clicks  []ClickHint // recent user clicks on the desktop (drop / new-item hints)
	Logf    func(format string, args ...any)
	// Renames holds old->new paths reported by the file-system watcher.
	Renames map[string]string
	known   map[string]bool
}

func (e *Engine) logf(f string, a ...any) {
	if e.Logf != nil {
		e.Logf(f, a...)
	}
}

func NewEngine(cfg *Config, env Env) *Engine {
	return &Engine{Cfg: cfg, Env: env, Spacing: Point{75, 100}, Now: time.Now,
		last: map[string]Point{}, placed: map[string]*placement{}}
}

func (e *Engine) Views() []*PlotView { return e.views }

// TitleH is the title bar height in physical pixels; it grows with the font size.
func (c *Config) TitleH(scale float64) int {
	pt := c.TitleSize
	if pt <= 0 {
		pt = 11
	}
	return int(math.Round(math.Max(30, float64(pt)*96/72*2.05) * scale))
}

// Pad is the space between a plot's edge and its icons, in physical pixels.
func (c *Config) Pad(scale float64) int {
	p := c.Padding
	if p <= 0 {
		p = 10
	}
	return int(math.Round(float64(p) * scale))
}

func (e *Engine) computeViews(present map[string]Point) {
	sx, sy := e.Spacing.X, e.Spacing.Y
	if sx < 16 {
		sx = 75
	}
	if sy < 16 {
		sy = 100
	}
	views := make([]*PlotView, 0, len(e.Cfg.Plots))
	for _, p := range e.Cfg.Plots {
		r, scale := e.Env.PlotRect(p)
		th, pad := e.Cfg.TitleH(scale), e.Cfg.Pad(scale)
		cols := (r.W() - 2*pad) / sx
		if cols < 1 {
			cols = 1
		}
		v := &PlotView{ID: p.ID, Scale: scale, Cols: cols}
		for _, path := range p.Items {
			if _, ok := present[path]; ok {
				v.Paths = append(v.Paths, path)
			}
		}
		v.Count = len(v.Paths)
		rows := (v.Count + cols - 1) / cols
		need := th + pad + rows*sy + pad
		if r.H() < need {
			r.B = r.T + need
		}
		v.Rect = r
		v.Title = Rect{r.L, r.T, r.R, r.T + th}
		// equal side margins so labels never hang over the plot edge
		v.Grid = Point{r.L + max(pad, (r.W()-cols*sx)/2), r.T + th + pad}
		views = append(views, v)
	}
	e.views = views
}

func (e *Engine) View(id string) *PlotView {
	for _, v := range e.views {
		if v.ID == id {
			return v
		}
	}
	return nil
}

func (e *Engine) plotAt(pt Point) *Plot {
	for i := len(e.views) - 1; i >= 0; i-- {
		if e.views[i].Rect.Contains(pt) {
			return e.Cfg.Plot(e.views[i].ID)
		}
	}
	return nil
}

// TitleAt returns the plot whose title bar contains pt.
func (e *Engine) TitleAt(pt Point) *Plot {
	for i := len(e.views) - 1; i >= 0; i-- {
		if e.views[i].Title.Contains(pt) {
			return e.Cfg.Plot(e.views[i].ID)
		}
	}
	return nil
}

func (e *Engine) cellPos(v *PlotView, idx int) Point {
	col, row := idx%v.Cols, idx/v.Cols
	// an item's position is the top-left of its icon image; centre the icon
	// (and therefore its label) in the cell
	return Point{v.Grid.X + col*e.Spacing.X + e.Cfg.IconOffsetX + e.Nudge(), v.Grid.Y + row*e.Spacing.Y + e.Cfg.IconOffsetY}
}

// dropIndex returns the index among the plot's present items (excluding
// `self`) at which an item whose centre is c should be inserted.
func (e *Engine) dropIndex(v *PlotView, c Point) int {
	col := (c.X - v.Grid.X) / max(1, e.Spacing.X)
	row := (c.Y - v.Grid.Y) / max(1, e.Spacing.Y)
	if c.X < v.Grid.X {
		col = 0
	}
	if c.Y < v.Grid.Y {
		row = 0
	}
	if col >= v.Cols {
		col = v.Cols - 1
	}
	return row*v.Cols + col
}

// Nudge is the gap between a cell's left edge and its icon image.
func (e *Engine) Nudge() int {
	iw := e.IconW
	if iw <= 0 {
		iw = 48
	}
	return max(0, (e.Spacing.X-iw)/2)
}

func (e *Engine) center(pos Point) Point {
	return Point{pos.X - e.Cfg.IconOffsetX - e.Nudge() + e.Spacing.X/2, pos.Y - e.Cfg.IconOffsetY + e.Spacing.Y/2}
}

func (e *Engine) owner(path string) *Plot {
	for _, p := range e.Cfg.Plots {
		for _, it := range p.Items {
			if it == path {
				return p
			}
		}
	}
	return nil
}

func removeStr(list []string, s string) []string {
	out := list[:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

func (e *Engine) unassign(path string) {
	for _, p := range e.Cfg.Plots {
		p.Items = removeStr(p.Items, path)
	}
	delete(e.placed, path)
	e.Dirty = true
}

// insertAt places path into plot p before the present item at index idx.
func (e *Engine) insertAt(p *Plot, path string, idx int, present map[string]Point) {
	e.unassign(path)
	presentIdx := 0
	for i, it := range p.Items {
		if _, ok := present[it]; !ok {
			continue
		}
		if presentIdx == idx {
			p.Items = append(p.Items[:i], append([]string{path}, p.Items[i:]...)...)
			return
		}
		presentIdx++
	}
	p.Items = append(p.Items, path)
}

func (e *Engine) dropInto(p *Plot, path string, pos Point, present map[string]Point) {
	e.logf("drop %q into %q at %v", path, p.Title, pos)
	idx := 1 << 30
	if v := e.View(p.ID); v != nil {
		idx = e.dropIndex(v, e.center(pos))
	}
	e.insertAt(p, path, idx, present)
}

// userPlaced reports whether a new item appeared where the user recently
// clicked/dropped (drag from Explorer, right-click > New), as opposed to being
// auto-placed by Explorer (a download, a saved file).
func (e *Engine) userPlaced(pos Point) bool {
	now := e.Now()
	c := e.center(pos)
	lim := 1.5 * math.Max(float64(e.Spacing.X), float64(e.Spacing.Y))
	for _, h := range e.Clicks {
		if now.Sub(h.At) < h.TTL && dist(h.Pt, c) < lim {
			return true
		}
	}
	return false
}

func (e *Engine) handleNew(path string, pos Point, present map[string]Point) {
	if dest := e.plotAt(e.center(pos)); dest != nil && e.userPlaced(pos) {
		e.dropInto(dest, path, pos, present)
		return
	}
	if inbox := e.Cfg.Plot(e.Cfg.InboxPlot); inbox != nil {
		e.unassign(path)
		inbox.Items = append(inbox.Items, path)
		e.logf("new item %q -> inbox %q", path, inbox.Title)
		e.Dirty = true
		return
	}
	if dest := e.plotAt(e.center(pos)); dest != nil {
		e.dropInto(dest, path, pos, present)
	}
}

const moveTol = 6

// Reconcile is called every tick with the current desktop items.
func (e *Engine) Reconcile(items []Item) []Move {
	cur := make(map[string]Point, len(items))
	for _, it := range items {
		cur[it.Path] = it.Pos
	}
	// Explorer sometimes reports an empty / half-built view while refreshing.
	if len(items) == 0 || (len(e.last) > 6 && len(items) < len(e.last)/3) {
		return nil
	}
	if e.views == nil {
		e.computeViews(cur)
	}
	// process in reading order so multi-selection drags keep their order
	order := make([]Item, len(items))
	copy(order, items)
	sort.Slice(order, func(i, j int) bool {
		if order[i].Pos.Y != order[j].Pos.Y {
			return order[i].Pos.Y < order[j].Pos.Y
		}
		return order[i].Pos.X < order[j].Pos.X
	})

	if !e.started {
		e.started = true
		known := map[string]bool{}
		for _, k := range e.Cfg.Known {
			known[k] = true
		}
		if len(known) > 0 {
			for _, it := range order {
				if !known[it.Path] && e.owner(it.Path) == nil {
					// created while Plots was not running
					if inbox := e.Cfg.Plot(e.Cfg.InboxPlot); inbox != nil {
						inbox.Items = append(inbox.Items, it.Path)
						e.Dirty = true
					}
				}
			}
		}
	} else {
		var added, removed []string
		for p := range cur {
			if _, ok := e.last[p]; !ok {
				added = append(added, p)
			}
		}
		for p := range e.last {
			if _, ok := cur[p]; !ok {
				removed = append(removed, p)
			}
		}
		if len(added) > 0 || len(removed) > 0 {
			e.logf("desktop changed: +%v -%v", added, removed)
		}
		renamed := map[string]bool{}
		for _, a := range added {
			for i, r := range removed {
				if r == "" || (dist(e.last[r], cur[a]) > 4 && e.Renames[r] != a) {
					continue
				}
				if p := e.owner(r); p != nil {
					for j, it := range p.Items {
						if it == r {
							p.Items[j] = a
						}
					}
					if pl := e.placed[r]; pl != nil {
						e.placed[a] = pl
						delete(e.placed, r)
					}
					e.Dirty = true
				}
				renamed[a] = true
				delete(e.Renames, r)
				removed[i] = ""
				break
			}
		}
		addedSet := map[string]bool{}
		for _, a := range added {
			addedSet[a] = true
		}
		half := float64(max(e.Spacing.X, e.Spacing.Y)) / 2
		for _, it := range order {
			path, pos := it.Path, it.Pos
			if addedSet[path] && !renamed[path] {
				// items that merely re-appear (Explorer refresh) keep their plot
				if e.owner(path) == nil && !e.known[path] {
					e.handleNew(path, pos, cur)
				}
				continue
			}
			p := e.owner(path)
			if p != nil {
				pl := e.placed[path]
				if pl == nil {
					continue
				}
				if pl.pending {
					if dist(pos, pl.target) <= half {
						pl.actual, pl.pending = pos, false // settled (Explorer may nudge it)
						continue
					}
				} else if dist(pos, pl.actual) <= moveTol {
					continue
				}
				// the user dragged it
				if dest := e.plotAt(e.center(pos)); dest != nil {
					e.dropInto(dest, path, pos, cur)
				} else {
					e.logf("drag %q out of %q to %v", path, p.Title, pos)
					e.unassign(path)
				}
				continue
			}
			if lp, ok := e.last[path]; ok && dist(lp, pos) > moveTol {
				if dest := e.plotAt(e.center(pos)); dest != nil {
					e.dropInto(dest, path, pos, cur)
				}
			}
		}
	}

	// forget items whose files are really gone (not just temporarily missing)
	for _, p := range e.Cfg.Plots {
		out := p.Items[:0]
		seen := map[string]bool{}
		for _, it := range p.Items {
			if seen[it] {
				continue
			}
			seen[it] = true
			if _, ok := cur[it]; !ok && e.Env.FileGone(it) {
				e.Dirty = true
				continue
			}
			out = append(out, it)
		}
		p.Items = out
	}

	// layout
	e.computeViews(cur)
	var moves []Move
	assigned := map[string]bool{}
	for _, v := range e.views {
		for i, path := range v.Paths {
			assigned[path] = true
			target := e.cellPos(v, i)
			pl := e.placed[path]
			if pl != nil && pl.target == target {
				continue
			}
			if cur[path] == target {
				e.placed[path] = &placement{target: target, actual: target}
				continue
			}
			moves = append(moves, Move{path, target})
			e.placed[path] = &placement{target: target, actual: target, pending: true}
			cur[path] = target
		}
	}
	for path := range e.placed {
		if !assigned[path] {
			delete(e.placed, path)
		}
	}
	e.last = cur

	// remember every item we have seen (only forget files that are really gone)
	if e.known == nil {
		e.known = map[string]bool{}
		for _, k := range e.Cfg.Known {
			e.known[k] = true
		}
	}
	changed := false
	for p := range cur {
		if !e.known[p] {
			e.known[p] = true
			changed = true
		}
	}
	if len(e.known) != len(cur) {
		for p := range e.known {
			if _, ok := cur[p]; !ok && e.Env.FileGone(p) {
				delete(e.known, p)
				changed = true
			}
		}
	}
	if changed {
		known := make([]string, 0, len(e.known))
		for p := range e.known {
			known = append(known, p)
		}
		sort.Strings(known)
		e.Cfg.Known = known
		e.Dirty = true
	}
	if len(e.Renames) > 200 {
		e.Renames = nil
	}
	return moves
}

// AdoptUnder assigns unassigned items lying inside a plot to that plot.
// Used when leaving edit mode so plots drawn over existing icons take them.
func (e *Engine) AdoptUnder(items []Item) {
	cur := map[string]Point{}
	for _, it := range items {
		cur[it.Path] = it.Pos
	}
	e.computeViews(cur)
	sorted := make([]Item, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Pos.Y != sorted[j].Pos.Y {
			return sorted[i].Pos.Y < sorted[j].Pos.Y
		}
		return sorted[i].Pos.X < sorted[j].Pos.X
	})
	for _, it := range sorted {
		if e.owner(it.Path) != nil {
			continue
		}
		if p := e.plotAt(e.center(it.Pos)); p != nil {
			p.Items = append(p.Items, it.Path)
			e.Dirty = true
		}
	}
}

// Relayout forces every assigned icon to be re-placed on the next tick.
func (e *Engine) Relayout() {
	e.placed = map[string]*placement{}
	e.views = nil
}

// IconAt reports whether pt lies on any desktop icon from the last tick.
func (e *Engine) IconAt(pt Point) bool {
	for _, pos := range e.last {
		r := RectWH(pos.X-e.Cfg.IconOffsetX-e.Nudge(), pos.Y-e.Cfg.IconOffsetY, e.Spacing.X, e.Spacing.Y)
		if r.Contains(pt) {
			return true
		}
	}
	return false
}
