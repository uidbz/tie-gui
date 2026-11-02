package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/uidbz/tie/client"

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
