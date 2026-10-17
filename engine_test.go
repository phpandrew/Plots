package main

import (
	"strings"
	"testing"
	"time"
)

type fakeEnv struct{ gone map[string]bool }

func (f fakeEnv) PlotRect(p *Plot) (Rect, float64) { return RectWH(p.X, p.Y, p.W, p.H), 1 }
func (f fakeEnv) FileGone(path string) bool        { return f.gone[path] }
func (f fakeEnv) FolderCount(p *Plot) int          { return 0 }
func (f fakeEnv) IsDir(path string) bool           { return strings.HasSuffix(path, "/") }

func newTestEngine() (*Engine, *Config) {
	cfg := DefaultConfig()
	cfg.Plots = []*Plot{
		{ID: "a", Title: "A", X: 1000, Y: 0, W: 320, H: 250},
		{ID: "b", Title: "B", X: 1000, Y: 400, W: 320, H: 250},
	}
	e := NewEngine(cfg, fakeEnv{gone: map[string]bool{}})
	e.Spacing = Point{75, 100}
	return e, cfg
}

func apply(items []Item, moves []Move) []Item {
	for _, m := range moves {
		for i := range items {
			if items[i].Path == m.Path {
				items[i].Pos = m.To
			}
		}
	}
	return items
}

func find(items []Item, p string) *Item {
	for i := range items {
		if items[i].Path == p {
			return &items[i]
		}
	}
	return nil
}

func TestDragInOutReorder(t *testing.T) {
	e, cfg := newTestEngine()
	items := []Item{{"x", Point{0, 0}}, {"y", Point{0, 100}}, {"z", Point{0, 200}}}
	items = apply(items, e.Reconcile(items)) // first tick: nothing moves
	if len(cfg.Plots[0].AllItems()) != 0 {
		t.Fatal("nothing should be adopted on first tick")
	}
	// user drags x into plot A
	find(items, "x").Pos = Point{1100, 100}
	m := e.Reconcile(items)
	items = apply(items, m)
	if len(cfg.Plots[0].AllItems()) != 1 || cfg.Plots[0].AllItems()[0] != "x" {
		t.Fatalf("x not in A: %v", cfg.Plots[0].AllItems())
	}
	if len(m) != 1 {
		t.Fatalf("expected a snap move, got %v", m)
	}
	// settle tick
	if m := e.Reconcile(items); len(m) != 0 {
		t.Fatalf("unexpected moves %v", m)
	}
	// drag y into A before x (first cell)
	v := e.View("a")
	find(items, "y").Pos = Point{v.Grid.X, v.Grid.Y}
	items = apply(items, e.Reconcile(items))
	if got := cfg.Plots[0].AllItems(); len(got) != 2 || got[0] != "y" || got[1] != "x" {
		t.Fatalf("order wrong: %v", got)
	}
	items = apply(items, e.Reconcile(items))
	// drag x to plot B
	find(items, "x").Pos = Point{1050, 470}
	items = apply(items, e.Reconcile(items))
	if len(cfg.Plots[1].AllItems()) != 1 || len(cfg.Plots[0].AllItems()) != 1 {
		t.Fatalf("move to B failed: %v %v", cfg.Plots[0].AllItems(), cfg.Plots[1].AllItems())
	}
	items = apply(items, e.Reconcile(items))
	// drag y out to the open desktop
	find(items, "y").Pos = Point{300, 300}
	items = apply(items, e.Reconcile(items))
	if len(cfg.Plots[0].AllItems()) != 0 {
		t.Fatalf("y should be unassigned: %v", cfg.Plots[0].AllItems())
	}
	if find(items, "y").Pos != (Point{300, 300}) {
		t.Fatal("y should stay where dropped")
	}
}

func TestInboxAndRename(t *testing.T) {
	e, cfg := newTestEngine()
	cfg.InboxPlot = "b"
	items := []Item{{"x", Point{0, 0}}}
	items = apply(items, e.Reconcile(items))
	// a new download appears somewhere (auto-placed by Explorer)
	items = append(items, Item{"dl.zip", Point{0, 100}})
	items = apply(items, e.Reconcile(items))
	if len(cfg.Plots[1].AllItems()) != 1 {
		t.Fatalf("download not in inbox: %v", cfg.Plots[1].AllItems())
	}
	items = apply(items, e.Reconcile(items))
	// rename keeps assignment
	p := find(items, "dl.zip")
	p.Path = "renamed.zip"
	items = apply(items, e.Reconcile(items))
	if cfg.Plots[1].AllItems()[0] != "renamed.zip" {
		t.Fatalf("rename lost: %v", cfg.Plots[1].AllItems())
	}
	// a temporarily missing item is kept unless the file is gone
	items = items[:1]
	items = apply(items, e.Reconcile(items))
	if len(cfg.Plots[1].AllItems()) != 1 {
		t.Fatal("transiently missing item dropped")
	}
	e.Env.(fakeEnv).gone["renamed.zip"] = true
	e.Reconcile(items)
	if len(cfg.Plots[1].AllItems()) != 0 {
		t.Fatal("deleted item kept")
	}
}

func TestUserDropBeatsInbox(t *testing.T) {
	e, cfg := newTestEngine()
	cfg.InboxPlot = "b"
	items := []Item{{"x", Point{0, 0}}}
	items = apply(items, e.Reconcile(items))
	e.Clicks = []ClickHint{{Point{1150, 120}, time.Now(), 5 * time.Second}}
	items = append(items, Item{"dragged.txt", Point{1150 - 37, 120 - 50}})
	e.Reconcile(items)
	if len(cfg.Plots[0].AllItems()) != 1 {
		t.Fatalf("user drop should land in A: A=%v B=%v", cfg.Plots[0].AllItems(), cfg.Plots[1].AllItems())
	}
}

func TestGrowAndAdopt(t *testing.T) {
	e, cfg := newTestEngine()
	cfg.GrowToFit = true
	var items []Item
	for i := 0; i < 12; i++ {
		items = append(items, Item{string(rune('a' + i)), Point{1000 + (i%4)*75, 40 + (i/4)*70}})
	}
	items = apply(items, e.Reconcile(items))
	e.AdoptUnder(items)
	items = apply(items, e.Reconcile(items))
	if len(cfg.Plots[0].AllItems()) != 12 {
		t.Fatalf("adopted %d", len(cfg.Plots[0].AllItems()))
	}
	v := e.View("a")
	if v.Rect.H() <= 250 {
		t.Fatalf("plot should grow, h=%d", v.Rect.H())
	}
	if m := e.Reconcile(items); len(m) != 0 {
		t.Fatalf("not stable: %v", m)
	}
}

func TestReappearKeepsPlot(t *testing.T) {
	e, cfg := newTestEngine()
	cfg.InboxPlot = "b"
	items := []Item{{"x", Point{0, 0}}, {"y", Point{0, 100}}, {"z", Point{0, 200}}}
	items = apply(items, e.Reconcile(items))
	find(items, "x").Pos = Point{1100, 100}
	items = apply(items, e.Reconcile(items))
	items = apply(items, e.Reconcile(items))
	// Explorer view flakes: x and y vanish, then come back somewhere else
	short := []Item{items[2]}
	e.Reconcile(append(short, Item{"w", Point{0, 300}}))
	back := []Item{{"x", Point{0, 0}}, {"y", Point{0, 100}}, items[2], {"w", Point{0, 300}}}
	e.Reconcile(back)
	if len(cfg.Plots[0].AllItems()) != 1 || cfg.Plots[0].AllItems()[0] != "x" {
		t.Fatalf("x should stay in A: %v / inbox %v", cfg.Plots[0].AllItems(), cfg.Plots[1].AllItems())
	}
	for _, it := range cfg.Plots[1].AllItems() {
		if it == "y" {
			t.Fatal("known item y must not go to inbox")
		}
	}
}

func parkedCount(e *Engine, items []Item) int {
	n := 0
	for _, it := range items {
		if it.Pos == e.ParkAt {
			n++
		}
	}
	return n
}

func TestScrollParksOverflow(t *testing.T) {
	e, cfg := newTestEngine()
	var items []Item
	for i := 0; i < 12; i++ {
		items = append(items, Item{string(rune('a' + i)), Point{1000 + (i%4)*75, 40 + (i/4)*70}})
	}
	items = apply(items, e.Reconcile(items))
	e.AdoptUnder(items)
	items = apply(items, e.Reconcile(items))
	// 320 wide -> 4 cols; 250 high -> 2 rows visible; 12 icons = 3 rows
	v := e.View("a")
	if v.Rect.H() != 250 || v.MaxScroll != 1 {
		t.Fatalf("expected fixed height with 1 row of scroll, h=%d max=%d", v.Rect.H(), v.MaxScroll)
	}
	if n := parkedCount(e, items); n != 4 {
		t.Fatalf("4 icons should be parked, got %d", n)
	}
	cfg.Plots[0].Scroll = 1
	items = apply(items, e.Reconcile(items))
	if n := parkedCount(e, items); n != 4 {
		t.Fatalf("after scrolling, 4 icons should be parked, got %d", n)
	}
	if find(items, "a").Pos != e.ParkAt {
		t.Fatal("first row should be scrolled away")
	}
}

func TestTabsAndRollup(t *testing.T) {
	e, cfg := newTestEngine()
	p := cfg.Plots[0]
	p.Norm()
	p.Tabs = []*Tab{{Name: "One", Items: []string{"x"}}, {Name: "Two", Items: []string{"y"}}}
	items := []Item{{"x", Point{0, 0}}, {"y", Point{0, 100}}}
	items = apply(items, e.Reconcile(items))
	if find(items, "y").Pos != e.ParkAt || find(items, "x").Pos == e.ParkAt {
		t.Fatalf("tab Two should be hidden: %v", items)
	}
	p.ActiveTab = 1
	items = apply(items, e.Reconcile(items))
	if find(items, "x").Pos != e.ParkAt || find(items, "y").Pos == e.ParkAt {
		t.Fatalf("tab One should be hidden now: %v", items)
	}
	p.Collapsed = true
	items = apply(items, e.Reconcile(items))
	if parkedCount(e, items) != 2 || e.View("a").Rect.H() != cfg.TitleH(1) {
		t.Fatal("rolled-up plot should hide everything and shrink to its title")
	}
	e.Peek = "a"
	items = apply(items, e.Reconcile(items))
	if parkedCount(e, items) != 1 {
		t.Fatal("peeking should show the active tab")
	}
	// parked icons stay assigned through a few ticks
	for i := 0; i < 3; i++ {
		items = apply(items, e.Reconcile(items))
	}
	if len(p.AllItems()) != 2 {
		t.Fatalf("items lost: %v", p.AllItems())
	}
}

func TestRules(t *testing.T) {
	e, cfg := newTestEngine()
	cfg.InboxPlot = "b"
	cfg.Plots[0].Norm()
	cfg.Plots[0].SetTabNames([]string{"Docs", "Pics"})
	cfg.Rules = []*Rule{{Pattern: "*.pdf;.docx", PlotID: "a", Tab: "Docs"}, {Pattern: "*.png", PlotID: "a", Tab: "pics"}}
	items := []Item{{"x", Point{0, 0}}}
	items = apply(items, e.Reconcile(items))
	items = append(items, Item{"C:/d/Report.PDF", Point{0, 100}}, Item{"C:/d/cat.png", Point{0, 200}}, Item{"C:/d/a.zip", Point{0, 300}}, Item{"C:/d/b.docx", Point{0, 400}})
	e.Reconcile(items)
	a := cfg.Plots[0]
	if len(a.Tabs[0].Items) != 2 || len(a.Tabs[1].Items) != 1 {
		t.Fatalf("rules misrouted: %v / %v", a.Tabs[0].Items, a.Tabs[1].Items)
	}
	if got := cfg.Plots[1].AllItems(); len(got) != 1 || got[0] != "C:/d/a.zip" {
		t.Fatalf("unmatched should go to inbox: %v", got)
	}
}

func TestSetTabNamesKeepsItems(t *testing.T) {
	p := &Plot{Items: []string{"1", "2"}}
	p.SetTabNames([]string{"A", "B", "C"})
	p.Tabs[2].Items = []string{"3"}
	p.SetTabNames([]string{"A"})
	if len(p.Tabs) != 1 || len(p.AllItems()) != 3 {
		t.Fatalf("merge failed: %+v", p.AllItems())
	}
}

func TestFolderPlotReceivesFiles(t *testing.T) {
	e, cfg := newTestEngine()
	cfg.Plots[1].Folder = "C:/Docs"
	cfg.InboxPlot = "b"
	var moved []string
	e.FolderDrop = func(path string, p *Plot) { moved = append(moved, path) }
	items := []Item{{"x", Point{0, 0}}}
	items = apply(items, e.Reconcile(items))
	items = append(items, Item{"new.pdf", Point{0, 100}})
	e.Reconcile(items)
	if len(moved) != 1 || len(cfg.Plots[1].AllItems()) != 0 {
		t.Fatalf("new item should be moved into the folder, got %v / %v", moved, cfg.Plots[1].AllItems())
	}
}
