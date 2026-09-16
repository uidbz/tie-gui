package main

import (
	"reflect"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"
)

// The Settings tab wraps its AppTabs in a border center so the tabs have a
// stable frame to fill: AppTabs' MinSize is the max of its tab contents'
// MinSizes, and both editors are scroll containers (MinSize ≈ 32px), so in
// the narrow sidebar an unwrapped AppTabs collapses to that height and only
// the connection editor's dropdown row is visible.
func TestSettingsTabWrapsTabsInBorder(t *testing.T) {
	test.NewApp()
	tc := client.NewTieClientFor(client.TestingConfig(), "testing")
	tieConfigPath = t.TempDir() + "/config.toml"

	tab := makeSettingsTab(tc, func() string { return "testing" },
		func(client.Config, string) {}, func() {}, widget.NewLabel("quick"))

	inner, ok := tab.Content.(*fyne.Container)
	if !ok || inner.Layout == nil {
		t.Fatalf("settings tab content = %T, want a *fyne.Container with a layout", tab.Content)
	}
	want := layout.NewBorderLayout(nil, nil, nil, nil)
	if !reflect.DeepEqual(inner.Layout, want) {
		t.Fatalf("settings tab layout = %#v, want border layout %#v", inner.Layout, want)
	}
	var tabs *container.AppTabs
	for _, o := range inner.Objects {
		if at, ok := o.(*container.AppTabs); ok {
			tabs = at
		}
	}
	if tabs == nil {
		t.Fatal("settings tab border does not contain the inner AppTabs")
	}
	if len(tabs.Items) != 3 {
		t.Fatalf("inner tabs = %d, want 3 (Connection + Quick tags + Startup)", len(tabs.Items))
	}
}
