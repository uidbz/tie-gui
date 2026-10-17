// Page state: the listing the gallery is showing, the current page, and the
// placement that turns a slice of the listing into tiles. Everything that
// feeds or replaces imageFiles lives here; the fetch that produced it is
// tracked so a placement can wait for it.
package gallery

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mholt/archives"

	"fyne.io/fyne/v2"

	"github.com/uidbz/tie-gui/tielog"
)

func (viewer *Gallery) ShowImageDir(path string) {
	viewer.setFiles(nil)
	viewer.currentPage = 0
	viewer.layout.setOffset(0)
	viewer.openedInfo = nil
	viewer.ReadImageDir(path, nil)
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *Gallery) ShowImageArchive(path string) {
	viewer.setFiles(nil)
	viewer.currentPage = 0
	viewer.layout.setOffset(0)
	viewer.openedInfo = nil
	viewer.ReadImageArchive(path)
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

// beginFetch and endFetch bracket a listing read. Every read builds its list
// locally and swaps it in, so a concurrent reader never sees a half-built list;
// the count exists so a page placement can wait for the read it depends on.
func (viewer *Gallery) beginFetch() {
	viewer.fetchMu.Lock()
	viewer.fetchCount++
	viewer.fetchMu.Unlock()
}

func (viewer *Gallery) endFetch() {
	viewer.fetchMu.Lock()
	viewer.fetchCount--
	if viewer.fetchCount == 0 {
		for _, ch := range viewer.fetchIdle {
			close(ch)
		}
		viewer.fetchIdle = nil
	}
	viewer.fetchMu.Unlock()
}

// fetchPending reports whether a listing read is in flight. The answer is a
// moment stale by nature; it only selects the fast path from the slow one.
func (viewer *Gallery) fetchPending() bool {
	viewer.fetchMu.Lock()
	defer viewer.fetchMu.Unlock()
	return viewer.fetchCount > 0
}

// waitFetches blocks until no listing read is in flight. It must not run on
// the UI goroutine — that is the whole point of it.
func (viewer *Gallery) waitFetches() {
	viewer.fetchMu.Lock()
	if viewer.fetchCount == 0 {
		viewer.fetchMu.Unlock()
		return
	}
	ch := make(chan struct{})
	viewer.fetchIdle = append(viewer.fetchIdle, ch)
	viewer.fetchMu.Unlock()
	<-ch
}

// LoadGallery lays out the current page of the current image list.
//
// It never blocks the caller. A listing fetch that is still in flight
// (ReadCustomAsync, ReadImageArchive) used to be waited for here with
// loading.Wait(), and the queue drain below waited for in-flight thumbnail
// loads — both from the UI goroutine, because LoadGallery is called from click
// handlers, pagination links and tag selections. One stalled triplestore or
// filehost therefore froze the window: the click did nothing, repainting
// stopped, and there was no cancel. Now the waits happen on a helper goroutine
// and the placement itself on the placement goroutine, so the click returns
// immediately and the stale page stays on screen until the new one is ready.
func (viewer *Gallery) LoadGallery() {
	// A fresh gallery view (new page, new directory, new query) starts at
	// the top; showGallery's tile-tracking restore is the only path that
	// scrolls anywhere else.
	if viewer.scroll != nil {
		viewer.scroll.ScrollToOffset(fyne.NewPos(0, 0))
	}
	viewer.startPlacement()
}

// SetSyncPlacement runs page placement on the caller's goroutine instead of a
// background one.
//
// It exists for tests. The software test driver has no UI goroutine: it runs
// fyne.Do inline on whichever goroutine called it, so a gallery worker and a
// test that drives layout itself (CreateView, Resize, Tap) mutate the widget
// tree concurrently, which -race reports even when the two are ordered by
// AwaitIdle. Placing on the caller's goroutine removes the second writer.
//
// Production code must not call it: placement waits for the previous page's
// thumbnail loads, and putting that back on the caller is the freeze
// LoadGallery exists to avoid.
func (viewer *Gallery) SetSyncPlacement(on bool) {
	viewer.syncPlace = on
	if viewer.layout != nil {
		viewer.layout.syncUI = on
	}
}

// startPlacement queues one page placement, superseding any placement queued
// by an earlier call.
func (viewer *Gallery) startPlacement() {
	seq := viewer.placeSeq.Add(1)
	if viewer.syncPlace {
		// The caller owns the widget tree in this mode, so waiting for the
		// listing here is both safe and required: placeNow publishes the
		// pagination widgets, and doing that on a helper goroutine would put
		// a second writer on them.
		viewer.waitFetches()
		viewer.placeNow(seq)
		return
	}
	if !viewer.fetchPending() {
		viewer.placeNow(seq)
		return
	}
	// A listing fetch is in flight and its result is the page we want to
	// place. Waiting for it on this goroutine would freeze the UI, so wait on
	// a helper and come back on the UI goroutine, where the image list and
	// page offset are snapshotted — they are only ever written from there.
	//
	// The placement WaitGroup is raised here, before the helper starts, and
	// released when the hand-over completes: a caller waiting for the grid to
	// settle then also waits for this queued placement, and the count never
	// drops to zero between the helper and the placement it starts.
	viewer.layout.placement.Add(1)
	go func() {
		defer tielog.Recover(viewer.log(), "gallery.placementHelper")
		viewer.waitFetches()
		fyne.Do(func() {
			defer viewer.layout.placement.Done()
			viewer.placeNow(seq)
		})
	}()
}

// placeNow snapshots the page on the UI goroutine and hands the layout work to
// the placement goroutine. seq is the placement generation this call was
// queued under.
func (viewer *Gallery) placeNow(seq uint64) {
	if seq != viewer.placeSeq.Load() {
		return // superseded: a newer LoadGallery owns the next placement
	}
	// Snapshot the slice header and page offset on the UI goroutine: a later
	// tag query swaps viewer.files() and ChangeGallery/ChangePage rewrite
	// layout.offset, so reading either inside the spawned goroutine would
	// race with them.
	imageFiles, offset := viewer.files(), viewer.layout.getOffset()
	viewer.buildPagination()
	viewer.galleryLoaded = true
	if viewer.syncPlace {
		viewer.layout.placeMu.Lock()
		viewer.layout.drainPending()
		viewer.layout.currentlyLoading.Wait()
		viewer.layout.placeTiles(imageFiles, offset)
		viewer.layout.placeMu.Unlock()
		// Synchronous mode means the caller owns the widget tree, so the page
		// is not finished until the thumbnail workers' write-backs have landed
		// too. AwaitIdle also gives them the happens-before edge a test needs
		// before it inspects or resizes the grid.
		viewer.AwaitIdle()
		return
	}
	viewer.layout.placement.Add(1)
	go func() {
		defer viewer.layout.placement.Done()
		defer tielog.Recover(viewer.log(), "gallery.placement")
		// One placement at a time: the drain and the wait below touch the
		// same queue and counter as the previous placement, and placeTiles
		// assumes it holds the lock.
		viewer.layout.placeMu.Lock()
		defer viewer.layout.placeMu.Unlock()
		// Discard the previous page's queued work and wait for its in-flight
		// loads. Both used to happen in ChangeGallery/ChangePage, on the UI
		// goroutine. Here they cost a background goroutine some time, and the
		// tileUpdater's Info guard drops any late result from the old page.
		viewer.layout.drainPending()
		viewer.layout.currentlyLoading.Wait()
		viewer.layout.placeTiles(imageFiles, offset)
	}()
}

// Loaded reports whether the grid has been laid out at least once
// (LoadGallery ran), so an embedding app can re-show Content as-is instead
// of reloading it.
func (viewer *Gallery) Loaded() bool {
	return viewer.galleryLoaded
}

// files returns the current listing. Treat the result as read-only: a later
// listing read replaces the slice rather than editing it.
func (viewer *Gallery) files() []*ImageInfo {
	viewer.filesMu.RLock()
	defer viewer.filesMu.RUnlock()
	return viewer.imageFiles
}

// setFiles publishes a finished listing in one step.
func (viewer *Gallery) setFiles(files []*ImageInfo) {
	viewer.filesMu.Lock()
	viewer.imageFiles = files
	viewer.filesMu.Unlock()
}

// AwaitIdle blocks until the gallery's background work — page placement,
// thumbnail loads and the widget write-backs they queue — has been applied.
//
// Embedding apps do not need it: in production every write-back runs on the
// one UI goroutine, in order. Tests that inspect or resize the grid from their
// own goroutine do, because the software test driver executes fyne.Do inline
// on the calling goroutine, so the gallery's workers touch the widget tree
// concurrently with the test.
func (viewer *Gallery) AwaitIdle() {
	if viewer.layout == nil {
		return
	}
	viewer.layout.placement.Wait()
	viewer.layout.currentlyLoading.Wait()
	viewer.layout.uiSync()
}

// ImageCount reports how many images the viewer currently holds.
func (viewer *Gallery) ImageCount() int {
	return len(viewer.files())
}

// RemoveItem removes info from the viewer's image list and fixes the order
// indices of remaining items. Call ChangeGallery afterwards to refresh the UI.
func (viewer *Gallery) RemoveItem(info *ImageInfo) {
	viewer.filesMu.Lock()
	defer viewer.filesMu.Unlock()
	for i, item := range viewer.imageFiles {
		if item == info {
			// Copy rather than splice in place: a placement goroutine may be
			// holding a snapshot of this same slice.
			next := make([]*ImageInfo, 0, len(viewer.imageFiles)-1)
			next = append(next, viewer.imageFiles[:i]...)
			next = append(next, viewer.imageFiles[i+1:]...)
			for j := i; j < len(next); j++ {
				next[j].order = j
			}
			viewer.imageFiles = next
			return
		}
	}
}

// ChangeGallery re-lays out the grid after the image list was replaced (a new
// query, a new directory). The previous page's queued and in-flight thumbnail
// work is discarded by the placement goroutine, not here, so this returns
// immediately even while a fetch is running.
func (viewer *Gallery) ChangeGallery() {
	viewer.currentPage = 0
	viewer.layout.setOffset(0)
	// The image list was replaced; any remembered open entry no longer
	// applies to it.
	viewer.openedInfo = nil
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

// ChangePage shows page page of the current list. Like ChangeGallery it does
// not wait for the previous page's transfers.
func (viewer *Gallery) ChangePage(page int) {
	if page < 0 || page > viewer.maxPages-1 {
		return
	}
	viewer.currentPage = page
	viewer.layout.setOffset(page * viewer.config.General.ImagesPerPage)
	viewer.LoadGallery()
	viewer.window.SetContent(viewer.Content)
}

func (viewer *Gallery) ReadImageDir(absolutePath string, selected *ImageInfo) {
	viewer.beginFetch()
	defer viewer.endFetch()

	viewer.currentPath = absolutePath
	dir, _ := os.ReadDir(absolutePath)
	// Build locally and swap once, the way ReadCustom does: a tag query or
	// a page read must never see a half-filled listing.
	var imageFiles []*ImageInfo
	i := 0
	for _, x := range dir {
		fullPath := filepath.Join(absolutePath, x.Name())
		switch true {
		case x.IsDir():
			subDirAbsPath := filepath.Join(absolutePath, x.Name())
			subDir, _ := os.ReadDir(subDirAbsPath)
			var previews []string
			for _, y := range subDir {
				if y.IsDir() {
					continue
				}
				subFilePath := filepath.Join(subDirAbsPath, y.Name())
				if IsImageFromPath(subFilePath) {
					previews = append(previews, subFilePath)
				}
			}
			if len(previews) > 0 {
				info := NewImageInfo(i, previews[0])
				info.InputIsDir = true
				info.DirPath = subDirAbsPath
				info.PreviewPaths = previews
				info.DisplayName = x.Name()
				imageFiles = append(imageFiles, info)
				i++
			}

		case IsArchiveFromPath(fullPath):
			// Pass only the filename (nil stream): the library then manages
			// its own file handles. Passing an *os.File and closing it here
			// leaves the returned FS unable to read members later ("file
			// already closed"), so member thumbnails never loaded.
			fsys, err := archives.FileSystem(context.Background(), fullPath, nil)
			if err != nil {
				continue
			}
			// Collect every image member so the archive tile can swipe
			// through them; the first image becomes the gallery entry.
			var previews []string
			fs.WalkDir(fsys, ".", func(path string, x fs.DirEntry, err error) error {
				if x.IsDir() {
					return nil
				}
				file, err := fsys.Open(path)
				if err != nil {
					return err
				}
				if IsImage(file) {
					previews = append(previews, path)
				}
				file.Close()
				return nil
			})
			if len(previews) > 0 {
				info := NewImageInfo(i, previews[0])
				info.InputIsDir = true
				info.DirPath = fullPath
				info.FullPath = fullPath
				info.archiveFile = fsys
				info.archiveName = filepath.Base(fullPath)
				info.InputIsArchive = true
				info.ShowArchive = true
				info.DisplayName = filepath.Base(fullPath)
				info.PreviewPaths = previews
				imageFiles = append(imageFiles, info)
				i++
			}

		case IsVideoFromPath(fullPath):
			info := NewImageInfo(i, fullPath)
			info.InputIsVideo = true
			info.DirPath = absolutePath
			info.DisplayName = x.Name()
			imageFiles = append(imageFiles, info)
			i++

		case IsImageFromPath(fullPath):
			if selected != nil && selected.Path == fullPath {
				selected.order = i
				selected.DirPath = absolutePath
				selected.DisplayName = x.Name()
				viewer.currentIndex = i
				imageFiles = append(imageFiles, selected)
			} else {
				info := NewImageInfo(i, fullPath)
				info.DirPath = absolutePath
				info.DisplayName = x.Name()
				imageFiles = append(imageFiles, info)
			}
			i++
		}
	}

	// Sort: primary key = DirPath (groups subdirs and archives together),
	// secondary key = filename (alphabetical within each group).
	sort.Slice(imageFiles, func(i, j int) bool {
		a, b := imageFiles[i], imageFiles[j]
		if a.DirPath != b.DirPath {
			return a.DirPath < b.DirPath
		}
		return filepath.Base(a.Path) < filepath.Base(b.Path)
	})
	for i := range imageFiles {
		imageFiles[i].order = i
	}
	viewer.setFiles(imageFiles)
}

func (viewer *Gallery) ReadCustom(readers []CustomReader) {
	viewer.beginFetch()
	defer viewer.endFetch()

	// Build into a local slice and swap, so overlapping tie queries can't
	// interleave appends into viewer.files() (last query wins).
	imageFiles := make([]*ImageInfo, 0, len(readers))
	for i, r := range readers {
		info := NewImageInfoCustomReader(i, r)
		info.Path = r.Path()
		// Set display name: prefer DisplayNamer interface, fallback to basename.
		// For URIs, filepath.Base may not work correctly, so DisplayNamer is important.
		if dn, ok := r.(DisplayNamer); ok {
			info.DisplayName = dn.DisplayName()
		} else {
			// Fallback: extract basename from path
			// This works for file paths but may show full URI for content:// schemes
			base := filepath.Base(info.Path)
			// If base is still very long (e.g. a URI), try to extract just the filename component
			if len(base) > 50 && strings.Contains(base, "%2F") {
				// URI-encoded path separator - decode and take last component
				parts := strings.Split(base, "%2F")
				if len(parts) > 0 {
					base = parts[len(parts)-1]
				}
			}
			info.DisplayName = base
		}
		if st, ok := r.(Subtitler); ok {
			info.Subtitle = st.Subtitle()
		}
		if o, ok := r.(Openable); ok {
			info.OnOpen = o.Open
		}
		if vf, ok := r.(VideoFile); ok && vf.IsVideo() {
			info.InputIsVideo = true
		}
		if dp, ok := r.(DimensionProvider); ok {
			info.SetDimensions(dp.Dimensions())
		}
		imageFiles = append(imageFiles, info)
	}
	// Publish the readers and the listing together: two overlapping async
	// fetches must not leave CustomReaders from one query paired with the
	// images of another.
	viewer.filesMu.Lock()
	viewer.CustomReaders = readers
	viewer.imageFiles = imageFiles
	viewer.filesMu.Unlock()
}

// ReadCustomAsync replaces the viewer's images with the readers produced by
// fetch, which runs in a goroutine. A LoadGallery queued right after this call
// defers its page placement until fetch and the swap have completed, without
// blocking the caller. A nil result from fetch (e.g. after it logged an error)
// leaves the current images untouched.
func (viewer *Gallery) ReadCustomAsync(fetch func() []CustomReader) {
	// Count before spawning the goroutine, so a LoadGallery queued right after
	// this call knows a listing is on its way and defers its placement until
	// the query results have been turned into images.
	viewer.beginFetch()
	go func() {
		defer viewer.endFetch()
		defer tielog.Recover(viewer.log(), "gallery.ReadCustomAsync")
		readers := fetch()
		if readers == nil {
			return
		}
		viewer.ReadCustom(readers)
	}()
}

func (viewer *Gallery) ReadImageArchive(zipFile string) {
	viewer.beginFetch()
	defer viewer.endFetch()

	// Pass only the filename (nil stream): the library manages its own file
	// handles, so the returned FS stays usable after this function returns.
	fsys, err := archives.FileSystem(context.Background(), zipFile, nil)
	if err != nil {
		// Archive cannot be opened; imageFiles stays empty
		return
	}
	// Build into a local slice and swap, the way ReadCustom does: calling this
	// twice (or an archive after a directory) replaces the listing instead of
	// appending duplicates with stale order indices.
	var imageFiles []*ImageInfo
	fs.WalkDir(fsys, ".", func(path string, x fs.DirEntry, err error) error {
		if x.IsDir() {
			return nil
		}
		file, err := fsys.Open(path)
		if err != nil {
			// Skip files that can't be opened
			return nil
		}
		// One handle per member: without this the walk relied on the os.File
		// finalizer to close them, and a large archive reached EMFILE first.
		if IsImage(file) {
			info := NewImageInfo(len(imageFiles), path)
			info.InputIsArchive = true
			info.archiveName = filepath.Base(zipFile)
			info.archiveFile = fsys
			imageFiles = append(imageFiles, info)
		}
		file.Close()
		return nil
	})
	sort.Slice(imageFiles, func(i, j int) bool {
		return imageFiles[i].Path < imageFiles[j].Path
	})
	for i := range imageFiles {
		imageFiles[i].order = i
	}
	viewer.setFiles(imageFiles)
}
