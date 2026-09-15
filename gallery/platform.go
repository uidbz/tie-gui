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

// NewPlatformFor creates a Platform for an explicitly chosen device type,
// bypassing detection. It exists for tests in other packages (which cannot
// reach the unexported field, and cannot make Fyne report a phone) and for
// callers that already know what they are targeting.
func NewPlatformFor(isMobile bool) *Platform {
	return &Platform{isMobile: isMobile}
}

// IsMobile returns true on mobile devices (Android, iOS).
func (p *Platform) IsMobile() bool {
	return p.isMobile
}

// CompactWidth is the width, in Fyne device-independent pixels, below which a
// touch window is treated as compact: too narrow to show a sidebar, a content
// grid and a companion pane side by side with finger-sized controls, or a
// table with several columns.
//
// The bar is set at a tablet in *landscape* (~1280dp), not at a phone: a 10"
// tablet in portrait is only ~800dp, which fits a split layout on paper but
// leaves each region too cramped to touch comfortably — a sidebar at 20% of
// 800dp is 160dp, narrower than a phone's whole screen. Pointer-driven
// windows are never compact regardless (see CompactLayout), so this value
// only ever describes touch devices.
const CompactWidth float32 = 1000

// CompactLayout reports whether a window of the given width should use the
// compact (phone) layout: panels become full-screen views or slide-over
// drawers instead of split panes. It is deliberately width-based rather than
// IsMobile-based, because Fyne reports tablets as mobile and a tablet (or a
// phone in landscape) has room for the split layout.
//
// A non-positive width means the canvas has not been laid out yet. On mobile
// that is assumed compact — the common case, and guessing the wide layout for
// a phone would show the split layout for one frame before flipping.
func (p *Platform) CompactLayout(width float32) bool {
	if !p.isMobile {
		return false
	}
	return width <= 0 || width < CompactWidth
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
