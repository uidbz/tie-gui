package tiethumb

import (
	"testing"

	"github.com/uidbz/tie/client"
)

func testConfig() client.Config {
	return client.Config{
		TripleStoreURL:   "http://store:1161",
		Namespace:        "Collections",
		Collection:       "images",
		DefaultFileHosts: []string{"top"},
		FileHosts: map[string]client.FileHost{
			"top":  {URL: "http://top:1162"},
			"coll": {URL: "http://coll:1162"},
		},
		Collections: map[string]client.CollectionEntry{
			"images": {TripleStoreURL: "http://store:1161", FileHosts: []string{"coll"}},
			"plain":  {TripleStoreURL: "http://store:1161"},
		},
	}
}

func TestFileHostUsesActiveCollection(t *testing.T) {
	tc := client.NewTieClientFor(testConfig(), "images")
	if got := FileHost(tc); got.URL != "http://coll:1162" {
		t.Fatalf("FileHost = %q, want the collection's host", got.URL)
	}
}

func TestFileHostFallsBackToTopLevel(t *testing.T) {
	tc := client.NewTieClientFor(testConfig(), "plain")
	if got := FileHost(tc); got.URL != "http://top:1162" {
		t.Fatalf("FileHost = %q, want the top-level default host", got.URL)
	}
}

func TestFileHostPrefersFast(t *testing.T) {
	tc := client.NewTieClientFor(testConfig(), "images")
	cfg := tc.Config
	cfg.FileHosts["fast"] = client.FileHost{URL: "http://fast:1162"}
	tc = client.NewTieClientFor(cfg, "images")
	if got := FileHost(tc); got.URL != "http://fast:1162" {
		t.Fatalf("FileHost = %q, want the fast host", got.URL)
	}
}

func TestFileHostNilClient(t *testing.T) {
	if got := FileHost(nil); got.URL != "" {
		t.Fatalf("FileHost(nil) = %+v, want zero", got)
	}
}
