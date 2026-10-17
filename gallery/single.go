// The single-image view: opening one entry, next/prev, fullscreen, the
// image-view cache, the external command and save actions, and the video
// overlay that replaces the grid.
package gallery

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"

	"github.com/uidbz/tie-gui/mpvplayer"
	"github.com/uidbz/tie-gui/tielog"
)

func (viewer *Gallery) CurrentImageInfo() *ImageInfo {
	return viewer.CurrentImageView.info
}

// prefetchImage decodes the next image on a background goroutine and parks the
// result on its ImageInfo, where ImageView.LoadImage picks it up.
//
// It used to call LoadImageToCache directly. That builds an ImageView: it reads
// the canvas size and creates or refreshes a canvas.Image, while showGallery
// frees the current view's bitmap on the UI goroutine — so the two wrote the
// same canvas.Image from different goroutines. Decoding is pure; attaching the
// pixels to a widget is not.
func (viewer *Gallery) prefetchImage(info *ImageInfo) {
	defer tielog.Recover(viewer.log(), "gallery.prefetchImage")
	viewer.cacheMu.Lock()
	_, built := viewer.cache[info.Path]
	viewer.cacheMu.Unlock()
	if built {
		return // the view already exists; decoding again would be wasted
	}
	img, format, err := info.decodedImage()
	if err != nil || img == nil {
		if err != nil {
			viewer.log().Debug("gallery: prefetch decode", "path", info.Path, "err", err)
		}
		return
	}
	info.setPrefetched(img, format)
}

func (viewer *Gallery) LoadImageToCache(info *ImageInfo) *ImageView {
	viewer.cacheMu.Lock()
	defer viewer.cacheMu.Unlock()
	if x, ok := viewer.cache[info.Path]; ok == false {
		if viewer.OnTapped != nil {
			info.OnTapped = viewer.OnTapped
		}
		if viewer.OnSwipeUp != nil {
			info.OnSwipeUp = viewer.OnSwipeUp
		}
		if viewer.OnDoubleTapped != nil {
			info.OnDoubleTapped = viewer.OnDoubleTapped
		}
		if info.OnDoubleTapped == nil {
			info.OnDoubleTapped = viewer.ToggleFullscreen
		}
		img := NewImageView(info, viewer.window.Canvas().Size(), true, viewer.window, viewer.window.Canvas().Focus, viewer.platform)
		img.log = viewer.log()
		// Update window title when image size changes (zoom, window resize).
		// Called from ImageLayout.Layout and ImageView zoom methods, which
		// already run on the UI thread.
		img.changeFn = func() {
			viewer.window.SetTitle("imgview - " + img.GetImageInfo())
		}
		// Wire fullscreen toggle for mobile tap-to-hide-system-bars behavior.
		img.toggleFullscreen = viewer.ToggleFullscreen
		viewer.cache[info.Path] = img
		return img
	} else {
		// A cached view may have had its bitmap released when returning to
		// the gallery (showGallery frees it to cut memory). Reload it,
		// otherwise the previously seen image comes back blank.
		if x.fyneImage == nil || x.fyneImage.Image == nil {
			x.loadOrPlaceholder()
		}
		return x
	}
}

func (viewer *Gallery) ToggleFullscreen() {
	viewer.isFullscreen = !viewer.isFullscreen
	viewer.CurrentImageView.fillWindow = false
	viewer.window.SetFullScreen(viewer.isFullscreen)
	viewer.CurrentImageView.fillWindow = true
	viewer.Content.Refresh()
}

func (viewer *Gallery) RunCmdA() {
	cmd := strings.ReplaceAll(viewer.config.Image.CmdA, "$FILE", viewer.CurrentImageView.info.Path)
	c := exec.Command("/bin/sh", "-c", cmd)
	go func() {
		defer tielog.Recover(viewer.log(), "gallery.runCmdA")
		// Command runs in background; configure command to show its own output if needed
		if err := c.Run(); err != nil {
			viewer.log().Warn("gallery: CmdA failed", "cmd", cmd, "err", err)
		}
	}()
}

func (viewer *Gallery) SaveImage() {
	if viewer.config.Image.SaveDir != "" {
		info := viewer.CurrentImageView.info
		if r, err := info.GetReader(); err == nil {
			if data, err := io.ReadAll(r); err == nil {
				filename := filepath.Base(info.Path)
				var dest string
				if info.InputIsArchive {
					dest = filepath.Join(viewer.config.Image.SaveDir, info.archiveName+"-"+filename)
				} else {
					dest = filepath.Join(viewer.config.Image.SaveDir, filename)
				}
				// Write file; silently ignore errors (user will notice if file doesn't appear)
				os.WriteFile(dest, data, 0755)
			}
		}
	}
}

func (viewer *Gallery) NextImage() *ImageInfo {
	// No wait for in-flight listing reads: every read builds its list locally
	// and swaps the slice in, so the list is never half-built. Waiting here
	// used to freeze the arrow keys and swipes behind a stalled tie query.
	files := viewer.files()
	if len(files) == 0 {
		return nil
	}
	nextImg := viewer.currentIndex + 1
	if nextImg >= len(files) {
		nextImg = len(files) - 1
	}
	return files[nextImg]
}

func (viewer *Gallery) PrevImage() *ImageInfo {
	files := viewer.files()
	if len(files) == 0 {
		return nil
	}
	nextImg := viewer.currentIndex - 1
	if nextImg < 0 {
		nextImg = 0
	}
	if nextImg >= len(files) {
		nextImg = len(files) - 1
	}
	return files[nextImg]
}

// showGallery returns to the gallery grid from the single-image or video view,
// releasing playback/image resources and restoring the prior scroll position.
func (viewer *Gallery) showGallery() {
	if viewer.platform.ShouldExitFullscreenOnGalleryView() && viewer.isFullscreen {
		viewer.isFullscreen = false
		viewer.window.SetFullScreen(false)
	}
	// Stop and release a playing video before returning to the grid.
	if viewer.currentVideo != nil {
		viewer.currentVideo.Close()
		viewer.currentVideo = nil
		if viewer.videoOnClose != nil {
			viewer.videoOnClose()
			viewer.videoOnClose = nil
		}
	}
	// Release the current full-size image to free memory when returning to gallery
	if viewer.CurrentImageView != nil && viewer.CurrentImageView.fyneImage != nil {
		viewer.CurrentImageView.fullImage = nil
		viewer.CurrentImageView.fyneImage.Image = nil
		viewer.CurrentImageView.fyneImage.Refresh()
	}
	// The info overlay belongs to the image view; close it when leaving.
	if viewer.infoOverlay != nil {
		viewer.infoOverlay.Hide()
	}
	// Switch back to the page that holds the opened entry before the tiles
	// are placed, so the grid ends up showing the image the user came from.
	// The entry may have moved to a different page while it was open (next/
	// prev navigation, or a resized window re-slicing the pages).
	pageBefore := viewer.currentPage
	if viewer.openedInfo != nil {
		files := viewer.files()
		idx := viewer.openedInfo.order
		if idx < 0 || idx >= len(files) || files[idx] != viewer.openedInfo {
			idx = -1
			for i, item := range files {
				if item == viewer.openedInfo {
					idx = i
					break
				}
			}
		}
		if idx >= 0 {
			if ipp := viewer.config.General.ImagesPerPage; ipp > 0 {
				viewer.currentPage = idx / ipp
				viewer.layout.setOffset(viewer.currentPage * ipp)
			}
		}
	}
	reloaded := !viewer.galleryLoaded || viewer.currentPage != pageBefore
	if reloaded {
		// The page is about to be replaced: hand the tile's page-relative
		// index to PlaceTiles, which scrolls it into view once the new
		// page's tiles are positioned. Must be set before LoadGallery
		// spawns the placement goroutine.
		if viewer.openedInfo != nil && viewer.layout != nil {
			if idx := viewer.openedInfo.order - viewer.layout.getOffset(); idx >= 0 {
				viewer.layout.pendingReveal = idx
			}
		}
		viewer.LoadGallery()
	} else {
		viewer.buildPagination()
	}
	viewer.CreateView()
	// On an unchanged page the tiles are already positioned: scroll the
	// opened tile into view directly (re-laying out first in case the
	// window was resized while the image was open).
	if !reloaded && viewer.scroll != nil && viewer.openedInfo != nil && viewer.layout != nil {
		idx := viewer.openedInfo.order - viewer.layout.getOffset()
		if idx >= 0 && idx < len(viewer.layout.tiles) {
			viewer.layout.relayoutGrid()
			tile := viewer.layout.tiles[idx]
			viewer.layout.scrollToOffset(tile.Position().Y, tile.Size().Height)
		} else {
			viewer.scroll.ScrollToOffset(viewer.savedScrollOffset)
		}
	} else if !reloaded && viewer.scroll != nil {
		viewer.scroll.ScrollToOffset(viewer.savedScrollOffset)
	}
	viewer.window.SetTitle("imgview")
	viewer.window.SetContent(viewer.Content)
}

// ShowGrid returns to the gallery grid from the single-image or video view,
// releasing playback/image resources. It is the exported form of the
// Q/Escape/Back handler, for embedding apps that need to force the grid back
// (e.g. after replacing the gallery's contents while an image was open).
func (viewer *Gallery) ShowGrid() {
	viewer.showGallery()
}

// VideoActive reports whether the in-window video player is currently shown.
func (viewer *Gallery) VideoActive() bool {
	return viewer.currentVideo != nil
}

// ShowVideo plays a video in the main window (mirroring ChangeImage's in-window
// swap) instead of spawning a separate window. On mobile it auto-enters
// fullscreen. onClose, if non-nil, runs after the player is closed (temp-file
// cleanup). Must be called on the UI goroutine.
func (viewer *Gallery) ShowVideo(player *mpvplayer.MPVPlayer, displayName string, onClose func()) {
	v := mpvplayer.NewVideo(player)
	viewer.currentVideo = v
	viewer.videoOnClose = onClose
	v.OnFullscreen = viewer.toggleVideoFullscreen
	if viewer.scroll != nil {
		viewer.savedScrollOffset = viewer.scroll.Offset
	}
	viewer.window.SetTitle("Video: " + displayName)
	viewer.Content.Objects = []fyne.CanvasObject{v}
	viewer.Content.Refresh()
	if viewer.platform.ShouldAutoFullscreen() && !viewer.isFullscreen {
		viewer.isFullscreen = true
		viewer.window.SetFullScreen(true)
		v.SetFullscreen(true)
	}
}

// toggleVideoFullscreen toggles OS window fullscreen for the video view and
// informs the widget so tap-to-toggle-controls activates. Kept separate from
// ToggleFullscreen, which dereferences CurrentImageView (nil during video).
func (viewer *Gallery) toggleVideoFullscreen() {
	if viewer.currentVideo == nil {
		return
	}
	viewer.isFullscreen = !viewer.isFullscreen
	viewer.window.SetFullScreen(viewer.isFullscreen)
	viewer.currentVideo.SetFullscreen(viewer.isFullscreen)
}

func (viewer *Gallery) ChangeImage(info *ImageInfo) {
	if info == nil {
		return
	}
	if info.OnOpen != nil {
		info.OnOpen()
		return
	}
	// Video files cannot be decoded as images; the caller's tile-onclick
	// handler (e.g. in cmd/imgview) is responsible for starting the player.
	if info.InputIsVideo {
		return
	}
	img := viewer.LoadImageToCache(info)
	viewer.currentPath = filepath.Dir(info.Path)
	viewer.CurrentImageView = img
	// Remember which gallery entry is open so showGallery can switch back to
	// its page and scroll it into view, even across pages.
	viewer.openedInfo = info
	// Save the gallery scroll position so it can be restored when the user
	// navigates back from the single-image view.
	if viewer.scroll != nil {
		viewer.savedScrollOffset = viewer.scroll.Offset
	}
	img.fillWindow = true
	img.container = viewer.Content
	img.hotkeys = viewer.hotkeys
	img.nextFn = func() { viewer.ChangeImage(viewer.NextImage()) }
	img.prevFn = func() { viewer.ChangeImage(viewer.PrevImage()) }
	viewer.CurrentImage.Objects = []fyne.CanvasObject{img}
	if info.order != -1 {
		viewer.currentIndex = info.order
	}
	// Prefetch the next image off-thread. NextImage is evaluated here, on
	// the calling (UI) goroutine, after currentIndex is updated: reading
	// imageFiles/currentIndex from a background goroutine races with tag
	// queries swapping viewer.files() and can index out of range.
	if next := viewer.NextImage(); next != nil && !next.InputIsVideo {
		go viewer.prefetchImage(next)
	}
	viewer.Content.Objects = []fyne.CanvasObject{viewer.CurrentImage}
	// Float the ☰ menu button over the image so the gallery menu (image
	// info, filename toggle) stays reachable while an image is open.
	if viewer.imageMenuOverlay != nil {
		viewer.Content.Objects = append(viewer.Content.Objects, viewer.imageMenuOverlay)
	}
	// Keep the info overlay across next/prev navigation: re-stack it above
	// the new image and retarget its contents.
	if viewer.infoOverlay != nil && viewer.infoOverlay.Visible() {
		viewer.Content.Objects = append(viewer.Content.Objects, viewer.infoOverlay)
		viewer.infoOverlay.setInfo(info)
	}
	viewer.Content.Refresh()
	if viewer.platform.ShouldAutoFullscreen() && !viewer.isFullscreen {
		viewer.isFullscreen = true
		viewer.window.SetFullScreen(true)
	}
	if viewer.OnImageChange != nil {
		viewer.OnImageChange(info)
	}
	img.changeFn()
}

// func (viewer *Gallery) SetImage() {
// 	viewer.window.SetContent(viewer.imageContainer)
// 	viewer.window.Canvas().Focus(viewer.currentImage)
// }
