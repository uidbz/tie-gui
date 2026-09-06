package tieconfig

import "github.com/uidbz/tie/client"

// AppCollection resolves which [Collections] entry name an app should bind at
// startup. The tie config file is shared by every app (and the tie CLI), so
// its DefaultCollection cannot be per-app: each GUI app stores its own
// selection and has its own default profile name, so e.g. images and sound
// can live in different profiles without the apps fighting over the shared
// default. Resolution order: the app's stored selection when it still names
// an entry, then the app's default profile name when the config has such an
// entry, then the config's DefaultCollection (which may be empty — the client
// then falls back to the flat top-level fields).
func AppCollection(cfg client.Config, stored, appDefault string) string {
	if stored != "" {
		if _, ok := cfg.Collections[stored]; ok {
			return stored
		}
	}
	if appDefault != "" {
		if _, ok := cfg.Collections[appDefault]; ok {
			return appDefault
		}
	}
	return cfg.DefaultCollection
}
