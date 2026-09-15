package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/gallery"

	"fyne.io/fyne/v2/widget"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
)

// findSplit walks a composed object tree looking for the queue-pane split, so
// the tests can assert on the shell's composition without depending on the
// exact nesting of Borders and Stacks around it.
func findSplit(o fyne.CanvasObject) *container.Split {
	switch v := o.(type) {
	case *container.Split:
		return v
	case *fyne.Container:
		for _, child := range v.Objects {
			if s := findSplit(child); s != nil {
				return s
			}
		}
	}
	return nil
}

func contains(o, want fyne.CanvasObject) bool {
	if o == want {
		return true
	}
	switch v := o.(type) {
	case *fyne.Container:
		for _, child := range v.Objects {
			if contains(child, want) {
				return true
			}
		}
	case *container.Split:
		return contains(v.Leading, want) || contains(v.Trailing, want)
	}
	return false
}

func TestShellWrapRegularLayoutPinsQueuePane(t *testing.T) {
	test.NewApp()
	win := test.NewWindow(nil)

	bar := widget.NewLabel("transport")
	panel := widget.NewLabel("queue")
	content := widget.NewLabel("wall")
	shell := &shellWindow{Window: win, bar: bar, queuePanel: panel}

	got := shell.wrap(content)
	split := findSplit(got)
	if split == nil {
		t.Fatal("regular layout composed no queue-pane split")
	}
	if split.Trailing != panel {
		t.Error("split trailing child is not the queue pane")
	}
	if !contains(got, content) || !contains(got, bar) {
		t.Error("composition dropped the content or the transport bar")
	}

	// A second wrap must reuse the split so the user's divider position
	// survives the gallery's own SetContent calls.
	other := widget.NewLabel("album")
	if again := findSplit(shell.wrap(other)); again != split {
		t.Error("second wrap built a new split, losing the divider position")
	}
	if split.Leading != other {
		t.Error("reused split did not take the new content as its leading child")
	}
}

func TestShellWrapCompactLayoutHasNoQueuePane(t *testing.T) {
	test.NewApp()
	win := test.NewWindow(nil)

	bar := widget.NewLabel("mini")
	content := widget.NewLabel("wall")
	shell := &shellWindow{Window: win, bar: bar, compact: true, queuePanel: widget.NewLabel("queue")}

	got := shell.wrap(content)
	if findSplit(got) != nil {
		t.Error("compact layout composed a queue-pane split")
	}
	if !contains(got, content) || !contains(got, bar) {
		t.Error("composition dropped the content or the mini bar")
	}
}

// Going compact and back must not reset the divider the user dragged.
func TestShellRemembersSplitOffset(t *testing.T) {
	test.NewApp()
	win := test.NewWindow(nil)

	shell := &shellWindow{Window: win, bar: widget.NewLabel("bar"), queuePanel: widget.NewLabel("queue")}
	split := findSplit(shell.wrap(widget.NewLabel("wall")))
	if split == nil {
		t.Fatal("no split built")
	}
	split.SetOffset(0.4)

	shell.dropSplit()
	shell.compact = true
	shell.wrap(widget.NewLabel("wall"))

	shell.compact = false
	restored := findSplit(shell.wrap(widget.NewLabel("wall")))
	if restored == nil {
		t.Fatal("split not rebuilt when leaving the compact layout")
	}
	if restored.Offset != 0.4 {
		t.Errorf("restored split offset = %v, want the remembered 0.4", restored.Offset)
	}
}

// wrap always includes the width watcher, since that is how the shell learns
// the window crossed the compact threshold. Forgetting it would freeze the
// layout mode chosen at startup.
func TestShellWrapIncludesWidthWatcher(t *testing.T) {
	test.NewApp()
	win := test.NewWindow(nil)

	watcher := newWidthWatcher(func(float32) {})
	shell := &shellWindow{Window: win, bar: widget.NewLabel("bar"), watcher: watcher}
	if !contains(shell.wrap(widget.NewLabel("wall")), watcher) {
		t.Error("composition dropped the width watcher")
	}
}

func TestWidthWatcherReportsChangesOnly(t *testing.T) {
	var widths []float32
	w := newWidthWatcher(func(width float32) { widths = append(widths, width) })

	w.Resize(fyne.NewSize(400, 100))
	w.Resize(fyne.NewSize(400, 500)) // height-only change: not a layout-mode event
	w.Resize(fyne.NewSize(900, 500))

	want := []float32{400, 900}
	if len(widths) != len(want) {
		t.Fatalf("reported widths %v, want %v", widths, want)
	}
	for i := range want {
		if widths[i] != want[i] {
			t.Fatalf("reported widths %v, want %v", widths, want)
		}
	}
}

// The layout override has to beat the width heuristic outright: it exists
// because width cannot classify a tablet (same device, ~800dp portrait vs
// ~1280dp landscape), so a pinned mode must hold in both orientations.
func TestCompactForWidthPreferenceOverridesWidth(t *testing.T) {
	phone := gallery.NewPlatformFor(true)

	cases := []struct {
		name  string
		pref  string
		width float32
		want  bool
	}{
		{"auto phone", config.LayoutAuto, 400, true},
		{"auto tablet portrait", config.LayoutAuto, 840, true},
		{"auto tablet landscape", config.LayoutAuto, 1280, false},
		{"auto empty preference behaves as auto", "", 400, true},
		{"pinned compact on a wide window", config.LayoutCompact, 1920, true},
		{"pinned regular on a phone", config.LayoutRegular, 360, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := compactForWidth(c.pref, phone, c.width); got != c.want {
				t.Errorf("compactForWidth(%q, mobile, %v) = %v, want %v", c.pref, c.width, got, c.want)
			}
		})
	}

	// A desktop window is only ever compact when explicitly pinned.
	desktop := gallery.NewPlatform()
	if compactForWidth(config.LayoutAuto, desktop, 300) {
		t.Error("auto made a narrow desktop window compact")
	}
	if !compactForWidth(config.LayoutCompact, desktop, 1920) {
		t.Error("pinned compact did not apply on desktop")
	}
}
