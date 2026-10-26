// Package tietest supplies tie client configuration for tests that run
// against the external test environment (tie/test-env/start.sh).
package tietest

import (
	"os"

	"github.com/uidbz/tie/client"
)

// Config returns client.TestingConfig with the credentials the test-env's
// generated triplestore config requires. tie v0.7 removed the test-env
// credentials from the client's default config, so a test that talks to the
// external triplestore has to supply them itself.
func Config() client.Config {
	cfg := client.TestingConfig()
	cfg.Username = envOr("TIE_TEST_USERNAME", "defaultuser")
	cfg.Password = envOr("TIE_TEST_PASSWORD", "defaultpassword")
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
