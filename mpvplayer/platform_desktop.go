//go:build !nompv && !android

package mpvplayer

import (
	"unsafe"

	"github.com/go-gl/glfw/v3.4/glfw"
)

// glProcAddress resolves an OpenGL function pointer for mpv's render API via
// GLFW's loader, which is active for the desktop GL contexts Fyne creates.
func glProcAddress(name string) unsafe.Pointer {
	return glfw.GetProcAddress(name)
}

// platformHwdec is the mpv hardware-decode mode for desktop: auto-safe picks a
// GPU decoder when one is known to interop cleanly, otherwise software.
func platformHwdec() string { return "auto-safe" }

// platformAO returns "" on desktop so mpv chooses its default audio output.
func platformAO() string { return "" }
