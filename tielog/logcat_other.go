//go:build !android

package tielog

// androidLog is nil off Android: there is no logcat to mirror to, and Setup
// then leaves that sink out entirely.
var androidLog func(tag, msg string)
