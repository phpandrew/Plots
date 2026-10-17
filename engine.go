package main

import (
	"math"
	"sort"
	"strings"
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

// Sub expresses r relative to base's top-left.
func (r Rect) Sub(base Rect) Rect {
	if r == (Rect{}) {
		return r
	}
	return Rect{r.L - base.L, r.T - base.T, r.R - base.L, r.B - base.T}
}

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
	ID        string
	Rect      Rect // effective rect (grown to fit icons / rolled up), screen coords
	Title     Rect
	Chevron   Rect   // roll-up button in the title bar
	TabBar    Rect   // empty when the plot has a single tab
	Tabs      []Rect // one per tab
	Grid      Point  // top-left of first icon cell
	Cols      int
	Rows      int // visible rows
	Scale     float64
	Count     int      // icons present in the active tab
	Paths     []string // icons shown, in cell order
	Parked    []string // icons hidden (other tabs, scrolled away, rolled up)
	Scroll    int      // first visible row
	MaxScroll int
	Collapsed bool
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
	// IsDir reports whether a desktop item is a folder.
	IsDir(path string) bool
	// FolderCount returns how many entries a live-folder plot shows.
	FolderCount(p *Plot) int
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

	views  []*PlotView
	last   map[string]Point
	placed map[string]*placement
	parked map[string]bool
	// ParkAt is an off-screen spot where hidden icons wait (screen coords).
	ParkAt Point
	// Peek temporarily shows a rolled-up plot (mouse hovering its title).
	Peek string
	// FolderDrop is called when a desktop item is sent to a live-folder plot
	// (dropped on it, or routed there by a rule / the new-items setting).
	FolderDrop func(path string, p *Plot)
	started    bool
	Clicks     []ClickHint // recent user clicks on the desktop (drop / new-item hints)
	Logf       func(format string, args ...any)
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
		last: map[string]Point{}, placed: map[string]*placement{}, parked: map[string]bool{}, ParkAt: Point{-32000, -32000}}
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

// TabBarH is the height of the tab strip.
func TabBarH(scale float64) int { return int(math.Round(26 * scale)) }

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
		p.Norm()
		r, scale := e.Env.PlotRect(p)
		th, pad := e.Cfg.TitleH(scale), e.Cfg.Pad(scale)
		tabH := 0
		if len(p.Tabs) > 1 && p.Folder == "" {
			tabH = TabBarH(scale)
		}
		cols := (r.W() - 2*pad) / sx
		if cols < 1 {
			cols = 1
		}
		v := &PlotView{ID: p.ID, Scale: scale, Cols: cols}
		v.Collapsed = p.Collapsed && e.Peek != p.ID
		var shown []string
		for ti, t := range p.Tabs {
			for _, path := range t.Items {
				if _, ok := present[path]; !ok {
					continue
				}
				if ti == p.ActiveTab && !v.Collapsed {
					shown = append(shown, path)
				} else {
					v.Parked = append(v.Parked, path)
				}
			}
		}
		v.Count = len(shown)
		if p.Folder != "" {
			v.Count = e.Env.FolderCount(p)
		}
		rows := (v.Count + cols - 1) / cols
		v.Title = Rect{r.L, r.T, r.R, r.T + th}
		v.Chevron = Rect{r.R - th, r.T, r.R, r.T + th}
		if v.Collapsed {
			r.B = r.T + th
		} else {
			need := th + tabH + pad + rows*sy + pad
			if e.Cfg.GrowToFit {
				if r.H() < need {
					r.B = r.T + need
				}
				v.Rows = rows
			} else {
				capRows := max(1, (r.H()-th-tabH-2*pad)/sy)
				v.Rows = capRows
				if rows > capRows {
					v.MaxScroll = rows - capRows
				}
				if p.Scroll > v.MaxScroll {
					p.Scroll = v.MaxScroll
				}
				v.Scroll = p.Scroll
				if p.Folder == "" {
					first, last := min(len(shown), v.Scroll*cols), min(len(shown), (v.Scroll+capRows)*cols)
					v.Parked = append(v.Parked, shown[:first]...)
					v.Parked = append(v.Parked, shown[last:]...)
					shown = shown[first:last]
				}
			}
			if tabH > 0 {
				v.TabBar = Rect{r.L, r.T + th, r.R, r.T + th + tabH}
				// tabs share the full width equally (2 = 50/50, 3 = thirds, ...)
				n := len(p.Tabs)
				for i := range p.Tabs {
					x0 := r.L + r.W()*i/n
					x1 := r.L + r.W()*(i+1)/n
					v.Tabs = append(v.Tabs, Rect{x0, v.TabBar.T, x1, v.TabBar.B})
				}
			}
		}
		v.Paths = shown
		v.Rect = r
		// equal side margins so labels never hang over the plot edge
		v.Grid = Point{r.L + max(pad, (r.W()-cols*sx)/2), r.T + th + tabH + pad}
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
	return (row+v.Scroll)*v.Cols + col
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
	p, _ := e.ownerTab(path)
	return p
}

func (e *Engine) ownerTab(path string) (*Plot, *Tab) {
	for _, p := range e.Cfg.Plots {
		p.Norm()
		for _, t := range p.Tabs {
			for _, it := range t.Items {
				if it == path {
					return p, t
				}
			}
		}
	}
	return nil, nil
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
		p.Norm()
		for _, t := range p.Tabs {
			t.Items = removeStr(t.Items, path)
		}
	}
	delete(e.placed, path)
	e.Dirty = true
}

// insertAt places path into tab t before the present item at index idx.
func (e *Engine) insertAt(t *Tab, path string, idx int, present map[string]Point) {
	e.unassign(path)
	presentIdx := 0
	for i, it := range t.Items {
		if _, ok := present[it]; !ok {
			continue
		}
		if presentIdx == idx {
			t.Items = append(t.Items[:i], append([]string{path}, t.Items[i:]...)...)
			return
		}
		presentIdx++
	}
	t.Items = append(t.Items, path)
}

// sendTo files path into plot p (tab t). Live-folder plots receive the file
// itself: it is moved into the folder.
func (e *Engine) sendTo(p *Plot, t *Tab, path string) {
	if p.Folder != "" {
		e.unassign(path)
		if e.FolderDrop != nil {
			e.FolderDrop(path, p)
		}
		return
	}
	e.appendTo(t, path)
}

// appendTo adds path to the end of a tab (removing it from anywhere else).
func (e *Engine) appendTo(t *Tab, path string) {
	e.unassign(path)
	t.Items = append(t.Items, path)
	e.Dirty = true
}

// tabAt returns the tab whose label is under pt (dropping onto a tab label).
func (e *Engine) tabAt(p *Plot, pt Point) *Tab {
	v := e.View(p.ID)
	if v == nil {
		return nil
	}
	for i, r := range v.Tabs {
		if r.Contains(pt) && i < len(p.Tabs) {
			return p.Tabs[i]
		}
	}
	return nil
}

func (e *Engine) dropInto(p *Plot, path string, pos Point, present map[string]Point) {
	e.logf("drop %q into %q at %v", path, p.Title, pos)
	if p.Folder != "" {
		e.sendTo(p, nil, path)
		return
	}
	if t := e.tabAt(p, e.center(pos)); t != nil {
		e.appendTo(t, path) // dropped on a tab label: file it in that tab
		return
	}
	idx := 1 << 30
	if v := e.View(p.ID); v != nil {
		idx = e.dropIndex(v, e.center(pos))
	}
	e.insertAt(p.Cur(), path, idx, present)
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
	if p, t := e.ruleFor(path); p != nil {
		e.sendTo(p, t, path)
		e.logf("new item %q -> %q by rule", path, p.Title)
		return
	}
	if inbox := e.Cfg.Plot(e.Cfg.InboxPlot); inbox != nil {
		e.sendTo(inbox, inbox.Tabs[0], path)
		e.logf("new item %q -> inbox %q", path, inbox.Title)
		return
	}
	if dest := e.plotAt(e.center(pos)); dest != nil {
		e.dropInto(dest, path, pos, present)
	}
}

// ruleFor returns the destination of the first sorting rule matching path.
func (e *Engine) ruleFor(path string) (*Plot, *Tab) {
	for _, r := range e.Cfg.Rules {
		p := e.Cfg.Plot(r.PlotID)
		if p == nil || strings.TrimSpace(r.Pattern) == "" {
			continue
		}
		if r.Match(path, e.Env.IsDir(path)) {
			return p, p.TabByName(r.Tab)
		}
	}
	return nil, nil
}

// ApplyRules re-sorts the new-items plot (and unsorted desktop icons) with the
// sorting rules. Returns how many icons moved.
func (e *Engine) ApplyRules() int {
	n := 0
	var candidates []string
	if inbox := e.Cfg.Plot(e.Cfg.InboxPlot); inbox != nil {
		candidates = append(candidates, inbox.AllItems()...)
	}
	for path := range e.last {
		if e.owner(path) == nil {
			candidates = append(candidates, path)
		}
	}
	for _, path := range candidates {
		p, t := e.ruleFor(path)
		if p == nil {
			continue
		}
		if _, cur := e.ownerTab(path); cur == t {
			continue
		}
		e.sendTo(p, t, path)
		n++
	}
	return n
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
					if p, t := e.ruleFor(it.Path); p != nil {
						e.sendTo(p, t, it.Path)
					} else if inbox := e.Cfg.Plot(e.Cfg.InboxPlot); inbox != nil {
						e.sendTo(inbox, inbox.Tabs[0], it.Path)
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
				if _, t := e.ownerTab(r); t != nil {
					for j, it := range t.Items {
						if it == r {
							t.Items[j] = a
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
				if e.parked[path] {
					// a hidden icon that Explorer moved back on screen: hide it again
					if dist(pos, pl.target) > moveTol {
						delete(e.placed, path)
					}
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
	seen := map[string]bool{}
	for _, p := range e.Cfg.Plots {
		p.Norm()
		for _, t := range p.Tabs {
			out := t.Items[:0]
			for _, it := range t.Items {
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
			t.Items = out
		}
	}

	// layout
	e.computeViews(cur)
	var moves []Move
	assigned := map[string]bool{}
	e.parked = map[string]bool{}
	for _, v := range e.views {
		targets := make([]Point, 0, len(v.Paths)+len(v.Parked))
		paths := make([]string, 0, len(v.Paths)+len(v.Parked))
		for i, path := range v.Paths {
			targets = append(targets, e.cellPos(v, i))
			paths = append(paths, path)
		}
		for _, path := range v.Parked {
			e.parked[path] = true
			targets = append(targets, e.ParkAt)
			paths = append(paths, path)
		}
		for i, path := range paths {
			assigned[path] = true
			target := targets[i]
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
		if p := e.plotAt(e.center(it.Pos)); p != nil && p.Folder == "" {
			p.Cur().Items = append(p.Cur().Items, it.Path)
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
	for path, pos := range e.last {
		if e.parked[path] {
			continue
		}
		r := RectWH(pos.X-e.Cfg.IconOffsetX-e.Nudge(), pos.Y-e.Cfg.IconOffsetY, e.Spacing.X, e.Spacing.Y)
		if r.Contains(pt) {
			return true
		}
	}
	return false
}

// IsParked reports whether an icon is currently hidden off-screen.
func (e *Engine) IsParked(path string) bool { return e.parked[path] }

// UnparkAll lays every assigned icon out on screen (all tabs, expanded),
// so nothing stays hidden off-screen after Plots exits.
func (e *Engine) UnparkAll() []Move {
	saveGrow, savePeek := e.Cfg.GrowToFit, e.Peek
	e.Cfg.GrowToFit = true
	defer func() { e.Cfg.GrowToFit, e.Peek = saveGrow, savePeek }()
	var moves []Move
	for _, p := range e.Cfg.Plots {
		p.Norm()
		v := e.View(p.ID)
		if v == nil {
			continue
		}
		i := 0
		for _, path := range p.AllItems() {
			if _, ok := e.last[path]; !ok {
				continue
			}
			moves = append(moves, Move{path, e.cellPos(&PlotView{Grid: Point{v.Grid.X, v.Title.B + e.Cfg.Pad(v.Scale)}, Cols: v.Cols}, i)})
			i++
		}
	}
	return moves
}
