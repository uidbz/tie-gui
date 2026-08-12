package gallery

import "fyne.io/fyne/v2"

// Platform encapsulates platform-specific behavior (mobile vs desktop) to
// centralize the scattered IsMobile() checks. This is a runtime seam: the
// codebase remains unified, but platform-dependent logic is consolidated here
// instead of ~10 sites across gallery and the mains.
//
// Create via NewPlatform(); it automatically detects the current device.
type Platform struct {
	isMobile bool
}

// NewPlatform creates a Platform instance that detects the current device type.
// In test environments where no Fyne app is running, it defaults to desktop.
func NewPlatform() *Platform {
	var isMobile bool
	// fyne.CurrentDevice() panics when no app is running (test environment).
	// Catch the panic and default to desktop behavior for tests.
	defer func() {
		if recover() != nil {
			isMobile = false
		}
	}()
	isMobile = fyne.CurrentDevice().IsMobile()
	return &Platform{
		isMobile: isMobile,
	}
}

// IsMobile returns true on mobile devices (Android, iOS).
func (p *Platform) IsMobile() bool {
	return p.isMobile
}

// ShouldFocusImageView returns true when the image view should be focused for
// keyboard navigation. On mobile, focusing the view summons the soft keyboard,
// so we skip it.
func (p *Platform) ShouldFocusImageView() bool {
	return !p.isMobile
}

// ShouldHandleHotkeysAtWindowLevel returns true when keyboard events should be
// handled at the window level instead of the focused widget. On mobile the
// image view is not focused (to suppress the soft keyboard), so hotkeys
// (including the Android Back button) never reach the widget's TypedKey
// handler. The window-level handler must process them instead.
func (p *Platform) ShouldHandleHotkeysAtWindowLevel() bool {
	return p.isMobile
}

// ShouldRegisterBackButton returns true when the "Back" hardware key should be
// registered as a hotkey. Android and iOS send key name "Back" to the focused
// widget when the user presses the system back button.
func (p *Platform) ShouldRegisterBackButton() bool {
	return p.isMobile
}

// ShouldAutoFullscreen returns true when opening a single image should
// automatically enter fullscreen mode. Mobile devices benefit from fullscreen
// by default; desktop users prefer windowed mode.
func (p *Platform) ShouldAutoFullscreen() bool {
	return p.isMobile
}

// ShouldExitFullscreenOnGalleryView returns true when returning to the gallery
// grid should exit fullscreen mode. On mobile, fullscreen is the default for
// single-image view but should be cleared when showing the grid.
func (p *Platform) ShouldExitFullscreenOnGalleryView() bool {
	return p.isMobile
}

// ShouldDownscaleImages returns true when decoded images should be downscaled
// to reduce GPU memory usage. Mobile devices with limited VRAM benefit from
// capping texture size to avoid re-upload overhead on every pinch-zoom frame.
// Desktop devices can handle full-resolution textures.
func (p *Platform) ShouldDownscaleImages() bool {
	return p.isMobile
}

// UsesMobileDragGestures returns true when drag/swipe gestures should use
// mobile-optimized handling (pinch-zoom, momentum scrolling). Desktop uses
// direct pan/drag.
func (p *Platform) UsesMobileDragGestures() bool {
	return p.isMobile
}

// ShouldUseTapForAction returns true when a tap (not swipe) should trigger
// the given action. On desktop, tap is preferred for most actions; on mobile,
// swipe may be preferred for certain gestures to avoid conflicts with pinch-zoom.
func (p *Platform) ShouldUseTapForAction() bool {
	return !p.isMobile
}
