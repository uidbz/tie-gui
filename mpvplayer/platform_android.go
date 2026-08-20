//go:build android && !nompv

package mpvplayer

/*
#cgo LDFLAGS: -lEGL -ldl
#include <stdlib.h>
#include <dlfcn.h>
#include <EGL/egl.h>

static void *gl_proc_address(const char *name) {
    // Prefer symbols already loaded into the process: Fyne's mobile driver
    // links GLESv2/v3, so the core GL entry points are resolvable via dlsym.
    // eglGetProcAddress is only guaranteed to return extension functions on
    // some Android drivers, so fall back to it only when dlsym misses.
    void *p = dlsym(RTLD_DEFAULT, name);
    if (p)
        return p;
    return (void *)eglGetProcAddress(name);
}
*/
import "C"

import "unsafe"

// glProcAddress resolves an OpenGL ES function pointer for mpv's render API.
func glProcAddress(name string) unsafe.Pointer {
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	return unsafe.Pointer(C.gl_proc_address(cn))
}

// nativeDisplay reports no native display on Android: mpv's render context
// takes no X11/Wayland param there. Hardware decode, when enabled, is handled
// by MediaCodec rather than a display-server GPU-interop path.
func nativeDisplay() (int, unsafe.Pointer) { return 0, nil }

// platformHwdec forces software decode on Android. MediaCodec hardware decode
// (hwdec=mediacodec[-copy]) additionally requires the ffmpeg jni bridge to be
// handed the JavaVM via av_jni_set_java_vm, which Fyne's mobile driver does not
// currently expose — enabling it without that fails to initialise the decoder.
func platformHwdec() string { return "no" }

// platformAO uses OpenSL ES, the audio output broadly available across Android
// versions.
func platformAO() string { return "opensles" }
