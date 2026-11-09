package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
	"github.com/uidbz/tie-gui/gallery"
)

// TabbedPane is one side of the twin-panel window: a tab bar plus a stack of
// independent FileManager views. Each tab keeps its own history, path, search
// and tag-filter state.
type TabbedPane struct {
	win      fyne.Window
	registry *fs.Registry
	ops      *fs.Operations
	cfg      *config.Config

	previewCfg   gallery.Config
	previewThumb gallery.Thumbnailer
	previewReady bool

	tabs    []*FileManager
	current int
	active  bool
	sibling *TabbedPane

	onActivate func(*TabbedPane)
	onBookmark func(path string)

	tabButtons *fyne.Container
	tabScroll  *container.Scroll
	stack      *fyne.Container
	view       *fyne.Container
}

// NewTabbedPane creates a pane with one initial tab at startPath.
func NewTabbedPane(startPath string, registry *fs.Registry, ops *fs.Operations, cfg *config.Config, win fyne.Window) *TabbedPane {
	tp := &TabbedPane{
		win:      win,
		registry: registry,
		ops:      ops,
		cfg:      cfg,
	}
	tp.tabButtons = container.NewHBox()
	tp.tabScroll = container.NewHScroll(tp.tabButtons)
	tp.stack = container.NewStack()

	add := widget.NewButtonWithIcon("", theme.ContentAddIcon(), tp.NewTab)
	add.Importance = widget.LowImportance
	strip := container.NewBorder(nil, nil, nil, add, tp.tabScroll)
	tp.view = container.NewBorder(strip, nil, nil, nil, tp.stack)

	tp.addTab(startPath)
	return tp
}

// View returns the pane's tab bar and active tab content.
func (tp *TabbedPane) View() *fyne.Container { return tp.view }

// SetOnActive registers the handler called when this pane is focused.
func (tp *TabbedPane) SetOnActive(fn func(*TabbedPane)) { tp.onActivate = fn }

// SetBookmarkHandler registers the handler used by every tab's bookmark button.
func (tp *TabbedPane) SetBookmarkHandler(fn func(path string)) { tp.onBookmark = fn }

// SetSibling wires the other pane so copy/move always target its current tab.
func (tp *TabbedPane) SetSibling(other *TabbedPane) {
	tp.sibling = other
	tp.syncSiblings()
}

// InitPreview configures preview mode for the current and future tabs.
func (tp *TabbedPane) InitPreview(cfg gallery.Config, thumb gallery.Thumbnailer) {
	tp.previewCfg = cfg
	tp.previewThumb = thumb
	tp.previewReady = true
	for _, fm := range tp.tabs {
		fm.InitPreview(cfg, thumb)
	}
}

// SetActive toggles the active-pane highlight for the current tab.
func (tp *TabbedPane) SetActive(active bool) {
	tp.active = active
	if fm := tp.currentFM(); fm != nil {
		fm.SetActive(active)
	}
}

// FocusPane makes this pane active and focuses its current tab.
func (tp *TabbedPane) FocusPane() {
	tp.markActive()
	if fm := tp.currentFM(); fm != nil {
		fm.focusPane()
	}
}

// Reload re-lists every tab (after a backend/config switch).
func (tp *TabbedPane) Reload() {
	for _, fm := range tp.tabs {
		fm.Reload()
	}
}

// NavigateTo navigates the current tab.
func (tp *TabbedPane) NavigateTo(path string) {
	if fm := tp.currentFM(); fm != nil {
		fm.NavigateTo(path)
	}
}

// CurrentPath returns the current tab's path.
func (tp *TabbedPane) CurrentPath() string {
	if fm := tp.currentFM(); fm != nil {
		return fm.CurrentPath()
	}
	return ""
}

// ShowFilter reveals the current tab's filter/page bar.
func (tp *TabbedPane) ShowFilter() {
	if fm := tp.currentFM(); fm != nil {
		fm.ShowFilter()
	}
}

// PreviewHandlesKey routes keys to the current tab's preview grid.
func (tp *TabbedPane) PreviewHandlesKey(key *fyne.KeyEvent) bool {
	if fm := tp.currentFM(); fm != nil {
		return fm.PreviewHandlesKey(key)
	}
	return false
}

// NewTab opens a new tab at the current location.
func (tp *TabbedPane) NewTab() {
	path := tp.CurrentPath()
	tp.NewTabAt(path)
}

// NewTabAt opens path in a new tab and focuses it.
func (tp *TabbedPane) NewTabAt(path string) {
	tp.addTab(path)
	tp.FocusPane()
}

// CloseTab closes the current tab. The last tab is kept so a pane always has a
// location to show.
func (tp *TabbedPane) CloseTab() {
	if len(tp.tabs) <= 1 {
		return
	}
	idx := tp.current
	old := tp.tabs[idx]
	old.endPathEdit()
	old.cancelSearchState()
	tp.stack.Remove(old.View())
	tp.tabs = append(tp.tabs[:idx], tp.tabs[idx+1:]...)
	if idx >= len(tp.tabs) {
		idx = len(tp.tabs) - 1
	}
	tp.current = -1
	tp.selectTab(idx)
	tp.FocusPane()
}

func (tp *TabbedPane) addTab(path string) *FileManager {
	fm := NewFileManager(path, tp.registry, tp.ops, tp.cfg, tp.win)
	fm.SetOnActive(func(*FileManager) { tp.markActive() })
	fm.SetBookmarkHandler(func(path string) {
		if tp.onBookmark != nil {
			tp.onBookmark(path)
		}
	})
	fm.onPathChanged = func(string) { tp.updateTabBar() }
	fm.onOpenInNewTab = tp.NewTabAt
	if tp.previewReady {
		fm.InitPreview(tp.previewCfg, tp.previewThumb)
	}

	tp.tabs = append(tp.tabs, fm)
	tp.stack.Add(fm.View())
	fm.View().Hide()
	tp.selectTab(len(tp.tabs) - 1)
	return fm
}

func (tp *TabbedPane) selectTab(idx int) {
	if idx < 0 || idx >= len(tp.tabs) {
		return
	}
	tp.current = idx
	for i, fm := range tp.tabs {
		if i == idx {
			fm.View().Show()
			fm.SetActive(tp.active)
		} else {
			fm.View().Hide()
			fm.SetActive(false)
		}
	}
	tp.stack.Refresh()
	tp.syncSiblings()
	tp.updateTabBar()
}

func (tp *TabbedPane) currentFM() *FileManager {
	if tp.current < 0 || tp.current >= len(tp.tabs) {
		return nil
	}
	return tp.tabs[tp.current]
}

func (tp *TabbedPane) markActive() {
	if tp.onActivate != nil {
		tp.onActivate(tp)
	}
}

func (tp *TabbedPane) syncSiblings() {
	if tp.sibling == nil {
		return
	}
	a, b := tp.currentFM(), tp.sibling.currentFM()
	if a != nil && b != nil {
		a.SetSibling(b)
		b.SetSibling(a)
	}
}

func (tp *TabbedPane) updateTabBar() {
	tp.tabButtons.RemoveAll()
	for i, fm := range tp.tabs {
		idx := i
		btn := widget.NewButton(tabTitle(fm.CurrentPath()), func() {
			tp.selectTab(idx)
			tp.FocusPane()
		})
		if idx == tp.current {
			btn.Importance = widget.HighImportance
		} else {
			btn.Importance = widget.LowImportance
		}
		tp.tabButtons.Add(btn)
	}
	tp.tabButtons.Refresh()
	tp.tabScroll.Refresh()
}

// tabTitle is the short label shown on a tab: the last path component, or the
// scheme root for tie:/ and similar locations.
func tabTitle(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if strings.HasSuffix(path, ":/") {
		return path
	}
	trimmed := strings.TrimRight(path, "/\\")
	if trimmed == "" {
		return "/"
	}
	if i := strings.LastIndexAny(trimmed, "/\\"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	if trimmed == "" {
		return "/"
	}
	r := []rune(trimmed)
	if len(r) > 24 {
		trimmed = string(r[:23]) + "…"
	}
	return trimmed
}
