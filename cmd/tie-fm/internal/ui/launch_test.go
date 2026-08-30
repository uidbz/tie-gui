package ui

import (
	"reflect"
	"testing"
)

func TestBuildCommand(t *testing.T) {
	cases := []struct {
		cmdline string
		file    string
		want    []string // program + args, nil = nil cmd
	}{
		{"mpv", "/tmp/a.mkv", []string{"mpv", "/tmp/a.mkv"}},              // path appended
		{"mpv %f", "/tmp/a.mkv", []string{"mpv", "/tmp/a.mkv"}},           // %f replaced
		{"foo --flag %f --loop", "/x", []string{"foo", "--flag", "/x", "--loop"}}, // %f mid-args, no append
		{"", "/x", nil},
	}
	for _, c := range cases {
		cmd := buildCommand(c.cmdline, c.file)
		if c.want == nil {
			if cmd != nil {
				t.Errorf("buildCommand(%q) = %v, want nil", c.cmdline, cmd.Args)
			}
			continue
		}
		if cmd == nil {
			t.Errorf("buildCommand(%q) = nil, want %v", c.cmdline, c.want)
			continue
		}
		if !reflect.DeepEqual(cmd.Args, c.want) {
			t.Errorf("buildCommand(%q) args = %v, want %v", c.cmdline, cmd.Args, c.want)
		}
	}
}
