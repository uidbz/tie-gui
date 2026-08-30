package main

import (
	"encoding/json"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"
)

const (
	prefKeyProfiles      = "tie.profiles"
	prefKeyActiveProfile = "tie.activeProfile"

	// Legacy flat preference keys, kept only for one-time migration when no
	// profiles have been saved yet.
	prefKeyWebservice    = "tie.webservice"
	prefKeyNamespace     = "tie.namespace"
	prefKeyCollection    = "tie.collection"
	prefKeyFilehostName  = "tie.filehost.name"
	prefKeyFilehostURL   = "tie.filehost.url"
	prefKeyFilehostInsec = "tie.filehost.insecure"
)

// profile holds all connection settings for one named configuration.
type profile struct {
	Name             string `json:"name"`
	Webservice       string `json:"webservice"`
	Namespace        string `json:"namespace"`
	Collection       string `json:"collection"`
	FilehostName     string `json:"filehostName"`
	FilehostURL      string `json:"filehostURL"`
	FilehostInsecure bool   `json:"filehostInsecure"`
}

// loadProfiles deserialises the profile list from Preferences.
// Returns nil when no profiles have been saved yet.
func loadProfiles(p fyne.Preferences) []profile {
	raw := p.String(prefKeyProfiles)
	if raw == "" {
		return nil
	}
	var profiles []profile
	if err := json.Unmarshal([]byte(raw), &profiles); err != nil {
		return nil
	}
	return profiles
}

// saveProfiles serialises the profile list into Preferences.
func saveProfiles(p fyne.Preferences, profiles []profile) {
	b, _ := json.Marshal(profiles)
	p.SetString(prefKeyProfiles, string(b))
}

// profileNames returns just the Name field of each profile.
func profileNames(profiles []profile) []string {
	names := make([]string, len(profiles))
	for i, pr := range profiles {
		names[i] = pr.Name
	}
	return names
}

// findProfile returns the profile with the given name, if it exists.
func findProfile(profiles []profile, name string) (profile, bool) {
	for _, pr := range profiles {
		if pr.Name == name {
			return pr, true
		}
	}
	return profile{}, false
}

// profileFromTC snapshots the live client state into a profile struct.
func profileFromTC(name string, tc *client.TieClient) profile {
	host := tieFileHost(tc)
	return profile{
		Name:             name,
		Webservice:       tc.Config.Webservice,
		Namespace:        tc.Config.Namespace,
		Collection:       tc.Config.Collection,
		FilehostName:     currentHostName(tc),
		FilehostURL:      host.URL,
		FilehostInsecure: host.Insecure,
	}
}

// applyProfileToConfig writes a profile's values into cfg. Fields that are
// empty in the profile are left unchanged so that a partially filled profile
// doesn't clobber values from the TOML config file.
func applyProfileToConfig(pr profile, cfg *client.Config) {
	if pr.Webservice != "" {
		cfg.Webservice = pr.Webservice
	}
	if pr.Namespace != "" {
		cfg.Namespace = pr.Namespace
	}
	if pr.Collection != "" {
		cfg.Collection = pr.Collection
	}
	if pr.FilehostName != "" && pr.FilehostURL != "" {
		if cfg.FileHosts == nil {
			cfg.FileHosts = make(map[string]client.FileHost)
		}
		cfg.FileHosts[pr.FilehostName] = client.FileHost{
			URL:      pr.FilehostURL,
			Insecure: pr.FilehostInsecure,
		}
		cfg.DefaultFileHosts = prependUnique(pr.FilehostName, cfg.DefaultFileHosts)
	}
}

// applyTiePrefs overlays saved settings onto cfg at startup. It prefers the
// profile system; if no profiles exist yet it falls back to the legacy flat
// preference keys so that existing installations continue to work.
func applyTiePrefs(a fyne.App, cfg *client.Config) {
	p := a.Preferences()
	profiles := loadProfiles(p)

	if len(profiles) > 0 {
		// Profile system: apply the last-active profile.
		activeName := p.StringWithFallback(prefKeyActiveProfile, profiles[0].Name)
		active, ok := findProfile(profiles, activeName)
		if !ok {
			active = profiles[0]
		}
		applyProfileToConfig(active, cfg)
		return
	}

	// Legacy flat keys: migrate opportunistically.
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

// makeSettingsTab returns an AppTabs tab item with named connection profiles.
// A dropdown at the top selects the active profile; "+" / "-" buttons add and
// delete profiles. Clicking Apply saves the current form values under the
// profile name shown in the form, applies them to the live client immediately,
// persists them to Preferences, and calls onApply (if non-nil) so callers can
// react — e.g. to reload the tag list from the newly active server.
func makeSettingsTab(a fyne.App, tc *client.TieClient, onApply func()) *container.TabItem {
	p := a.Preferences()

	// Load saved profiles, creating a default one from the live client if none
	// exist yet (first run, or after clearing Preferences).
	profiles := loadProfiles(p)
	if len(profiles) == 0 {
		profiles = []profile{profileFromTC("Default", tc)}
		saveProfiles(p, profiles)
		p.SetString(prefKeyActiveProfile, profiles[0].Name)
	}
	activeName := p.StringWithFallback(prefKeyActiveProfile, profiles[0].Name)
	if _, ok := findProfile(profiles, activeName); !ok {
		activeName = profiles[0].Name
	}

	// Form fields.
	profileName := widget.NewEntry()
	daemonURL := widget.NewEntry()
	daemonURL.SetPlaceHolder("http://localhost:1161")
	namespace := widget.NewEntry()
	namespace.SetPlaceHolder("Collections")
	collection := widget.NewEntry()
	collection.SetPlaceHolder("Main")
	hostName := widget.NewEntry()
	hostName.SetPlaceHolder("fast")
	hostURL := widget.NewEntry()
	hostURL.SetPlaceHolder("http://localhost:1162")
	hostInsecure := widget.NewCheck("", nil)
	status := widget.NewLabel("")

	// loadIntoForm populates all form fields from a profile.
	loadIntoForm := func(pr profile) {
		profileName.SetText(pr.Name)
		daemonURL.SetText(pr.Webservice)
		namespace.SetText(pr.Namespace)
		collection.SetText(pr.Collection)
		hostName.SetText(pr.FilehostName)
		hostURL.SetText(pr.FilehostURL)
		hostInsecure.SetChecked(pr.FilehostInsecure)
	}

	// Dropdown: switching profiles loads their values into the form but does
	// NOT apply them to the live client — the user must click Apply.
	dropdown := widget.NewSelect(profileNames(profiles), nil)
	dropdown.SetSelected(activeName)
	if pr, ok := findProfile(profiles, activeName); ok {
		loadIntoForm(pr)
	}
	dropdown.OnChanged = func(name string) {
		if pr, ok := findProfile(profiles, name); ok {
			loadIntoForm(pr)
		}
		p.SetString(prefKeyActiveProfile, name)
		status.SetText("")
	}

	// "+" button: add a new profile pre-filled with the current form values.
	addBtn := widget.NewButton("+", func() {
		base := "New profile"
		name := base
		for i := 2; ; i++ {
			if _, exists := findProfile(profiles, name); !exists {
				break
			}
			name = fmt.Sprintf("%s %d", base, i)
		}
		newPr := profile{
			Name:             name,
			Webservice:       daemonURL.Text,
			Namespace:        namespace.Text,
			Collection:       collection.Text,
			FilehostName:     hostName.Text,
			FilehostURL:      hostURL.Text,
			FilehostInsecure: hostInsecure.Checked,
		}
		profiles = append(profiles, newPr)
		saveProfiles(p, profiles)
		dropdown.Options = profileNames(profiles)
		dropdown.Refresh()
		dropdown.SetSelected(name) // triggers OnChanged → loadIntoForm + pref save
		status.SetText("Profile created.")
	})

	// "-" button: delete the currently selected profile.
	delBtn := widget.NewButton("-", func() {
		if len(profiles) <= 1 {
			status.SetText("Cannot delete the only profile.")
			return
		}
		sel := dropdown.Selected
		for i, pr := range profiles {
			if pr.Name == sel {
				profiles = append(profiles[:i], profiles[i+1:]...)
				break
			}
		}
		saveProfiles(p, profiles)
		dropdown.Options = profileNames(profiles)
		dropdown.Refresh()
		dropdown.SetSelected(profiles[0].Name) // triggers OnChanged → loadIntoForm
		status.SetText("Profile deleted.")
	})

	profileRow := container.NewBorder(
		nil, nil, nil,
		container.NewHBox(addBtn, delBtn),
		dropdown,
	)

	form := widget.NewForm(
		widget.NewFormItem("Profile name", profileName),
		widget.NewFormItem("Daemon URL", daemonURL),
		widget.NewFormItem("Namespace", namespace),
		widget.NewFormItem("Collection", collection),
		widget.NewFormItem("Filehost name", hostName),
		widget.NewFormItem("Filehost URL", hostURL),
		widget.NewFormItem("Skip TLS verify", hostInsecure),
	)

	applyBtn := widget.NewButton("Apply", func() {
		oldName := dropdown.Selected
		newName := profileName.Text
		if newName == "" {
			newName = oldName
		}

		pr := profile{
			Name:             newName,
			Webservice:       daemonURL.Text,
			Namespace:        namespace.Text,
			Collection:       collection.Text,
			FilehostName:     hostName.Text,
			FilehostURL:      hostURL.Text,
			FilehostInsecure: hostInsecure.Checked,
		}

		// Update or rename the profile in the list.
		found := false
		for i, existing := range profiles {
			if existing.Name == oldName {
				profiles[i] = pr
				found = true
				break
			}
		}
		if !found {
			profiles = append(profiles, pr)
		}
		saveProfiles(p, profiles)

		// Refresh dropdown options in case of a rename.
		dropdown.Options = profileNames(profiles)
		dropdown.Refresh()
		dropdown.SetSelected(newName)
		p.SetString(prefKeyActiveProfile, newName)

		// Apply to live client immediately.
		applyProfileToConfig(pr, &tc.Config)
		*tc = *client.NewTieClient(tc.Config)

		if onApply != nil {
			onApply()
		}

		status.SetText("Saved.")
	})

	content := container.NewVScroll(container.NewVBox(profileRow, form, applyBtn, status))
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
