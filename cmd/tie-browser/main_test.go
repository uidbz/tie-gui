package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/uidbz/tie/client"

	"github.com/uidbz/fynerisor/gui"
	"github.com/uidbz/fynerisor/scene"
	tiestore "github.com/uidbz/fynerisor/scene/store/tie"
)

func TestWrapHome(t *testing.T) {
	if got := wrapHome("scenes/home"); got != "tie:scenes/home" {
		t.Fatalf("wrapHome = %q", got)
	}
	if got := wrapHome("tie:/scenes/home"); got != "tie:/scenes/home" {
		t.Fatalf("wrapHome kept = %q", got)
	}
}

func TestEnsureHomeScene(t *testing.T) {
	cfg := client.TestingConfig()
	cfg.Username = "defaultuser"
	cfg.Password = "defaultpassword"
	tc := client.NewTieClient(cfg)
	tc.SetTimeout(2 * time.Second)
	if _, err := tc.ServerVersion(); err != nil {
		t.Skipf("tie test environment not running: %v", err)
	}
	store := tiestore.New(tc)
	home := fmt.Sprintf("tie:/fynerisor-browser-tests/%d/home", time.Now().UnixNano())
	ctx := context.Background()
	if err := ensureHomeScene(ctx, store, home); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Load(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := scene.Decode(snap.Data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title == "" || doc.Count() < 3 {
		t.Fatalf("welcome scene looks empty: %+v", doc)
	}
}

func TestRenderHomeScene(t *testing.T) {
	cfg := client.TestingConfig()
	cfg.Username = "defaultuser"
	cfg.Password = "defaultpassword"
	tc := client.NewTieClient(cfg)
	tc.SetTimeout(2 * time.Second)
	if _, err := tc.ServerVersion(); err != nil {
		t.Skipf("tie test environment not running: %v", err)
	}
	store := tiestore.New(tc)
	home := fmt.Sprintf("tie:/fynerisor-browser-tests/%d/browser-home", time.Now().UnixNano())
	if err := ensureHomeScene(context.Background(), store, home); err != nil {
		t.Fatal(err)
	}
	a := test.NewApp()
	defer a.Quit()
	w := test.NewWindow(nil)
	w.Resize(fyne.NewSize(900, 600))
	tb := newTieBrowser(a, w, tc, store)
	tb.setHome(home)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tb.browser.CurrentPage() != nil {
			st, err := tb.window.State()
			if st == gui.StateReady {
				return
			}
			if st == gui.StateFailed {
				t.Fatalf("scene script failed: %v", err)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	st, err := tb.window.State()
	t.Fatalf("scene did not become ready: %s %v page=%v", st, err, tb.browser.CurrentPage())
}

func TestSplitQuery(t *testing.T) {
	include, exclude := splitQuery("family 2014 -private -draft")
	if len(include) != 2 || include[0] != "family" || include[1] != "2014" {
		t.Fatalf("include = %v", include)
	}
	if len(exclude) != 2 || exclude[0] != "private" || exclude[1] != "draft" {
		t.Fatalf("exclude = %v", exclude)
	}
}
