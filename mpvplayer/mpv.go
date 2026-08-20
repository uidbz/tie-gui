//go:build !nompv

package mpvplayer

/*
#cgo LDFLAGS: -lmpv
#include <mpv/client.h>
#include <mpv/render.h>
#include <mpv/render_gl.h>
#include <stdlib.h>
#include <stdint.h>

// Forward declarations of Go-exported callbacks.
void *goGetProcAddress(void *ctx, char *name);
void goRenderUpdate(void *ctx);

// Trampoline used as mpv's get_proc_address: mpv passes a plain C string; we
// hand it to Go, which resolves it via the platform GL loader (GLFW on
// desktop, EGL/dlsym on Android).
static void *get_proc_address_bridge(void *ctx, const char *name) {
    return goGetProcAddress(ctx, (char *)name);
}

// Build the OpenGL init params param array. Cgo cannot take the address of a C
// function from Go, so we assemble the struct here.
//
// disp_type selects the optional native-display param: 1 = X11, 2 = Wayland, 0 = none.
// proc_ctx is a Go cgo.Handle passed as an integer (uintptr_t), never a Go
// pointer: stuffing a cgo.Handle into a Go unsafe.Pointer that stays live
// across this cgo call trips the Go stack scanner ("bad pointer ... : 0x1")
// and aborts on the GL thread.
static mpv_render_param *make_gl_init_params(uintptr_t proc_ctx, int disp_type, void *disp) {
    mpv_opengl_init_params *gl = calloc(1, sizeof(mpv_opengl_init_params));
    gl->get_proc_address = get_proc_address_bridge;
    gl->get_proc_address_ctx = (void *)proc_ctx;

    mpv_render_param *params = calloc(5, sizeof(mpv_render_param));
    params[0].type = MPV_RENDER_PARAM_API_TYPE;
    params[0].data = MPV_RENDER_API_TYPE_OPENGL;
    params[1].type = MPV_RENDER_PARAM_OPENGL_INIT_PARAMS;
    params[1].data = gl;
    static int yes = 1;
    params[2].type = MPV_RENDER_PARAM_ADVANCED_CONTROL;
    params[2].data = &yes;

    int idx = 3;
    if (disp != NULL) {
        if (disp_type == 1) {
            params[idx].type = MPV_RENDER_PARAM_X11_DISPLAY;
            params[idx].data = disp;
            idx++;
        } else if (disp_type == 2) {
            params[idx].type = MPV_RENDER_PARAM_WL_DISPLAY;
            params[idx].data = disp;
            idx++;
        }
    }
    params[idx].type = 0;
    params[idx].data = NULL;
    return params;
}

static void free_gl_init_params(mpv_render_param *params) {
    if (!params) return;
    if (params[1].data) free(params[1].data);
    free(params);
}

// Render the current frame into the given FBO.
static int render_to_fbo(mpv_render_context *ctx, int fbo, int w, int h) {
    mpv_opengl_fbo target;
    target.fbo = fbo;
    target.w = w;
    target.h = h;
    target.internal_format = 0;

    int flip_y = 0;
    mpv_render_param params[3];
    params[0].type = MPV_RENDER_PARAM_OPENGL_FBO;
    params[0].data = &target;
    params[1].type = MPV_RENDER_PARAM_FLIP_Y;
    params[1].data = &flip_y;
    params[2].type = 0;
    params[2].data = NULL;
    return mpv_render_context_render(ctx, params);
}

static void set_update_callback(mpv_render_context *ctx, uintptr_t go_ctx) {
    mpv_render_context_set_update_callback(ctx, goRenderUpdate, (void *)go_ctx);
}
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"
)

// MPVPlayer drives a libmpv instance and renders its video into an OpenGL FBO
// via the Render API. It implements canvas.GLVideoRenderer.
type MPVPlayer struct {
	mpv    *C.mpv_handle
	render *C.mpv_render_context

	initOnce sync.Once
	initErr  error

	needsPaint atomic.Bool
	onUpdate   func()

	self cgo.Handle
	file string

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// NewMPVPlayer creates a new libmpv player that will play file (path or URL).
func NewMPVPlayer(file string) (*MPVPlayer, error) {
	h := C.mpv_create()
	if h == nil {
		return nil, fmt.Errorf("mpv_create failed")
	}
	if err := checkMPV(C.mpv_initialize(h)); err != nil {
		C.mpv_terminate_destroy(h)
		return nil, err
	}
	setOption(h, "vo", "libmpv")
	setOption(h, "hwdec", platformHwdec())
	if ao := platformAO(); ao != "" {
		setOption(h, "ao", ao)
	}

	p := &MPVPlayer{mpv: h, file: file, stop: make(chan struct{}), done: make(chan struct{})}
	p.self = cgo.NewHandle(p)
	go p.eventLoop()
	return p, nil
}

func (p *MPVPlayer) eventLoop() {
	defer close(p.done)
	for {
		select {
		case <-p.stop:
			return
		default:
		}
		ev := C.mpv_wait_event(p.mpv, C.double(0.1))
		if ev == nil {
			continue
		}
		switch ev.event_id {
		case C.MPV_EVENT_NONE:
		case C.MPV_EVENT_SHUTDOWN:
			return
		}
	}
}

func (p *MPVPlayer) SetOnUpdate(fn func()) { p.onUpdate = fn }

func (p *MPVPlayer) ensureRender() error {
	p.initOnce.Do(func() {
		// Keep the cgo.Handle in a C scalar (uintptr_t); storing it in a Go
		// unsafe.Pointer across the cgo call below aborts on the GL thread.
		handle := C.uintptr_t(p.self)
		dispType, disp := nativeDisplay()
		params := C.make_gl_init_params(handle, C.int(dispType), disp)
		defer C.free_gl_init_params(params)

		var rctx *C.mpv_render_context
		if err := checkMPV(C.mpv_render_context_create(&rctx, p.mpv, params)); err != nil {
			p.initErr = err
			return
		}
		p.render = rctx
		C.set_update_callback(rctx, handle)
		p.command("loadfile", p.file)
	})
	return p.initErr
}

func (p *MPVPlayer) RenderInto(fbo uint32, width, height int) {
	if err := p.ensureRender(); err != nil {
		return
	}
	p.needsPaint.Store(false)
	C.render_to_fbo(p.render, C.int(fbo), C.int(width), C.int(height))
}

func (p *MPVPlayer) NeedsPaint() bool { return p.needsPaint.Load() }

func (p *MPVPlayer) Aspect() float32 {
	a, err := p.getPropertyDouble("video-params/aspect")
	if err != nil || a <= 0 {
		return 0
	}
	return float32(a)
}

func (p *MPVPlayer) Play()  { p.setPropertyFlag("pause", false) }
func (p *MPVPlayer) Pause() { p.setPropertyFlag("pause", true) }

func (p *MPVPlayer) TogglePause() bool {
	paused := !p.IsPaused()
	p.setPropertyFlag("pause", paused)
	return paused
}

func (p *MPVPlayer) IsPaused() bool {
	v, err := p.getPropertyFlag("pause")
	return err == nil && v
}

func (p *MPVPlayer) Position() float64 {
	v, _ := p.getPropertyDouble("time-pos")
	return v
}

func (p *MPVPlayer) Duration() float64 {
	v, _ := p.getPropertyDouble("duration")
	return v
}

func (p *MPVPlayer) SeekTo(seconds float64) {
	p.command("seek", strconv.FormatFloat(seconds, 'f', 3, 64), "absolute")
}

func (p *MPVPlayer) Close() {
	if p.stop != nil {
		p.stopOnce.Do(func() { close(p.stop) })
		<-p.done
	}
	if p.render != nil {
		C.mpv_render_context_free(p.render)
		p.render = nil
	}
	if p.mpv != nil {
		C.mpv_terminate_destroy(p.mpv)
		p.mpv = nil
	}
	if p.self != 0 {
		p.self.Delete()
		p.self = 0
	}
}

func (p *MPVPlayer) getPropertyDouble(name string) (float64, error) {
	if p.mpv == nil {
		return 0, fmt.Errorf("mpv closed")
	}
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	var val C.double
	if err := checkMPV(C.mpv_get_property(p.mpv, cn, C.MPV_FORMAT_DOUBLE, unsafe.Pointer(&val))); err != nil {
		return 0, err
	}
	return float64(val), nil
}

func (p *MPVPlayer) getPropertyFlag(name string) (bool, error) {
	if p.mpv == nil {
		return false, fmt.Errorf("mpv closed")
	}
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	var val C.int
	if err := checkMPV(C.mpv_get_property(p.mpv, cn, C.MPV_FORMAT_FLAG, unsafe.Pointer(&val))); err != nil {
		return false, err
	}
	return val != 0, nil
}

func (p *MPVPlayer) setPropertyFlag(name string, value bool) {
	if p.mpv == nil {
		return
	}
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	var val C.int
	if value {
		val = 1
	}
	C.mpv_set_property(p.mpv, cn, C.MPV_FORMAT_FLAG, unsafe.Pointer(&val))
}

func (p *MPVPlayer) command(args ...string) {
	cargs := make([]*C.char, len(args)+1)
	for i, a := range args {
		cargs[i] = C.CString(a)
	}
	cargs[len(args)] = nil
	C.mpv_command(p.mpv, &cargs[0])
	for i := range args {
		C.free(unsafe.Pointer(cargs[i]))
	}
}

// setOption and checkMPV are package-level helpers used by both the player
// and the screenshot code.
func setOption(h *C.mpv_handle, name, value string) {
	cn := C.CString(name)
	cv := C.CString(value)
	defer C.free(unsafe.Pointer(cn))
	defer C.free(unsafe.Pointer(cv))
	C.mpv_set_option_string(h, cn, cv)
}

func checkMPV(status C.int) error {
	if status >= 0 {
		return nil
	}
	return fmt.Errorf("mpv error: %s", C.GoString(C.mpv_error_string(status)))
}

// mpvCommand is a package-level command helper used by screenshot.go.
func mpvCommand(h *C.mpv_handle, args ...string) {
	cargs := make([]*C.char, len(args)+1)
	for i, a := range args {
		cargs[i] = C.CString(a)
	}
	cargs[len(args)] = nil
	C.mpv_command(h, &cargs[0])
	for i := range args {
		C.free(unsafe.Pointer(cargs[i]))
	}
}

//export goGetProcAddress
func goGetProcAddress(ctx unsafe.Pointer, name *C.char) unsafe.Pointer {
	return glProcAddress(C.GoString(name))
}

//export goRenderUpdate
func goRenderUpdate(ctx unsafe.Pointer) {
	p := cgo.Handle(uintptr(ctx)).Value().(*MPVPlayer)
	p.needsPaint.Store(true)
	if p.onUpdate != nil {
		p.onUpdate()
	}
}
