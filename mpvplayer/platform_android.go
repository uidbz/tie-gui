//go:build android && !nompv

package mpvplayer

/*
#cgo LDFLAGS: -lEGL -ldl -lavcodec
#include <stdlib.h>
#include <dlfcn.h>
#include <EGL/egl.h>

// av_jni_set_java_vm lives in the vendored libavcodec; ffmpeg's headers are not
// shipped, so declare the prototype directly. Registering the process JavaVM is
// a prerequisite for MediaCodec hardware decoding (h264_mediacodec) and for the
// render API's MediaCodec hwdec interop — without it the decoder init fails with
// "No Java virtual machine has been registered".
extern int av_jni_set_java_vm(void *vm, void *log_ctx);

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

static int set_java_vm(void *vm) {
    return av_jni_set_java_vm(vm, NULL);
}
*/
import "C"

import (
	"sync"
	"unsafe"

	"fyne.io/fyne/v2/driver"
)

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

// platformHwdec uses MediaCodec hardware decoding in copy-back mode: frames are
// decoded on the GPU/DSP then copied to system memory, so the libmpv render VO
// uploads them through the same texture path as software frames (no Surface or
// zero-copy interop needed). Requires registerJavaVM to have run first.
func platformHwdec() string { return "mediacodec-copy" }

var javaVMOnce sync.Once

// registerJavaVM hands the process JavaVM to ffmpeg so the MediaCodec decoder
// can attach to it. driver.RunNative surfaces the JVM pointer that Fyne's mobile
// driver captured in JNI_OnLoad. Idempotent; safe to call from any goroutine.
func registerJavaVM() {
	javaVMOnce.Do(func() {
		driver.RunNative(func(ctx any) error {
			if ac, ok := ctx.(*driver.AndroidContext); ok && ac.VM != 0 {
				C.set_java_vm(unsafe.Pointer(ac.VM))
			}
			return nil
		})
	})
}

// platformAO uses OpenSL ES, the audio output broadly available across Android
// versions.
func platformAO() string { return "opensles" }
