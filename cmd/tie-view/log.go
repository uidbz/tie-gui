package main

import (
	"github.com/uidbz/tie-gui/tielog"
)

// log is tie-view's logger: tie queries that failed, tag writes that were
// rejected, archives that would not open, thumbnails that could not be
// uploaded. main replaces the discard default with the app logger from
// tielog.Setup, which also writes $XDG_STATE_HOME/tie/tie-view.log.
//
// The messages used to be fmt.Printf calls to stdout, which meant they were
// invisible when the app was launched from a desktop entry and absent entirely
// on Android.
var log = tielog.Discard()
