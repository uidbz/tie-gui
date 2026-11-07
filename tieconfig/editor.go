package tieconfig

import (
	"fmt"
	"sort"
	"strings"

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
// (URL, store, credentials, TLS). active names the collection the caller is
// currently bound to (its own stored/default profile — see AppCollection); the
// dropdown preselects it, falling back to DefaultCollection and then the first
// entry.
//
// Picking a collection in the dropdown fires onSelect (not on programmatic
// selections), so the caller can switch its live client to the chosen profile
// immediately and remember the choice in its own config; the tie file itself
// is only written by Apply. Apply writes the config to savePath and calls
// onApply with the saved config (DefaultCollection set to the applied
// collection) so the caller can rebuild its client. The editor returns a
// scrollable CanvasObject, so callers wrap it in whatever tab/page they need.
// Persisting to savePath (under Dir) is what makes settings survive an Android
// reinstall.
func Editor(cfg client.Config, savePath, active string, onSelect func(name string), onApply func(client.Config)) fyne.CanvasObject {
	// Edit a copy with cloned maps; mutations only reach the caller on Apply.
	cfg = clone(cfg)
	ensureCollections(&cfg)

	if _, ok := cfg.Collections[active]; !ok {
		active = cfg.DefaultCollection
	}
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
	status.Wrapping = fyne.TextWrapWord
	target := widget.NewLabel("")
	target.Wrapping = fyne.TextWrapWord

	// refreshTarget is assigned after formConfig exists; loadIntoForm calls it
	// so programmatic form fills keep the effective-target line in step.
	var refreshTarget func()

	// loadIntoForm shows a collection's stored values (raw, without top-level
	// fallbacks) so the form reflects what the file actually holds. The
	// effective-target line below the form shows the values a client resolves,
	// including those inherited from the top level.
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
		if refreshTarget != nil {
			refreshTarget()
		}
	}

	dropdown := widget.NewSelect(collectionKeys(cfg), nil)
	// suppressSelect stops programmatic SetSelected calls (init, add, delete,
	// Apply) from firing onSelect; only user picks switch the caller's client.
	suppressSelect := false
	setSelected := func(key string) {
		suppressSelect = true
		dropdown.SetSelected(key)
		suppressSelect = false
	}

	dropdown.SetSelected(active) // no OnChanged assigned yet
	loadIntoForm(active)
	dropdown.OnChanged = func(key string) {
		if _, ok := cfg.Collections[key]; ok {
			loadIntoForm(key)
			status.SetText("")
			if !suppressSelect && onSelect != nil {
				onSelect(key)
			}
		}
	}

	// formConfig builds the config the form currently describes, without
	// writing it. rename removes the old collection key when the user changed
	// the name; display/test calls keep both so a half-typed name does not
	// discard the selected entry from the in-memory copy.
	formConfig := func(rename bool) (client.Config, string) {
		oldKey := dropdown.Selected
		key := strings.TrimSpace(collKey.Text)
		if key == "" {
			key = oldKey
		}

		out := clone(cfg)
		var hosts []string
		if name := strings.TrimSpace(hostName.Text); name != "" {
			hosts = []string{name}
		}
		if rename && key != oldKey {
			delete(out.Collections, oldKey)
		}
		out.Collections[key] = client.CollectionEntry{
			Namespace:      namespace.Text,
			Collection:     collectionID.Text,
			TripleStoreURL: triplestoreURL.Text,
			Username:       username.Text,
			Password:       password.Text,
			Insecure:       triplestoreInsecure.Checked,
			FileHosts:      hosts,
		}
		out.DefaultCollection = key

		if name := strings.TrimSpace(hostName.Text); name != "" {
			out.FileHosts[name] = client.FileHost{
				URL:      hostURL.Text,
				Store:    hostStore.Text,
				Username: hostUser.Text,
				Password: hostPass.Text,
				Insecure: hostInsecure.Checked,
			}
		}
		return out, key
	}

	// clearDBBtn appears only when the config pins a local .tie database, which
	// takes precedence over the triplestore URL. On a phone that file is usually
	// not present, so the URL settings would be silently ignored.
	var clearDBBtn *widget.Button
	clearDBBtn = widget.NewButton("Clear local database file", func() {
		cfg.TripleStoreFile = ""
		clearDBBtn.Hide()
		refreshTarget()
		status.SetText("Local database file cleared; Apply to save.")
	})
	clearDBBtn.Hide()

	refreshTarget = func() {
		out, key := formConfig(false)
		target.SetText(targetSummary(out, key))
		if out.TripleStoreFile != "" {
			clearDBBtn.Show()
		} else {
			clearDBBtn.Hide()
		}
	}

	for _, entry := range []*widget.Entry{
		collKey, namespace, collectionID, triplestoreURL, username, password,
		hostName, hostURL, hostStore, hostUser, hostPass,
	} {
		entry.OnChanged = func(string) { refreshTarget() }
	}
	triplestoreInsecure.OnChanged = func(bool) { refreshTarget() }
	hostInsecure.OnChanged = func(bool) { refreshTarget() }
	refreshTarget()

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
		setSelected(name) // triggers OnChanged -> loadIntoForm
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
		setSelected(keys[0])
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

	var testBtn *widget.Button
	testBtn = widget.NewButton("Test connection", func() {
		// Snapshot the form before leaving the UI goroutine; widgets must not
		// be read from the test worker.
		out, key := formConfig(false)
		testBtn.Disable()
		status.SetText("Testing connection…")
		go func() {
			report := testConnection(out, key)
			fyne.Do(func() {
				status.SetText(report)
				testBtn.Enable()
				refreshTarget()
			})
		}()
	})

	applyBtn := widget.NewButton("Apply connection", func() {
		saved, key := formConfig(true)

		if err := conf.WriteConfig(savePath, saved); err != nil {
			status.SetText("Save failed: " + err.Error())
			return
		}
		cfg = saved

		dropdown.Options = collectionKeys(cfg)
		dropdown.Refresh()
		setSelected(key)
		refreshTarget()

		if onApply != nil {
			onApply(clone(cfg))
		}
		status.SetText("Saved to " + savePath)
	})

	return container.NewVScroll(container.NewVBox(
		collRow, form, target, clearDBBtn,
		container.NewHBox(testBtn, applyBtn), status,
	))
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
			Namespace:      cfg.Namespace,
			Collection:     cfg.Collection,
			TripleStoreURL: cfg.TripleStoreURL,
			Username:       cfg.Username,
			Password:       cfg.Password,
			Insecure:       cfg.WebserviceInsecure,
			FileHosts:      cfg.DefaultFileHosts,
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
