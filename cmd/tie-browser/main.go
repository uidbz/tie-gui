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
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/software"

	"github.com/uidbz/tie-gui/tieconfig"
	"github.com/uidbz/tie/client"

	"github.com/uidbz/fynerisor/browser"
	"github.com/uidbz/fynerisor/designer"
	"github.com/uidbz/fynerisor/gui"
	guiscene "github.com/uidbz/fynerisor/gui/scene"
	"github.com/uidbz/fynerisor/scene"
	"github.com/uidbz/fynerisor/scene/render"
	renderbuiltin "github.com/uidbz/fynerisor/scene/render/builtin"
	tieassets "github.com/uidbz/fynerisor/scene/render/tie"
	tiestore "github.com/uidbz/fynerisor/scene/store/tie"
)

const appID = "com.uidbz.tiebrowser"

func main() {
	configName := flag.String("config", "", "tie config name or path")
	collection := flag.String("collection", "", "tie collection (default: config default)")
	home := flag.String("home", "", "home scene URL (default tie:/scenes/home)")
	flag.Parse()

	cfg := tieconfig.Load(*configName)
	name := *collection
	if name == "" {
		name = cfg.DefaultCollection
	}
	tc := client.NewTieClientFor(cfg, name)
	store := tiestore.New(tc)

	a := app.NewWithID(appID)
	win := a.NewWindow("tie-browser")
	win.Resize(fyne.NewSize(1280, 820))

	homeURL := *home
	if homeURL == "" {
		homeURL = "tie:/scenes/home"
	}
	if err := ensureHomeScene(context.Background(), store, homeURL); err != nil {
		log.Println("tie-browser: home scene:", err)
	}

	tb := newTieBrowser(a, win, tc, store)
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
}

func newTieBrowser(a fyne.App, win fyne.Window, tc *client.TieClient, store *tiestore.Store) *TieBrowser {
	reg := renderbuiltin.NewRegistry()
	tieassets.Register(reg, tc)
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

func wrapHome(home string) string {
	if strings.HasPrefix(home, "tie:") || strings.HasPrefix(home, "http") || strings.HasPrefix(home, "file:") {
		return home
	}
	return "tie:" + home
}

// GetCurrentSource implements browser.SourceViewProvider.
func (t *TieBrowser) GetCurrentSource() string { return t.source }

func (t *TieBrowser) loadPage(ctx context.Context, rawURL, _, _ string) (*browser.Page, error) {
	ref := wrapHome(rawURL)
	if !strings.HasPrefix(ref, "tie:") {
		return nil, fmt.Errorf("tie-browser can only load tie: pages, got %q", rawURL)
	}
	snap, err := t.store.Load(ctx, ref)
	if err != nil {
		return nil, err
	}
	t.source = string(snap.Data)
	return &browser.Page{URL: ref, Kind: browser.KindScene, Body: snap.Data, Version: snap.Version, Writable: true}, nil
}

func (t *TieBrowser) renderScene(br *browser.Browser, p *browser.Page) error {
	doc, issues, err := scene.DecodeValidated(p.Body, t.reg.Registry)
	if err != nil {
		return err
	}
	if scene.Errors(issues) > 0 {
		return fmt.Errorf("scene validation: %s", firstIssue(issues))
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
	script := "window.SetContent(page)\n"
	if doc.Script != "" {
		ref, err := scene.ResolveRef(p.URL, doc.Script)
		if err != nil {
			return err
		}
		snap, err := t.store.Load(context.Background(), ref)
		if err != nil {
			return fmt.Errorf("sidecar %s: %w", ref, err)
		}
		script = string(snap.Data) + "\nwindow.SetContent(page)\n"
	}
	br.Window().LoadScript(script)
	br.Window().ExecuteWith(map[string]any{
		"page":  page,
		"scene": guiscene.NewScene(cfg, p.URL),
	})
	return nil
}

// saveThumbnail renders the saved scene offscreen and stores a preview blob.
// Rendering happens on the UI thread; the upload runs in the background.
func (t *TieBrowser) saveThumbnail(ref string, doc *scene.Document) {
	view := render.NewView(doc, render.Options{Registry: t.reg, Assets: t.assets, Mode: render.ModePlay, Base: ref})
	canvas := software.NewCanvas()
	canvas.SetContent(view)
	size := fyne.NewSize(320, 200)
	canvas.Resize(size)
	view.Resize(size)
	capture, ok := canvas.(interface{ Capture() image.Image })
	if !ok {
		return
	}
	img := capture.Capture()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return
	}
	data := buf.Bytes()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := t.store.SaveThumbnail(ctx, ref, data); err != nil {
			log.Printf("tie-browser: thumbnail: %v", err)
		}
	}()
	view.Destroy()
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
		Store:     e.app.store,
		Ref:       p.URL,
		Version:   p.Version,
		Window:    br.Window().FyneWindow,
		Navigator: tieNav{e.app}, Opener: tieOpener{e.app},
		Status: br.SetStatus,
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
		dir, err := os.UserCacheDir()
		if err != nil {
			dir = os.TempDir()
		}
		dir = filepath.Join(dir, "tie-browser")
		_ = os.MkdirAll(dir, 0o755)
		dest := filepath.Join(dir, hash)
		if _, err := os.Stat(dest); err != nil {
			if err := o.app.tc.Download("", hash, dest); err != nil {
				o.app.browser.SetStatus("Open failed: " + err.Error())
				return
			}
		}
		_ = fyne.CurrentApp().OpenURL(&url.URL{Scheme: "file", Path: filepath.ToSlash(dest)})
		return
	}
	if u, err := url.Parse(ref); err == nil {
		_ = fyne.CurrentApp().OpenURL(u)
	}
}

func (o tieOpener) Play(ref string) { o.Open(ref) }
func (o tieOpener) Pause()          {}

type tieMenu struct{ app *TieBrowser }

func (m tieMenu) GetMenuItems() []browser.MenuItem {
	return []browser.MenuItem{
		{Label: "Home", Action: func() { m.app.browser.Navigate(wrapHome(m.app.home)) }},
		{Label: "Edit Page", Action: func() { m.app.browser.ToggleEdit() }},
		{Label: "View Source", Action: func() { m.app.browser.ToggleSourceView() }},
		{Label: "Quit", Action: func() { m.app.fyneApp.Quit() }},
	}
}

// ensureHomeScene creates a starter scene on first run.
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
					Props: map[string]any{"text": "tie-browser", "bold": true}},
				{ID: "hint", Type: "label", Place: scene.Place{X: 80, Y: 150, W: 900, H: 120},
					Props: map[string]any{"text": "A point-and-click browser for tie scenes. Press F4 or the pencil to edit this page, add hotspots and tie media from the palette, then Done to save.", "wrap": "word"}},
				{ID: "edit-hint", Type: "label", Place: scene.Place{X: 80, Y: 320, W: 700, H: 40},
					Props: map[string]any{"text": "Press F4 or the pencil to edit this page."}},
			},
		},
	}
	// The edit button uses the browser's own edit command; scene actions cannot
	// call it, so a slot/script example is left to the editor.
	data, err := scene.Encode(doc)
	if err != nil {
		return err
	}
	_, err = store.Save(ctx, ref, data, "")
	return err
}
