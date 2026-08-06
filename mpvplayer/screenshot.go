//go:build !nompv && !android

package mpvplayer

/*
#cgo LDFLAGS: -lmpv
#include <mpv/client.h>
#include <mpv/render.h>
#include <stdlib.h>

// create_sw_rctx creates a software render context for vo=libmpv.
// No OpenGL context is required; mpv renders directly into a memory buffer.
static mpv_render_context *create_sw_rctx(mpv_handle *h) {
    mpv_render_param params[] = {
        {MPV_RENDER_PARAM_API_TYPE, MPV_RENDER_API_TYPE_SW},
        {0, NULL}
    };
    mpv_render_context *rctx = NULL;
    if (mpv_render_context_create(&rctx, h, params) < 0) {
        return NULL;
    }
    return rctx;
}

// sw_render renders the current video frame into buf (RGBA, row-major).
// buf must be at least w*h*4 bytes.
static int sw_render(mpv_render_context *rctx, int w, int h, void *buf) {
    size_t stride = (size_t)w * 4;
    int sz[2] = {w, h};
    mpv_render_param params[] = {
        {MPV_RENDER_PARAM_SW_SIZE, sz},
        {MPV_RENDER_PARAM_SW_FORMAT, "rgba"},
        {MPV_RENDER_PARAM_SW_STRIDE, &stride},
        {MPV_RENDER_PARAM_SW_POINTER, buf},
        {0, NULL}
    };
    return mpv_render_context_render(rctx, params);
}

// sw_has_frame returns non-zero when a new video frame is available.
static int sw_has_frame(mpv_render_context *rctx) {
    return (int)(mpv_render_context_update(rctx) & MPV_RENDER_UPDATE_FRAME);
}
*/
import "C"

import (
	"image"
	"io"
	"os"
	"strconv"
	"time"
	"unsafe"
)

// ExtractFrame returns a single video frame at timestamp seconds using
// libmpv's software renderer. pathOrURL may be a local file path or an
// HTTP/HTTPS URL — libmpv handles both. maxWidth and maxHeight are upper
// bounds; the actual render dimensions respect the video's aspect ratio.
// Returns nil on any failure (mpv unavailable, file unreadable, timeout).
func ExtractFrame(pathOrURL string, maxWidth, maxHeight int, timestamp float64) image.Image {
	h := C.mpv_create()
	if h == nil {
		return nil
	}
	defer C.mpv_terminate_destroy(h)

	setOption(h, "vo", "libmpv")
	setOption(h, "hwdec", "no") // force software decode; works on any system
	setOption(h, "ao", "null")  // discard audio
	if C.mpv_initialize(h) < 0 {
		return nil
	}

	rctx := C.create_sw_rctx(h)
	if rctx == nil {
		return nil
	}
	defer C.mpv_render_context_free(rctx)

	// Drain mpv events in a goroutine — mandatory to keep the pipeline
	// flowing (without consumers, mpv's internal queue fills and stalls).
	fileStarted := make(chan struct{}, 1)
	go func() {
		for {
			ev := C.mpv_wait_event(h, 0.05)
			if ev == nil || ev.event_id == C.MPV_EVENT_SHUTDOWN {
				return
			}
			// MPV_EVENT_START_FILE fires when mpv has opened and begun
			// decoding the file; it is safe to seek after this point.
			if ev.event_id == C.MPV_EVENT_START_FILE {
				select {
				case fileStarted <- struct{}{}:
				default:
				}
			}
		}
	}()

	// Load the file/URL.
	mpvCommand(h, "loadfile", pathOrURL)

	// Wait until mpv has opened the file before seeking.
	select {
	case <-fileStarted:
	case <-time.After(10 * time.Second):
		return nil
	}

	// Seek to the requested timestamp.
	if timestamp > 0 {
		mpvCommand(h, "seek",
			strconv.FormatFloat(timestamp, 'f', 3, 64),
			"absolute+keyframes")
	}

	// Poll until a rendered frame is available (up to 5 s after seek).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if C.sw_has_frame(rctx) != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Determine render dimensions from the video's display aspect ratio so
	// we get the correct shape without letterboxing/pillarboxing.
	renderW, renderH := maxWidth, maxHeight
	cname := C.CString("video-params/aspect")
	var aspect C.double
	if C.mpv_get_property(h, cname, C.MPV_FORMAT_DOUBLE, unsafe.Pointer(&aspect)) == 0 &&
		float64(aspect) > 0 {
		renderH = int(float64(maxWidth) / float64(aspect))
		if renderH > maxHeight {
			renderH = maxHeight
			renderW = int(float64(maxHeight) * float64(aspect))
		}
	}
	C.free(unsafe.Pointer(cname))

	// Render the frame into an RGBA buffer.
	buf := make([]byte, renderW*renderH*4)
	if C.sw_render(rctx, C.int(renderW), C.int(renderH), unsafe.Pointer(&buf[0])) < 0 {
		return nil
	}

	img := image.NewNRGBA(image.Rect(0, 0, renderW, renderH))
	copy(img.Pix, buf)
	return img
}

// ExtractFrameFromReader writes r to a temporary file then calls ExtractFrame.
// Use this when you have a downloaded video blob but no URL.
func ExtractFrameFromReader(r io.ReadSeeker, maxWidth, maxHeight int, timestamp float64) image.Image {
	tmp, err := os.CreateTemp("", "imgview-vid-*")
	if err != nil {
		return nil
	}
	defer os.Remove(tmp.Name())

	var copyErr error
	if _, err2 := r.Seek(0, io.SeekStart); err2 == nil {
		_, copyErr = io.Copy(tmp, r)
	}
	tmp.Close()
	if copyErr != nil {
		return nil
	}

	return ExtractFrame(tmp.Name(), maxWidth, maxHeight, timestamp)
}
