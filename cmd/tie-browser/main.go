// Command tie-browser is the tie-native scene browser: point-and-click pages
// stored in a tie collection, rendered by fynerisor's scene runtime and edited
// in place with the fynerisor designer.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/widget"

	pwclient "github.com/uidbz/pwplay/client"
	"github.com/uidbz/tie-gui/tieconfig"
	"github.com/uidbz/tie-gui/tiestyle"
	"github.com/uidbz/tie/client"

	"github.com/uidbz/fynerisor/browser"
	"github.com/uidbz/fynerisor/designer"
	"github.com/uidbz/fynerisor/gui"
	"github.com/uidbz/fynerisor/gui/guithread"
	guiscene "github.com/uidbz/fynerisor/gui/scene"
	"github.com/uidbz/fynerisor/scene"
	"github.com/uidbz/fynerisor/scene/render"
	renderbuiltin "github.com/uidbz/fynerisor/scene/render/builtin"
	tieassets "github.com/uidbz/fynerisor/scene/render/tie"
	tiestore "github.com/uidbz/fynerisor/scene/store/tie"
)

const appID = "github.com.uidbz.tiebrowser"

func main() {
	configName := flag.String("config", "", "tie config name or path")
	collection := flag.String("collection", "", "tie collection (default: config default)")
	home := flag.String("home", "", "home scene URL (default tie:/scenes/home)")
	pwplay := flag.String("pwplay", "http://localhost:8080", "pwplay-server for tie.audio playback")
	flag.Parse()

	cfg := tieconfig.Load(*configName)
	name := *collection
	if name == "" {
		name = cfg.DefaultCollection
	}
	tc := client.NewTieClientFor(cfg, name)
	store := tiestore.New(tc)
	store.OnIndexError = func(err error) {
		log.Printf("tie-browser: scene index: %v", err)
	}

	a := app.NewWithID(appID)
	tiestyle.Apply(a)
	win := a.NewWindow("tie-browser")
	win.Resize(fyne.NewSize(1280, 820))

	homeURL := *home
	if homeURL == "" {
		homeURL = "tie:/scenes/home"
	}

	tb := newTieBrowser(a, win, tc, store)
	if *pwplay != "" {
		tb.pw = pwclient.New(*pwplay)
	}
	tb.setHome(homeURL)
	tb.browser.ShowAndRun()
}

// TieBrowser is the browser host for tie scene pages.
type TieBrowser struct {
	fyneApp fyne.App
	window  *gui.Window
	browser *browser.Browser
	tc      *client.TieClient
	store   *tiestore.Store
	assets  *tieassets.Assets
	reg     *render.Registry
	source  string
	editor  *designer.Editor
	home    string
	pw      *pwclient.Client
}

func newTieBrowser(a fyne.App, win fyne.Window, tc *client.TieClient, store *tiestore.Store) *TieBrowser {
	reg := renderbuiltin.NewRegistry()
	tieassets.Register(reg, tc)
	// The indexer only records asset-kind props as scene members; without the
	// schema it also swept in collection/directory UIDs.
	store.Schema = reg.Registry
	t := &TieBrowser{
		fyneApp: a,
		tc:      tc,
		store:   store,
		assets:  tieassets.New(tc),
		reg:     reg,
	}
	t.window = gui.NewWindow(win,
		gui.WithAppName("tie-browser"),
		gui.WithGlobal("scene", guiscene.NewScene(guiscene.Config{
			Registry: reg, Assets: t.assets, Embed: &render.StoreResolver{Store: store},
			Navigator: tieNav{t}, Opener: tieOpener{t}, Status: func(s string) {},
		}, "")),
	)
	t.browser = browser.New(t.window, browser.Config{
		MenuProvider: tieMenu{t},
		LoadPage:     t.loadPage,
		RenderScene:  t.renderScene,
		OnNavigateError: func(u string, err error) {
			log.Printf("tie-browser: %s: %v", u, err)
		},
	})
	t.browser.EnableSourceView(t, nil)
	t.browser.EnableEditor(&tieEditor{t})
	return t
}

func (t *TieBrowser) setHome(home string) {
	t.home = home
	t.browser.SetHome(wrapHome(home))
	t.browser.SetHomeURL(wrapHome(home))
}

// wrapHome turns a scene path into a tie: URL. "tie:scenes/home" (no slash) is
// opaque to net/url and rejected by the store, so always keep the slash form.
func wrapHome(home string) string {
	home = strings.TrimSpace(home)
	if strings.HasPrefix(home, "tie:") || strings.HasPrefix(home, "http") || strings.HasPrefix(home, "file:") {
		return home
	}
	if strings.HasPrefix(home, "/") {
		return "tie:" + home
	}
	return "tie:/" + home
}

// GetCurrentSource implements browser.SourceViewProvider.
func (t *TieBrowser) GetCurrentSource() string { return t.source }

func (t *TieBrowser) loadPage(ctx context.Context, rawURL, _, _ string) (*browser.Page, error) {
	ref := wrapHome(rawURL)
	if !strings.HasPrefix(ref, "tie:") {
		return nil, fmt.Errorf("tie-browser can only load tie: pages, got %q", rawURL)
	}
	snap, err := t.store.Load(ctx, ref)
	if errors.Is(err, scene.ErrNotFound) && ref == wrapHome(t.home) {
		// Create the starter page only when the scene directory does not exist
		// at all. A directory that exists but has no pointer is an error (the
		// document was lost), not an invitation to overwrite it.
		if uid, uidErr := t.tc.DirUIDFromPath(ref); uidErr == nil && uid == "" {
			if createErr := ensureHomeScene(ctx, t.store, ref); createErr != nil {
				return nil, createErr
			}
			snap, err = t.store.Load(ctx, ref)
		}
	}
	if err != nil {
		return nil, err
	}
	doc, decErr := scene.Decode(snap.Data)
	if decErr != nil {
		return nil, decErr
	}
	var script []byte
	var scriptErr error
	if doc.Script != "" {
		// Sidecars live inside the scene directory. Load them here, on the
		// navigation goroutine, never on the UI thread.
		script, _, scriptErr = t.store.LoadScript(ctx, ref, path.Base(doc.Script))
	}
	return &browser.Page{
		URL: ref, Kind: browser.KindScene, Body: snap.Data, Version: snap.Version,
		Writable: true, Script: script, ScriptErr: scriptErr,
	}, nil
}

func (t *TieBrowser) renderScene(br *browser.Browser, p *browser.Page) error {
	t.source = string(p.Body)
	doc, issues, err := scene.DecodeValidated(p.Body, t.reg.Registry)
	if err != nil {
		return err
	}
	cfg := guiscene.Config{
		Registry: t.reg, Assets: t.assets,
		Embed:     &render.StoreResolver{Store: t.store},
		Window:    br.Window(),
		Navigator: tieNav{t}, Opener: tieOpener{t},
		Status: br.SetStatus,
	}
	page, err := guiscene.BuildPage(context.Background(), doc, cfg, p.URL, p.Version)
	if err != nil {
		return err
	}
	p.Dispose = page.View().Destroy

	// Put the data-only page on screen before running the script: a script
	// error must not leave a blank page, and scenes work without scripts.
	view := page.CanvasObject()
	if scene.Errors(issues) > 0 {
		banner := widget.NewLabel("Scene has validation problems: " + firstIssue(issues))
		banner.Wrapping = fyne.TextWrapWord
		view = container.NewBorder(banner, nil, nil, nil, view)
	}
	content := t.window.GetContentContainer()
	content.Objects = []fyne.CanvasObject{view}
	content.Refresh()

	if p.ScriptErr != nil {
		log.Printf("tie-browser: sidecar script %s: %v", p.URL, p.ScriptErr)
		br.SetStatus("Scene script unavailable: " + p.ScriptErr.Error())
	}
	script := "window.SetContent(page)\n"
	if len(p.Script) > 0 {
		script = string(p.Script) + "\n" + script
	}
	br.Window().LoadScript(script)
	br.Window().ExecuteWith(map[string]any{
		"page":  page,
		"scene": guiscene.NewScene(cfg, p.URL),
	})
	return nil
}

// saveThumbnail renders the saved scene offscreen and stores a preview blob.
// It runs off the UI thread: a save must never block on rendering, and a
// software-painter panic must not take the app down right after a save.
func (t *TieBrowser) saveThumbnail(ref string, doc *scene.Document) {
	clone := doc.Clone()
	go func() {
		var (
			view *render.View
			c    fyne.Canvas
		)
		guithread.DoAndWait(func() {
			view = render.NewView(clone, render.Options{
				Registry: t.reg, Assets: t.assets, Mode: render.ModePlay, Base: ref,
			})
			sw := software.NewCanvas()
			sw.SetContent(view)
			size := fyne.NewSize(640, 400)
			sw.Resize(size)
			view.Resize(size)
			c = sw
		})
		// Give asynchronous asset loads a bounded moment to land. The preview
		// is best-effort; the page itself is unaffected.
		time.Sleep(300 * time.Millisecond)
		var (
			data []byte
			err  error
		)
		guithread.DoAndWait(func() {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("thumbnail: %v", p)
				}
			}()
			capture, ok := c.(interface{ Capture() image.Image })
			if !ok {
				err = errors.New("thumbnail: canvas cannot capture")
				return
			}
			var buf bytes.Buffer
			if encErr := png.Encode(&buf, capture.Capture()); encErr != nil {
				err = encErr
				return
			}
			data = buf.Bytes()
		})
		guithread.Do(func() { view.Destroy() })
		if err != nil {
			log.Printf("tie-browser: thumbnail: %v", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := t.store.SaveThumbnail(ctx, ref, data); err != nil {
			log.Printf("tie-browser: thumbnail: %v", err)
		}
	}()
}

func firstIssue(issues []scene.Issue) string {
	for _, i := range issues {
		if i.Severity == scene.Error {
			return i.String()
		}
	}
	if len(issues) > 0 {
		return issues[0].String()
	}
	return "unknown"
}

// setSaveState maps the designer's save lifecycle onto the browser's
// persistent indicator.
func (t *TieBrowser) setSaveState(s designer.SaveState, err error) {
	switch s {
	case designer.SaveDirty:
		t.browser.SetSaveState("● Unsaved")
	case designer.SaveSaving:
		t.browser.SetSaveState("Saving…")
	case designer.SaveSaved:
		t.browser.SetSaveState("Saved")
	case designer.SaveFailed:
		if err != nil {
			t.browser.SetSaveState("Save failed")
		} else {
			t.browser.SetSaveState("Save failed")
		}
	default:
		t.browser.SetSaveState("")
	}
}

// tieEditor implements browser.EditorProvider.
type tieEditor struct{ app *TieBrowser }

func (e *tieEditor) CanEdit(p *browser.Page) bool {
	return p != nil && p.Kind == browser.KindScene && p.Writable
}

func (e *tieEditor) Open(p *browser.Page, br *browser.Browser) fyne.CanvasObject {
	doc, err := scene.Decode(p.Body)
	if err != nil {
		br.SetStatus("Cannot edit: " + err.Error())
		return nil
	}
	ed := designer.New(doc, designer.Options{
		Registry:  e.app.reg,
		Assets:    e.app.assets,
		Embed:     &render.StoreResolver{Store: e.app.store},
		Store:     e.app.store,
		Ref:       p.URL,
		Version:   p.Version,
		Window:    br.Window().FyneWindow,
		Navigator: tieNav{e.app}, Opener: tieOpener{e.app},
		Status: br.SetStatus,
		OnDirty: func(dirty bool) {
			if dirty {
				br.SetSaveState("● Unsaved")
			}
		},
		OnSaveState: e.app.setSaveState,
		AssetPick:   e.app.pickAsset,
		OnClose: func(reload bool) {
			e.app.editor = nil
			br.CloseEditor(reload)
		},
		OnSaved: func(doc *scene.Document) { e.app.saveThumbnail(p.URL, doc) },
	})
	e.app.editor = ed
	return ed.Object()
}

func (e *tieEditor) BeforeLeave(done func(ok bool)) {
	if e.app.editor == nil || e.app.editor.Closed() {
		e.app.editor = nil
		done(true)
		return
	}
	e.app.editor.BeforeLeave(done)
}

// RequestClose is called when the user toggles edit mode (pencil / Ctrl+E /
// F4). Route it through the designer's save prompt instead of discarding.
func (e *tieEditor) RequestClose() {
	if e.app.editor != nil && !e.app.editor.Closed() {
		e.app.editor.RequestClose()
	}
}

func (e *tieEditor) EditorClosed() {
	if e.app.editor != nil {
		e.app.editor.Abandon()
	}
	e.app.editor = nil
}

// tieNav maps scene actions to browser navigation.
type tieNav struct{ app *TieBrowser }

func (n tieNav) Navigate(ref string) { n.app.browser.Navigate(ref) }
func (n tieNav) Back()               { n.app.browser.Back() }

// tieOpener opens tie blobs (and ordinary URLs) with the system handler.
type tieOpener struct{ app *TieBrowser }

func (o tieOpener) Open(ref string) {
	if strings.HasPrefix(ref, "tie:") {
		hash := strings.TrimPrefix(ref, "tie:")
		if len(hash) != 64 {
			o.app.browser.SetStatus("Cannot open " + ref)
			return
		}
		// Download off the UI thread: a video or camera original is large, and
		// the system handler must not freeze the window.
		go func() {
			dir, err := os.UserCacheDir()
			if err != nil {
				dir = os.TempDir()
			}
			dir = filepath.Join(dir, "tie-browser")
			_ = os.MkdirAll(dir, 0o755)
			dest := filepath.Join(dir, hash)
			if _, statErr := os.Stat(dest); statErr != nil {
				if dlErr := o.app.tc.Download("", hash, dest); dlErr != nil {
					guithread.Do(func() { o.app.browser.SetStatus("Open failed: " + dlErr.Error()) })
					return
				}
			}
			guithread.Do(func() {
				_ = fyne.CurrentApp().OpenURL(&url.URL{Scheme: "file", Path: filepath.ToSlash(dest)})
			})
		}()
		return
	}
	if u, err := url.Parse(ref); err == nil {
		_ = fyne.CurrentApp().OpenURL(u)
	}
}

func (o tieOpener) Play(ref string) {
	hash := strings.TrimPrefix(ref, "tie:")
	if o.app.pw == nil || len(hash) != 64 {
		o.Open(ref)
		return
	}
	go func() {
		host, err := o.app.tc.ResolveHost("")
		if err != nil {
			guithread.Do(func() { o.app.browser.SetStatus("Play failed: " + err.Error()) })
			return
		}
		stream := strings.TrimRight(host.URL, "/") + "/" + hash
		if err := playNow(o.app.pw, stream); err != nil {
			guithread.Do(func() {
				o.app.browser.SetStatus("Play failed: " + err.Error())
				o.Open(ref)
			})
			return
		}
		guithread.Do(func() { o.app.browser.SetStatus("Playing " + hash[:8]) })
	}()
}

func (o tieOpener) Pause() {
	if o.app.pw != nil {
		go func() { _ = o.app.pw.Pause() }()
	}
}

// editScript opens the scene's sidecar script in a plain editor. The script
// runs after the data-only page is on screen, so a broken edit never blanks
// the page; saving reloads the scene.
func (t *TieBrowser) editScript() {
	if t.browser.IsEditing() {
		t.browser.SetStatus("Finish editing the page before changing its script")
		return
	}
	p := t.browser.CurrentPage()
	if p == nil || p.Kind != browser.KindScene || !p.Writable {
		t.browser.SetStatus("This page has no editable script")
		return
	}
	doc, err := scene.Decode(p.Body)
	if err != nil {
		t.browser.SetStatus("Cannot edit script: " + err.Error())
		return
	}
	name := doc.Script
	if name == "" {
		name = tiestore.DefaultScriptName
	}
	entry := widget.NewMultiLineEntry()
	entry.SetText(string(p.Script))
	entry.SetPlaceHolder("// page.risor — runs after the data-only page is on screen.\n// page.Node(\"title\").SetText(\"Hello\")")
	entry.SetMinRowsVisible(18)
	version := p.Version
	d := dialog.NewCustomConfirm("Edit page script", "Save", "Cancel", entry, func(save bool) {
		if !save {
			return
		}
		src := entry.Text
		go t.saveScript(p.URL, doc, name, src, version)
	}, t.window.FyneWindow)
	d.Resize(fyne.NewSize(820, 620))
	d.Show()
}

func (t *TieBrowser) saveScript(ref string, doc *scene.Document, name, src, version string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Write the script first; then point the scene at it when it had none.
	if err := t.store.SaveScript(ctx, ref, path.Base(name), []byte(src)); err != nil {
		guithread.Do(func() { t.browser.SetStatus("Script save failed: " + err.Error()) })
		return
	}
	if doc.Script == "" {
		doc.Script = name
		data, err := scene.Encode(doc)
		if err != nil {
			guithread.Do(func() { t.browser.SetStatus("Scene save failed: " + err.Error()) })
			return
		}
		if _, err := t.store.Save(ctx, ref, data, version); err != nil {
			guithread.Do(func() { t.browser.SetStatus("Scene save failed: " + err.Error()) })
			return
		}
	}
	guithread.Do(func() {
		t.browser.SetStatus("Script saved")
		t.browser.Reload()
	})
}

type tieMenu struct{ app *TieBrowser }

func (m tieMenu) GetMenuItems() []browser.MenuItem {
	return []browser.MenuItem{
		{Label: "Home", Action: func() { m.app.browser.Navigate(wrapHome(m.app.home)) }},
		{Label: "Edit Page (Ctrl+E)", Action: func() { m.app.browser.ToggleEdit() }},
		{Label: "Edit Page Script…", Action: m.app.editScript},
		{Label: "View Source", Action: func() { m.app.browser.ToggleSourceView() }},
		{Label: "Quit", Action: func() { m.app.fyneApp.Quit() }},
	}
}

// ensureHomeScene creates a starter scene on first run. Callers must have
// verified that the scene directory does not exist.
func ensureHomeScene(ctx context.Context, store *tiestore.Store, home string) error {
	ref := wrapHome(home)
	if _, err := store.Load(ctx, ref); err == nil || !errors.Is(err, scene.ErrNotFound) {
		return err
	}
	doc := &scene.Document{
		Format: scene.Format,
		Title:  "Welcome to tie-browser",
		Root: &scene.Node{
			ID: "root", Type: "free",
			Place: scene.Place{W: 1200, H: 700},
			Props: map[string]any{
				"designW": 1200.0, "designH": 700.0, "scale": "fit", "letterbox": "#0f172a",
			},
			Children: []*scene.Node{
				{ID: "title", Type: "label", Place: scene.Place{X: 80, Y: 70, W: 700, H: 60},
					Style: &scene.Style{Fg: "#f8fafc"},
					Props: map[string]any{"text": "tie-browser", "bold": true}},
				{ID: "hint", Type: "label", Place: scene.Place{X: 80, Y: 150, W: 900, H: 120},
					Style: &scene.Style{Fg: "#cbd5e1"},
					Props: map[string]any{"text": "A point-and-click browser for tie scenes. Press Ctrl+E (or the pencil) to edit this page, add hotspots and tie media from the palette, then Done to save.", "wrap": "word"}},
				{ID: "edit-hint", Type: "label", Place: scene.Place{X: 80, Y: 320, W: 900, H: 40},
					Style: &scene.Style{Fg: "#cbd5e1"},
					Props: map[string]any{"text": "Ctrl+E toggles the designer; Ctrl+S saves while editing."}},
			},
		},
	}
	data, err := scene.Encode(doc)
	if err != nil {
		return err
	}
	_, err = store.Save(ctx, ref, data, "")
	return err
}
