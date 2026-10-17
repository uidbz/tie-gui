package main

import (
	"github.com/uidbz/tie-gui/tielog"
)

// log is imgview's logger. main replaces the discard default with the app
// logger from tielog.Setup, which also writes $XDG_STATE_HOME/tie/imgview.log.
var log = tielog.Discard()
