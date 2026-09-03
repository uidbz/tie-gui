package tieconfig

import (
	"fmt"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/conf"
	"github.com/uidbz/tie/client"
)

// Editor builds a settings form that edits a tie client config's [Collections.*]
// entries as plain TOML (no Fyne Preferences): a dropdown selects the active
// collection, "+"/"-" add and delete entries, and the form edits that
// collection's triplestore, namespace and credentials plus its primary filehost
// (URL, store, credentials, TLS). Apply writes the config to savePath and calls
// onApply with the saved config (DefaultCollection set to the active
// collection) so the caller can rebuild its client. It returns a scrollable
// CanvasObject, so callers wrap it in whatever tab/page they need. Persisting to
// savePath (under Dir) is what makes settings survive an Android reinstall.
func Editor(cfg client.Config, savePath string, onApply func(client.Config)) fyne.CanvasObject {
	// Edit a copy with cloned maps; mutations only reach the caller on Apply.
	cfg = clone(cfg)
	ensureCollections(&cfg)

	active := cfg.DefaultCollection
	if _, ok := cfg.Collections[active]; !ok {
		if ks := collectionKeys(cfg); len(ks) > 0 {
			active = ks[0]
		}
	}

	collKey := widget.NewEntry()
	namespace := widget.NewEntry()
	namespace.SetPlaceHolder("Collections")
	collectionID := widget.NewEntry()
	collectionID.SetPlaceHolder("(defaults to the collection name)")
	triplestoreURL := widget.NewEntry()
	triplestoreURL.SetPlaceHolder("http://localhost:1161")
	username := widget.NewEntry()
	password := widget.NewPasswordEntry()
	triplestoreInsecure := widget.NewCheck("", nil)

	hostName := widget.NewEntry()
	hostName.SetPlaceHolder("default")
	hostURL := widget.NewEntry()
	hostURL.SetPlaceHolder("http://localhost:1162")
	hostStore := widget.NewEntry()
	hostStore.SetPlaceHolder("(blank = default store)")
	hostUser := widget.NewEntry()
	hostPass := widget.NewPasswordEntry()
	hostInsecure := widget.NewCheck("", nil)

	status := widget.NewLabel("")

	// loadIntoForm shows a collection's stored values (raw, without top-level
	// fallbacks) so the form reflects what the file actually holds.
	loadIntoForm := func(key string) {
		e := cfg.Collections[key]
		collKey.SetText(key)
		namespace.SetText(e.Namespace)
		collectionID.SetText(e.Collection)
		triplestoreURL.SetText(e.TripleStoreURL)
		username.SetText(e.Username)
		password.SetText(e.Password)
		triplestoreInsecure.SetChecked(e.Insecure)

		hn := primaryHost(e)
		h := cfg.FileHosts[hn]
		hostName.SetText(hn)
		hostURL.SetText(h.URL)
		hostStore.SetText(h.Store)
		hostUser.SetText(h.Username)
		hostPass.SetText(h.Password)
		hostInsecure.SetChecked(h.Insecure)
	}

	dropdown := widget.NewSelect(collectionKeys(cfg), nil)
	dropdown.SetSelected(active)
	loadIntoForm(active)
	dropdown.OnChanged = func(key string) {
		if _, ok := cfg.Collections[key]; ok {
			loadIntoForm(key)
			status.SetText("")
		}
	}

	addBtn := widget.NewButton("+", func() {
		base := "new-collection"
		name := base
		for i := 2; ; i++ {
			if _, exists := cfg.Collections[name]; !exists {
				break
			}
			name = fmt.Sprintf("%s-%d", base, i)
		}
		cfg.Collections[name] = client.CollectionEntry{}
		dropdown.Options = collectionKeys(cfg)
		dropdown.Refresh()
		dropdown.SetSelected(name) // triggers OnChanged -> loadIntoForm
		status.SetText("Collection created; edit and Apply.")
	})

	delBtn := widget.NewButton("-", func() {
		if len(cfg.Collections) <= 1 {
			status.SetText("Cannot delete the only collection.")
			return
		}
		sel := dropdown.Selected
		delete(cfg.Collections, sel)
		if cfg.DefaultCollection == sel {
			cfg.DefaultCollection = ""
		}
		keys := collectionKeys(cfg)
		dropdown.Options = keys
		dropdown.Refresh()
		dropdown.SetSelected(keys[0])
		status.SetText("Collection deleted; Apply to persist.")
	})

	collRow := container.NewBorder(nil, nil, nil, container.NewHBox(addBtn, delBtn), dropdown)

	form := widget.NewForm(
		widget.NewFormItem("Collection name", collKey),
		widget.NewFormItem("Namespace", namespace),
		widget.NewFormItem("Collection id", collectionID),
		widget.NewFormItem("Triplestore URL", triplestoreURL),
		widget.NewFormItem("Triplestore username", username),
		widget.NewFormItem("Triplestore password", password),
		widget.NewFormItem("Skip triplestore TLS verify", triplestoreInsecure),
		widget.NewFormItem("Filehost name", hostName),
		widget.NewFormItem("Filehost URL", hostURL),
		widget.NewFormItem("Filehost store", hostStore),
		widget.NewFormItem("Filehost username", hostUser),
		widget.NewFormItem("Filehost password", hostPass),
		widget.NewFormItem("Skip filehost TLS verify", hostInsecure),
	)

	applyBtn := widget.NewButton("Apply connection", func() {
		oldKey := dropdown.Selected
		key := collKey.Text
		if key == "" {
			key = oldKey
		}

		var hosts []string
		if hostName.Text != "" {
			hosts = []string{hostName.Text}
		}
		entry := client.CollectionEntry{
			Namespace:      namespace.Text,
			Collection:     collectionID.Text,
			TripleStoreURL: triplestoreURL.Text,
			Username:       username.Text,
			Password:       password.Text,
			Insecure:       triplestoreInsecure.Checked,
			FileHosts:      hosts,
		}
		if key != oldKey {
			delete(cfg.Collections, oldKey)
		}
		cfg.Collections[key] = entry
		cfg.DefaultCollection = key

		if hostName.Text != "" {
			cfg.FileHosts[hostName.Text] = client.FileHost{
				URL:      hostURL.Text,
				Store:    hostStore.Text,
				Username: hostUser.Text,
				Password: hostPass.Text,
				Insecure: hostInsecure.Checked,
			}
			// The collection entry's own FileHosts already names this host; do
			// not touch the shared top-level DefaultFileHosts.
		}

		if err := conf.WriteConfig(savePath, cfg); err != nil {
			status.SetText("Save failed: " + err.Error())
			return
		}

		dropdown.Options = collectionKeys(cfg)
		dropdown.Refresh()
		dropdown.SetSelected(key)

		if onApply != nil {
			onApply(clone(cfg))
		}
		status.SetText("Saved to " + savePath)
	})

	return container.NewVScroll(container.NewVBox(collRow, form, applyBtn, status))
}

// collectionKeys returns the config's collection names, sorted.
func collectionKeys(c client.Config) []string {
	keys := make([]string, 0, len(c.Collections))
	for k := range c.Collections {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ensureCollections guarantees cfg has at least one collection to edit,
// synthesizing one from the flat fields when a config has no [Collections]
// table. client.LoadConfig normalizes loaded files, but a first-run built-in
// default is not normalized, so the form would otherwise be empty.
func ensureCollections(cfg *client.Config) {
	if len(cfg.Collections) > 0 {
		return
	}
	name := cfg.Collection
	if name == "" {
		name = "default"
	}
	cfg.Collections = map[string]client.CollectionEntry{
		name: {
			Namespace:  cfg.Namespace,
			Collection: cfg.Collection,
			FileHosts:  cfg.DefaultFileHosts,
		},
	}
	if cfg.DefaultCollection == "" {
		cfg.DefaultCollection = name
	}
}

// primaryHost returns the name of a collection entry's first filehost, or "".
func primaryHost(e client.CollectionEntry) string {
	if len(e.FileHosts) > 0 {
		return e.FileHosts[0]
	}
	return ""
}

// clone copies c with fresh Collections and FileHosts maps so edits to the
// returned config don't mutate the caller's shared maps.
func clone(c client.Config) client.Config {
	cols := make(map[string]client.CollectionEntry, len(c.Collections))
	for k, v := range c.Collections {
		cols[k] = v
	}
	c.Collections = cols
	hosts := make(map[string]client.FileHost, len(c.FileHosts))
	for k, v := range c.FileHosts {
		hosts[k] = v
	}
	c.FileHosts = hosts
	return c
}
