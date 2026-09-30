package data

import (
	"testing"

	"github.com/uidbz/tie/client"
)

// An exclude-only tag selection filters the latest-albums listing by the
// rows' expanded tag attributes.
func TestHasAnyTag(t *testing.T) {
	row := client.Row{Attributes: map[string][]string{"tag": {"rock", "live"}}}
	if !hasAnyTag(row, []string{"jazz", "live"}) {
		t.Error("row tagged live not matched by an exclude of live")
	}
	if hasAnyTag(row, []string{"jazz"}) {
		t.Error("row matched an exclude tag it does not carry")
	}
	if hasAnyTag(client.Row{}, []string{"rock"}) {
		t.Error("untagged row matched")
	}
}
