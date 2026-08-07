package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"git.sr.ht/~uid/tie/client"
)

const (
	prefKeyWebservice    = "tie.webservice"
	prefKeyNamespace     = "tie.namespace"
	prefKeyCollection    = "tie.collection"
	prefKeyFilehostName  = "tie.filehost.name"
	prefKeyFilehostURL   = "tie.filehost.url"
	prefKeyFilehostInsec = "tie.filehost.insecure"
)

// applyTiePrefs overlays values saved in app Preferences onto cfg.
// Call after loadTieConfig so that user settings take effect on all platforms,
// including Android where no on-disk config file is available.
func applyTiePrefs(a fyne.App, cfg *client.Config) {
	p := a.Preferences()
	if v := p.String(prefKeyWebservice); v != "" {
		cfg.Webservice = v
	}
	if v := p.String(prefKeyNamespace); v != "" {
		cfg.Namespace = v
	}
	if v := p.String(prefKeyCollection); v != "" {
		cfg.Collection = v
	}
	name := p.String(prefKeyFilehostName)
	url := p.String(prefKeyFilehostURL)
	if name != "" && url != "" {
		h := client.FileHost{
			URL:      url,
			Insecure: p.Bool(prefKeyFilehostInsec),
		}
		if cfg.FileHosts == nil {
			cfg.FileHosts = make(map[string]client.FileHost)
		}
		cfg.FileHosts[name] = h
		cfg.DefaultFileHosts = prependUnique(name, cfg.DefaultFileHosts)
	}
}

// makeSettingsTab returns an AppTabs tab item with a form for editing the tie
// daemon URL and active filehost. Changes are applied to the live client
// immediately and persisted via fyne Preferences (works on Android).
func makeSettingsTab(a fyne.App, tc *client.TieClient) *container.TabItem {
	p := a.Preferences()

	activeHost := tieFileHost(tc)
	activeName := p.StringWithFallback(prefKeyFilehostName, currentHostName(tc))

	daemonURL := widget.NewEntry()
	daemonURL.SetPlaceHolder("http://localhost:1161")
	daemonURL.SetText(p.StringWithFallback(prefKeyWebservice, tc.Config.Webservice))

	namespace := widget.NewEntry()
	namespace.SetPlaceHolder("Collections")
	namespace.SetText(p.StringWithFallback(prefKeyNamespace, tc.Config.Namespace))

	collection := widget.NewEntry()
	collection.SetPlaceHolder("Main")
	collection.SetText(p.StringWithFallback(prefKeyCollection, tc.Config.Collection))

	hostName := widget.NewEntry()
	hostName.SetPlaceHolder("fast")
	hostName.SetText(activeName)

	hostURL := widget.NewEntry()
	hostURL.SetPlaceHolder("http://localhost:1162")
	hostURL.SetText(p.StringWithFallback(prefKeyFilehostURL, activeHost.URL))

	hostInsecure := widget.NewCheck("", nil)
	hostInsecure.SetChecked(p.BoolWithFallback(prefKeyFilehostInsec, activeHost.Insecure))

	status := widget.NewLabel("")

	form := widget.NewForm(
		widget.NewFormItem("Daemon URL", daemonURL),
		widget.NewFormItem("Namespace", namespace),
		widget.NewFormItem("Collection", collection),
		widget.NewFormItem("Filehost name", hostName),
		widget.NewFormItem("Filehost URL", hostURL),
		widget.NewFormItem("Skip TLS verify", hostInsecure),
	)

	applyBtn := widget.NewButton("Apply", func() {
		daemon := daemonURL.Text
		ns := namespace.Text
		coll := collection.Text
		name := hostName.Text
		url := hostURL.Text
		insecure := hostInsecure.Checked

		// Persist to Preferences.
		p.SetString(prefKeyWebservice, daemon)
		p.SetString(prefKeyNamespace, ns)
		p.SetString(prefKeyCollection, coll)
		p.SetString(prefKeyFilehostName, name)
		p.SetString(prefKeyFilehostURL, url)
		p.SetBool(prefKeyFilehostInsec, insecure)

		// Update the live client so the change takes effect immediately
		// for all subsequent tie requests.
		tc.Config.Webservice = daemon
		tc.Config.Namespace = ns
		tc.Config.Collection = coll
		if tc.Config.FileHosts == nil {
			tc.Config.FileHosts = make(map[string]client.FileHost)
		}
		tc.Config.FileHosts[name] = client.FileHost{URL: url, Insecure: insecure}
		tc.Config.DefaultFileHosts = prependUnique(name, tc.Config.DefaultFileHosts)

		status.SetText("Saved.")
	})

	content := container.NewVScroll(container.NewVBox(form, applyBtn, status))
	return container.NewTabItem("Settings", content)
}

// currentHostName returns the name of the currently active filehost in tc,
// using the same priority order as tieFileHost.
func currentHostName(tc *client.TieClient) string {
	if tieHostName != "" {
		return tieHostName
	}
	if _, ok := tc.Config.FileHosts["fast"]; ok {
		return "fast"
	}
	if len(tc.Config.DefaultFileHosts) > 0 {
		return tc.Config.DefaultFileHosts[0]
	}
	return "default"
}

// prependUnique returns ss with name at the front, removing any duplicate.
func prependUnique(name string, ss []string) []string {
	out := make([]string, 0, len(ss)+1)
	out = append(out, name)
	for _, s := range ss {
		if s != name {
			out = append(out, s)
		}
	}
	return out
}
