package tieconfig

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/tielog"
)

// A filehost URL with credentials in it is a leak specific to this stack: the
// apps build stream URLs as baseURL+"/"+contentHash and hand them to libmpv and
// pwplay, so the password ends up in the player's command line and in its log.
func TestHostURLUserinfo(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"http://fh.example:2160", ""},
		{"https://fh.example/tie", ""},
		{"http://media:sekrit@fh.example", "media:sekrit"},
		{"http://media@fh.example", "media"},
		{"http://media:sekrit@fh.example:2160/deep/path", "media:sekrit"},
		{"", ""},
		{"not a url", ""},
		// A colon in the path is not credentials.
		{"http://fh.example/pathwith:colon", ""},
	}
	for _, c := range cases {
		if got := HostURLUserinfo(c.url); got != c.want {
			t.Errorf("HostURLUserinfo(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// A credential-bearing config still loads; it only gets reported. Refusing to
// start would break a working setup over a hazard the user may have chosen on
// purpose.
func TestWarnHostURLUserinfoReportsOnlyLeakyHosts(t *testing.T) {
	var buf bytes.Buffer
	SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { SetLogger(tielog.Discard()) })

	cfg := client.Config{}
	cfg.FileHosts = map[string]client.FileHost{
		"clean": {URL: "http://fh.example"},
		"leaky": {URL: "http://u:p@fh.example"},
	}
	WarnHostURLUserinfo(cfg)

	logged := buf.String()
	if !strings.Contains(logged, "host=leaky") {
		t.Errorf("the credential-bearing host was not reported:\n%s", logged)
	}
	if !strings.Contains(logged, "userinfo=u:p") {
		t.Errorf("the record does not say which credentials:\n%s", logged)
	}
	if strings.Contains(logged, "clean") {
		t.Errorf("a clean host URL was reported:\n%s", logged)
	}
}
