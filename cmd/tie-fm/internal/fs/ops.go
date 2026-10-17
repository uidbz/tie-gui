package fs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uidbz/tie/client"
)

type OpType int

const (
	OpCopy OpType = iota
	OpMove
	OpExtract // unpack archive A into directory B (Operations.Extract)
)

type Status int

const (
	StatusPending Status = iota
	StatusRunning
	StatusCompleted
	StatusError
)

// Operations is a queue-based async engine that runs file copy/move ops one at a
// time, exposing in-flight ops for progress display.
type Operations struct {
	queued chan *Op
	// mu guards active: run appends and remove deletes on the ops goroutine,
	// while the progress view reads Active() from the UI goroutine.
	mu     sync.Mutex
	active []*Op
	reg    *Registry
	// ActiveChanged fires on the ops goroutine whenever the in-flight list
	// changes. Implementations must marshal any UI work into fyne.Do.
	ActiveChanged func()
	// ConflictResolver, when non-nil, is consulted whenever a copy/move finds
	// the destination name already occupied. It runs on the ops goroutine and
	// must block until the user answers (the UI marshals the dialog to the UI
	// goroutine via fyne.Do and waits on a channel). A nil resolver keeps the
	// historic behaviour: silently overwrite.
	ConflictResolver func(Conflict) ConflictResult
}

func NewOperations(reg *Registry) *Operations {
	o := &Operations{
		queued:        make(chan *Op, 16),
		reg:           reg,
		ActiveChanged: func() {},
	}
	go o.run()
	return o
}

// ConflictAction is the user's answer to a destination-conflict prompt,
// mirroring the options of KDE Dolphin's (KIO RenameDialog) conflict dialog.
type ConflictAction int

const (
	ConflictCancel       ConflictAction = iota // abort the operation
	ConflictSkip                               // skip this item
	ConflictSkipAll                            // skip this and all further conflicts of the op
	ConflictOverwrite                          // replace the existing item
	ConflictOverwriteAll                       // replace this and all further conflicts of the op
	ConflictRename                             // transfer under ConflictResult.NewName instead
)

// Conflict describes a copy/move destination name that is already occupied.
type Conflict struct {
	Source   Entry // the item being transferred
	Existing Entry // the item already at the destination
	DestDir  string
	// Multiple is true for multi-item transfers (directories, file lists):
	// the dialog offers "Apply to All".
	Multiple bool
}

// ConflictResult is the user's decision about a Conflict.
type ConflictResult struct {
	Action  ConflictAction
	NewName string // for ConflictRename
}

// errConflictCancelled aborts an op when the user answers Cancel to a
// conflict prompt.
var errConflictCancelled = errors.New("cancelled")

func (o *Operations) run() {
	var opID int
	for op := range o.queued {
		o.mu.Lock()
		op.opID = opID
		opID++
		o.active = append(o.active, op)
		o.mu.Unlock()
		if err := op.ctx.Err(); err != nil {
			// Cancelled while still queued: skip the transfer entirely.
			op.SetStatus(StatusError)
			op.SetErr(err)
		} else {
			op.SetStatus(StatusRunning)
			if err := op.Do(); err != nil {
				op.SetStatus(StatusError)
				op.SetErr(err)
			}
		}
		o.remove(op.opID)
		if op.OnComplete != nil {
			op.OnComplete(op)
		}
	}
}

// Active returns the in-flight operations, oldest first. The snapshot is a copy:
// the ops goroutine appends to and deletes from the live list while the UI
// goroutine reads it to draw progress, so handing out the slice itself raced and
// let a frame render a torn view — a row for an op that had already finished, or
// the same op twice.
func (o *Operations) Active() []*Op {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]*Op(nil), o.active...)
}

func (o *Operations) remove(opID int) {
	o.mu.Lock()
	found := false
	for i, x := range o.active {
		if x.opID == opID {
			o.active = slices.Delete(o.active, i, i+1)
			found = true
			break
		}
	}
	o.mu.Unlock()
	if found {
		o.ActiveChanged()
	}
}

// Copy enqueues a copy of source into dest. done, if non-nil, runs after the op
// finishes (success or error), from the ops goroutine — wrap UI work in fyne.Do.
func (o *Operations) Copy(source, dest Entry, done func(*Op)) *Op {
	op := o.newOp(source, dest, OpCopy, done)
	o.queued <- op
	return op
}

// CopyAs enqueues a copy like Copy, additionally stamping the imported
// directory with the given dir-type label (e.g. "audio-dir") once the transfer
// into a typing backend (tie) succeeds. See Op.DirType for what gets labeled.
func (o *Operations) CopyAs(source, dest Entry, dirType string, done func(*Op)) *Op {
	op := o.newOp(source, dest, OpCopy, done)
	op.DirType = dirType
	o.queued <- op
	return op
}

// Move enqueues a move of source into dest. See Copy for the done callback.
func (o *Operations) Move(source, dest Entry, done func(*Op)) *Op {
	op := o.newOp(source, dest, OpMove, done)
	o.queued <- op
	return op
}

// MoveAs enqueues a move like Move, additionally stamping the imported
// directory with the given dir-type label, like CopyAs.
func (o *Operations) MoveAs(source, dest Entry, dirType string, done func(*Op)) *Op {
	op := o.newOp(source, dest, OpMove, done)
	op.DirType = dirType
	o.queued <- op
	return op
}

// ImportAlbum enqueues the import of one planned album group (from
// client.PlanAlbumImport). Unlike Copy/Move the destination is exact: the
// group lands at g.Dest verbatim, not at B.Path/<source name>. A whole-tree
// group mirrors its SourceDir there (sidecars included), a file-list group
// imports its listed files following the planner's disc-aware placement
// (g.SubPaths: multi-disc members route to cd<N> subdirectories) or, for
// files without a subpath, into Dest/<rel-below-SourceDir>, and an archive
// group is a single-file import. A whole-tree group the planner converted
// for its disc structure arrives as Files+Sidecars+SubPaths and takes the
// same file-list path. dirType is stamped on the imported album root like
// CopyAs — except for archive groups, where the blob itself carries the
// audio-archive classification. tags, when non-empty, are applied to every
// imported file and to the album root (the archive blob only for archive
// groups), so the album is queryable in media apps like tie-audio; the
// group's aggregated artist/album/year are recorded on the album root.
func (o *Operations) ImportAlbum(g client.AlbumGroup, dirType string, tags []string, done func(*Op)) *Op {
	src := Entry{Path: g.SourceDir, Name: filepath.Base(g.SourceDir), IsDir: true}
	dest := Entry{Path: tieURI(g.Dest), IsDir: true}
	op := o.newOp(src, dest, OpCopy, done)
	op.DirType = dirType
	op.Tags = tags
	switch {
	case g.IsArchive:
		// The archive blob is the album: tag it (via op.Tags), but leave the
		// destination directory unlabeled, untagged and without aggregates.
		op.A = Entry{Path: g.Files[0], Name: filepath.Base(g.Files[0]), Size: g.Size}
		op.DirType = ""
	case !g.WholeTree():
		// Files ∪ Sidecars: a disc-structured whole-tree group converts to an
		// explicit import in the planner, its cover art riding along as
		// sidecars. SubPaths carries the disc-aware placement (cd<N> routing).
		op.Files = make([]string, 0, len(g.Files)+len(g.Sidecars))
		op.Files = append(op.Files, g.Files...)
		op.Files = append(op.Files, g.Sidecars...)
		op.FileSubPaths = g.SubPaths
		op.SetTotalSize(g.Size) // probed by the planner; drives the progress bar
	default:
		op.ExactDest = true
	}
	if !g.IsArchive {
		op.DirTags = tags
		op.AlbumArtist = g.Artist
		op.AlbumTitle = g.Album
		if g.Year != 0 {
			op.AlbumYear = strconv.Itoa(g.Year)
		}
	}
	o.queued <- op
	return op
}

// newOp builds an operation with its cancellation context and pause condition
// initialized, so Pause/Resume/Cancel work even while the op is still queued.
func (o *Operations) newOp(source, dest Entry, t OpType, done func(*Op)) *Op {
	ctx, cancel := context.WithCancel(context.Background())
	op := &Op{
		A: source, B: dest, OpType: t,
		importer:   o.importerFor(dest.Path),
		srcFS:      o.exporterFor(source.Path),
		resolver:   o.ConflictResolver,
		multiple:   source.IsDir,
		OnComplete: done,
		ctx:        ctx,
		cancel:     cancel,
		// Set before the op is ever published to Active(), so the UI never
		// reads a zero start time.
		StartTime: time.Now(),
	}
	if o.reg != nil {
		op.destFS = o.reg.For(dest.Path)
	}
	op.cond = sync.NewCond(&op.mu)
	return op
}

// importerFor returns the Importer serving destPath's scheme, or nil when the
// destination is a plain local path (the byte-copy engine handles those). Any
// remote backend (tie, mtp) that implements Importer receives copied-in files.
func (o *Operations) importerFor(destPath string) Importer {
	if o.reg == nil || IsLocal(destPath) {
		return nil
	}
	imp, _ := o.reg.For(destPath).(Importer)
	return imp
}

// exporterFor returns the FileSystem serving a remote source (tie, mtp), used to
// materialize (download) its entries before copying them out to a local
// destination. It is nil for a plain local source (the byte-copy engine reads
// those directly).
func (o *Operations) exporterFor(srcPath string) FileSystem {
	if o.reg == nil || IsLocal(srcPath) {
		return nil
	}
	return o.reg.For(srcPath)
}

type Op struct {
	A         Entry
	B         Entry
	OpType    OpType
	StartTime time.Time
	// DirType, when non-empty, is a dir-type label (e.g. "audio-dir") stamped
	// on the imported directory after a successful import into a typing
	// backend (tie): the freshly created directory root for a directory
	// transfer, the destination directory itself for a file transfer.
	DirType string
	// Tags, when non-empty, are applied to every imported file (album
	// imports), making the album's tracks queryable in media apps.
	Tags []string
	// DirTags are applied to the labeled directory itself (album imports),
	// so the album directory matches tag queries like its files do.
	DirTags []string
	// AlbumArtist/AlbumTitle/AlbumYear, when non-empty, are the album's
	// aggregated metadata recorded on the labeled directory (album imports),
	// so media apps can title the album without reading its tracks.
	AlbumArtist string
	AlbumTitle  string
	AlbumYear   string
	// ExactDest makes a directory import land at B.Path verbatim (an album
	// import's rendered destination) instead of the usual B.Path/<A.Name>.
	ExactDest bool
	// Files, when non-nil, is an explicit list of files under A.Path to
	// import (an album group's audio files) instead of walking the whole
	// tree; each lands at B.Path/<rel-below-A.Path>.
	Files []string
	// FileSubPaths, when non-nil, overrides per-file placement for Files: a
	// source path maps to its slash-separated destination below B.Path
	// (the planner's disc-aware cd<N> routing). Files missing from the map
	// fall back to <rel-below-A.Path>.
	FileSubPaths map[string]string

	OnComplete func(*Op) // optional; called after the op finishes (ok or error)

	importer Importer   // non-nil when the destination is a remote backend (tie, mtp)
	srcFS    FileSystem // non-nil when the source is a remote backend (export)
	destFS   FileSystem // lists the destination for name collisions (extraction, conflicts)
	// resolver, when non-nil, is consulted when the destination name is
	// already occupied (copy/move conflicts); see Operations.ConflictResolver.
	resolver func(Conflict) ConflictResult
	// multiple marks a multi-item transfer (directory, file list): conflict
	// prompts offer "Apply to All".
	multiple bool
	// sticky, once set, is the conflict answer applied to all further
	// conflicts of this op without prompting again (SkipAll / OverwriteAll).
	sticky ConflictAction
	// stickySet distinguishes "no sticky answer" from ConflictCancel (0).
	stickySet bool
	// skipped records that at least one item was skipped; a move then keeps
	// its source instead of deleting it.
	skipped bool
	// renameTo, when non-empty, overrides the destination name (the
	// conflict dialog's Rename choice).
	renameTo string
	// listCache memoizes destination listings for remote conflict detection.
	listCache map[string][]Entry
	// materializedPath, when set, is the local path copyFile reads from instead
	// of op.A.Path — used when a remote source has been downloaded to a temp file.
	materializedPath string
	opID             int

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	cond   *sync.Cond // signaled on Resume/Cancel to wake a paused transfer
	paused bool

	// Progress state is written by the ops goroutine while a transfer runs and
	// read by the UI goroutine to draw it, so it is guarded rather than plain.
	// (It used to be plain fields, which raced: the progress view polled them
	// every frame while the transfer updated them.)
	progMu sync.Mutex
	status Status
	err    error
	total  int64 // bytes the transfer is expected to move
	read   int64 // bytes moved so far
}

// Status reports where the op is in its lifecycle. Safe to read from any
// goroutine.
func (op *Op) Status() Status {
	op.progMu.Lock()
	defer op.progMu.Unlock()
	return op.status
}

// SetStatus records the op's lifecycle state. Only the ops goroutine should
// write it.
func (op *Op) SetStatus(s Status) {
	op.progMu.Lock()
	op.status = s
	op.progMu.Unlock()
}

// Err reports the transfer's failure, or nil. Safe to read from any goroutine.
func (op *Op) Err() error {
	op.progMu.Lock()
	defer op.progMu.Unlock()
	return op.err
}

// SetErr records a transfer failure. Only the ops goroutine should write it.
func (op *Op) SetErr(err error) {
	op.progMu.Lock()
	op.err = err
	op.progMu.Unlock()
}

// TotalSize is the byte count the transfer is expected to move, 0 while that is
// unknown (PctComplete then reports 0).
func (op *Op) TotalSize() int64 {
	op.progMu.Lock()
	defer op.progMu.Unlock()
	return op.total
}

// SetTotalSize sets the expected byte count of the transfer.
func (op *Op) SetTotalSize(n int64) {
	op.progMu.Lock()
	op.total = n
	op.progMu.Unlock()
}

// BytesRead is how many bytes have moved so far.
func (op *Op) BytesRead() int64 {
	op.progMu.Lock()
	defer op.progMu.Unlock()
	return op.read
}

// AddBytesRead accounts for n more bytes transferred.
func (op *Op) AddBytesRead(n int64) {
	op.progMu.Lock()
	op.read += n
	op.progMu.Unlock()
}

// SetBytesRead sets the transferred byte count, for the cases where it is known
// in one step rather than accumulated (a same-filesystem rename).
func (op *Op) SetBytesRead(n int64) {
	op.progMu.Lock()
	op.read = n
	op.progMu.Unlock()
}

// Cancel aborts the operation. In-flight byte transfers stop at their next
// chunk (local copies via the read loop, tie uploads via the progress writer),
// and a queued-but-not-started op is skipped.
func (op *Op) Cancel() {
	if op.cancel != nil {
		op.cancel()
	}
	op.mu.Lock()
	op.paused = false
	op.mu.Unlock()
	if op.cond != nil {
		op.cond.Broadcast()
	}
}

// Pausable reports whether the op supports pausing. Tie imports upload over a
// live HTTP connection that the server may time out if stalled, so they are not
// pausable — only cancellable.
func (op *Op) Pausable() bool { return op.importer == nil }

// Pause suspends an in-flight transfer at its next chunk. No-op for
// non-pausable ops (tie imports) or before the op starts.
func (op *Op) Pause() {
	if !op.Pausable() {
		return
	}
	op.mu.Lock()
	op.paused = true
	op.mu.Unlock()
}

// Resume continues a paused transfer.
func (op *Op) Resume() {
	op.mu.Lock()
	op.paused = false
	op.mu.Unlock()
	if op.cond != nil {
		op.cond.Broadcast()
	}
}

// IsPaused reports whether the op is currently paused.
func (op *Op) IsPaused() bool {
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.paused
}

// wait blocks while the op is paused and returns a non-nil error once the op is
// cancelled. Byte-transfer loops call it before each chunk so pause and cancel
// take effect promptly.
func (op *Op) wait() error {
	if op.ctx == nil {
		return nil
	}
	op.mu.Lock()
	for op.paused && op.ctx.Err() == nil {
		op.cond.Wait()
	}
	op.mu.Unlock()
	return op.ctx.Err()
}

// PctComplete returns copy progress in [0,1].
func (op *Op) PctComplete() float32 {
	total := op.TotalSize()
	if total == 0 {
		return 0
	}
	return float32(op.BytesRead()) / float32(total)
}

func (op *Op) Do() error {
	switch op.OpType {
	case OpCopy:
		return op.doCopy()
	case OpMove:
		return op.doMove()
	case OpExtract:
		return op.doExtract()
	}
	return nil
}

func (op *Op) doCopy() error {
	if op.A.Path == op.B.Path {
		return errors.New("source and destination are identical")
	}
	if err := op.checkContainment(); err != nil {
		return err
	}
	if op.srcFS != nil && op.importer != nil {
		return errors.New("remote-to-remote transfer is not supported")
	}
	if op.importer != nil {
		return op.doImport()
	}
	if op.srcFS != nil {
		return op.doExport()
	}
	proceed, err := op.resolveTopConflict()
	if err != nil {
		return err
	}
	if !proceed {
		op.SetStatus(StatusCompleted)
		return nil
	}
	if op.A.IsDir {
		return copyDir(op)
	}
	if err := copyFile(op, nil); err != nil {
		return err
	}
	op.SetStatus(StatusCompleted)
	return nil
}

// resolveTopConflict checks whether the transfer's top-level destination name
// is occupied and, if so, asks the user what to do (see resolveConflict). The
// rename choice is recorded in op.renameTo. proceed=false means skip.
func (op *Op) resolveTopConflict() (bool, error) {
	destDir, name := op.B.Path, op.A.Name
	if !op.B.IsDir {
		destDir, name = filepath.Dir(op.B.Path), filepath.Base(op.B.Path)
	}
	var resolved string
	var proceed bool
	var err error
	if IsLocal(destDir) {
		resolved, proceed, err = op.checkLocalConflict(destDir, name, op.A)
	} else {
		resolved, proceed, err = op.checkRemoteConflict(destDir, name, op.A)
	}
	if err != nil || !proceed {
		return proceed, err
	}
	op.renameTo = resolved
	return true, nil
}

// doImport copies a local file (or tree) into the tie filesystem via the
// destination backend's Importer. Directories are walked and each file imported
// under the mirrored subpath.
func (op *Op) doImport() error {
	if op.Files != nil {
		return op.importFileList()
	}
	if op.A.IsDir {
		return op.importDir()
	}
	proceed, err := op.resolveTopConflict()
	if err != nil {
		return err
	}
	if !proceed {
		op.SetStatus(StatusCompleted)
		return nil
	}
	op.SetTotalSize(op.A.Size)
	if err := op.importFile(op.B.Path, op.A.Path, op.destName()); err != nil {
		return err
	}
	// A file transfer labels the destination directory itself.
	if err := op.applyDirLabel(op.B.Path, ""); err != nil {
		return err
	}
	op.SetStatus(StatusCompleted)
	return nil
}

// applyDirLabel stamps the op's label (dir-type, tags, album aggregates) onto
// the directory at dirURI; name is the display name recorded for a freshly
// created directory root ("" leaves the directory's name untouched). It is a
// no-op when nothing was requested. A requested dir-type or tags error when
// the backend cannot label directories — the user asked for the label, so
// silently dropping it would be wrong; a name/metadata-only stamp is
// best-effort and skipped on backends without labeling.
func (op *Op) applyDirLabel(dirURI, name string) error {
	label := DirLabel{
		Type:   op.DirType,
		Tags:   op.DirTags,
		Name:   name,
		Artist: op.AlbumArtist,
		Album:  op.AlbumTitle,
		Year:   op.AlbumYear,
	}
	if label.Type == "" && len(label.Tags) == 0 && label.Name == "" &&
		label.Artist == "" && label.Album == "" && label.Year == "" {
		return nil
	}
	labeler, ok := op.importer.(DirLabeler)
	if !ok {
		if label.Type != "" || len(label.Tags) > 0 {
			return errors.New("destination does not support directory types")
		}
		return nil
	}
	return labeler.LabelDir(dirURI, label)
}

// importFile imports a single file through the destination backend, streaming
// upload progress into op.TotalBytesRead when the backend supports it. Backends
// that only implement Importer (no progress) have their bytes counted at the
// end so the bar still reaches 100%. An op carrying tags requires a
// TagImporter backend — silently dropping requested tags would be wrong.
func (op *Op) importFile(destDir, srcPath, name string) error {
	if len(op.Tags) > 0 {
		ti, ok := op.importer.(TagImporter)
		if !ok {
			return errors.New("destination does not support tagging")
		}
		return ti.ImportTagged(destDir, srcPath, name, op.Tags, &progressWriter{op: op})
	}
	if pi, ok := op.importer.(ProgressImporter); ok {
		return pi.ImportWithProgress(destDir, srcPath, name, &progressWriter{op: op})
	}
	before := op.BytesRead()
	if err := op.importer.Import(destDir, srcPath, name); err != nil {
		return err
	}
	if info, err := os.Stat(srcPath); err == nil {
		op.SetBytesRead(before + info.Size())
	}
	return nil
}

// importDir walks a local directory and imports each file into the tie tree
// under B.Path/<dirname>/<relative-subdirs>, mirroring copyDir's placement.
// An ExactDest op (album import) places the tree at B.Path itself.
func (op *Op) importDir() error {
	if !op.ExactDest {
		proceed, err := op.resolveTopConflict()
		if err != nil {
			return err
		}
		if !proceed {
			op.SetStatus(StatusCompleted)
			return nil
		}
	}
	base := op.B.Path + "/" + op.destName()
	if op.ExactDest {
		base = op.B.Path
	}
	// Size the whole tree up front so the progress bar has a fixed denominator
	// and climbs smoothly, rather than jumping as each file's size is discovered.
	if err := filepath.WalkDir(op.A.Path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				op.AddTotalSize(info.Size())
			}
		}
		return nil
	}); err != nil {
		return err
	}
	walk := func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(op.A.Path, srcPath)
		if err != nil {
			return err
		}
		destDir := base
		if sub := filepath.ToSlash(filepath.Dir(rel)); sub != "." {
			destDir += "/" + sub
		}
		srcEntry, _ := statEntry(srcPath)
		name, proceed, err := op.checkRemoteConflict(destDir, filepath.Base(srcPath), srcEntry)
		if err != nil {
			return err
		}
		if !proceed {
			return nil
		}
		return op.importFile(destDir, srcPath, name)
	}
	if err := filepath.WalkDir(op.A.Path, walk); err != nil {
		return err
	}
	// A directory transfer labels the freshly created root directory. The
	// backend creates it on demand, so an empty source tree still gets its
	// (otherwise uncreated) directory labeled.
	if err := op.applyDirLabel(base, path.Base(tiePath(base))); err != nil {
		return err
	}
	op.SetStatus(StatusCompleted)
	return nil
}

// importFileList imports an explicit list of files (an album group's audio
// files and sidecars). Placement follows op.FileSubPaths when the planner
// routed the file (disc-aware cd<N> subdirectories), else each file lands at
// B.Path/<rel-below-A.Path>. Unlike importDir only the listed files transfer
// — unlisted members stay local.
func (op *Op) importFileList() error {
	for _, f := range op.Files {
		if err := op.wait(); err != nil {
			return err
		}
		var destDir, name string
		if sub, ok := op.FileSubPaths[f]; ok {
			destDir = op.B.Path
			if dir := path.Dir(sub); dir != "." {
				destDir += "/" + dir
			}
			name = path.Base(sub)
		} else {
			rel, err := filepath.Rel(op.A.Path, f)
			if err != nil {
				return err
			}
			destDir = op.B.Path
			if sub := filepath.ToSlash(filepath.Dir(rel)); sub != "." {
				destDir += "/" + sub
			}
			name = filepath.Base(f)
		}
		srcEntry, _ := statEntry(f)
		resolved, proceed, err := op.checkRemoteConflict(destDir, name, srcEntry)
		if err != nil {
			return err
		}
		if !proceed {
			continue
		}
		if err := op.importFile(destDir, f, resolved); err != nil {
			return err
		}
	}
	if err := op.applyDirLabel(op.B.Path, path.Base(tiePath(op.B.Path))); err != nil {
		return err
	}
	op.SetStatus(StatusCompleted)
	return nil
}

// doExport copies a tie file (or tree) out to a local destination. Each tie file
// is materialized (downloaded) to a temp file first, then byte-copied to disk.
func (op *Op) doExport() error {
	if op.A.IsDir {
		return op.exportDir()
	}
	proceed, err := op.resolveTopConflict()
	if err != nil {
		return err
	}
	if !proceed {
		op.SetStatus(StatusCompleted)
		return nil
	}
	local, err := op.srcFS.Materialize(op.A)
	if err != nil {
		return err
	}
	op.materializedPath = local
	if err := copyFile(op, nil); err != nil {
		return err
	}
	op.SetStatus(StatusCompleted)
	return nil
}

// exportDir walks a tie directory (via the source FileSystem's List) and exports
// each file under B.Path/<dirname>/<relative-subdirs>, mirroring copyDir.
func (op *Op) exportDir() error {
	proceed, err := op.resolveTopConflict()
	if err != nil {
		return err
	}
	if !proceed {
		op.SetStatus(StatusCompleted)
		return nil
	}
	base := filepath.Join(op.B.Path, op.destName())
	var walk func(dir Entry, relDir string) error
	walk = func(dir Entry, relDir string) error {
		entries, err := op.srcFS.List(dir.Path)
		if err != nil {
			return err
		}
		destDir := filepath.Join(base, relDir)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return err
		}
		for _, child := range entries {
			if child.IsDir {
				if err := walk(child, filepath.Join(relDir, child.Name)); err != nil {
					return err
				}
				continue
			}
			name, proceed, err := op.checkLocalConflict(destDir, child.Name, child)
			if err != nil {
				return err
			}
			if !proceed {
				continue
			}
			local, err := op.srcFS.Materialize(child)
			if err != nil {
				return err
			}
			sub := &Op{
				A:                child,
				B:                Entry{Path: destDir, IsDir: true},
				OpType:           OpCopy,
				materializedPath: local,
				renameTo:         name,
			}
			op.AddTotalSize(child.Size)
			if err := copyFile(sub, op); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(op.A, ""); err != nil {
		return err
	}
	op.SetStatus(StatusCompleted)
	return nil
}

func (op *Op) doMove() error {
	if op.A.Path == op.B.Path {
		return errors.New("source and destination are identical")
	}
	if err := op.checkContainment(); err != nil {
		return err
	}
	if op.srcFS != nil {
		return errors.New("moving out of a remote device is not supported; copy instead")
	}
	if op.importer != nil {
		if err := op.doImport(); err != nil {
			return err
		}
		// A move must not delete what the user skipped in a conflict prompt.
		if op.skipped {
			return nil
		}
		return os.RemoveAll(op.A.Path)
	}
	proceed, err := op.resolveTopConflict()
	if err != nil {
		return err
	}
	if !proceed {
		op.SetStatus(StatusCompleted)
		return nil
	}
	dest := op.destPath()
	// Fast path: a same-filesystem rename moves without copying bytes.
	if err := os.Rename(op.A.Path, dest); err == nil {
		op.SetTotalSize(op.A.Size)
		op.SetBytesRead(op.A.Size)
		op.SetStatus(StatusCompleted)
		return nil
	}
	// Cross-device or otherwise un-renameable: copy then delete the source.
	if op.A.IsDir {
		if err := copyDir(op); err != nil {
			return err
		}
	} else if err := copyFile(op, nil); err != nil {
		return err
	}
	if op.skipped {
		return nil
	}
	if err := os.RemoveAll(op.A.Path); err != nil {
		return err
	}
	op.SetStatus(StatusCompleted)
	return nil
}

// destPath resolves the concrete destination path for A given B (which may be a
// directory to place A into, or an explicit target path).
func (op *Op) destPath() string {
	if op.B.IsDir {
		return filepath.Join(op.B.Path, op.destName())
	}
	if op.renameTo != "" {
		return filepath.Join(filepath.Dir(op.B.Path), op.renameTo)
	}
	return op.B.Path
}

// destName is the name the transfer lands under at the destination: A.Name,
// or the conflict dialog's rename choice.
func (op *Op) destName() string {
	if op.renameTo != "" {
		return op.renameTo
	}
	return op.A.Name
}

// insideOrSame reports whether child names the same location as parent or a
// location below it. Paths are slash-normalised and cleaned, so the check works
// for local paths and tie:/ URIs alike and cannot be slipped by a trailing
// slash or a "./" prefix.
func insideOrSame(parent, child string) bool {
	p := path.Clean(filepath.ToSlash(parent))
	c := path.Clean(filepath.ToSlash(child))
	return c == p || strings.HasPrefix(c, p+"/")
}

// checkContainment rejects a directory transfer whose destination lies inside
// its own source.
//
// Without it, "move root into root/sub" is a data-loss machine: os.Rename
// refuses with EINVAL (POSIX will not move a directory into itself) and the
// error is discarded as "cross-device, fall back to copy", so copyDir walks
// root while writing the copies into root/sub/root/…, and doMove then
// RemoveAll's root — taking the fresh copies with it. The op reports
// StatusCompleted with Err nil and nothing is left. Copying a folder into
// itself is the mirror image: WalkDir descends into the copy it is writing and
// grows the tree until the disk fills.
//
// Only a local→local transfer can contain itself: an import walks a local
// source into a remote namespace and an export downloads a remote tree to a
// local one, so source and destination never name locations in one tree.
func (op *Op) checkContainment() error {
	if !op.A.IsDir || !IsLocal(op.A.Path) || !IsLocal(op.B.Path) {
		return nil
	}
	// Compare absolute paths: a relative source and an absolute destination
	// (or vice versa) name the same tree, and a lexical compare of mixed forms
	// would miss it.
	src, err := filepath.Abs(op.A.Path)
	if err != nil {
		return nil
	}
	dest, err := filepath.Abs(op.destPath())
	if err != nil {
		return nil
	}
	if insideOrSame(src, dest) {
		if op.OpType == OpMove {
			return errors.New("cannot move a folder into itself")
		}
		return errors.New("cannot copy a folder into itself")
	}
	return nil
}

// resolveConflict asks the user what to do about an occupied destination
// name. It returns the name to transfer under and whether to proceed at all.
// A sticky SkipAll/OverwriteAll answer applies without prompting again; a nil
// resolver keeps the historic behaviour (overwrite). Cancel aborts the op.
func (op *Op) resolveConflict(destDir string, src, existing Entry) (string, bool, error) {
	if op.stickySet {
		switch op.sticky {
		case ConflictSkipAll:
			op.skipped = true
			return "", false, nil
		case ConflictOverwriteAll:
			return existing.Name, true, nil
		}
	}
	if op.resolver == nil {
		return existing.Name, true, nil
	}
	res := op.resolver(Conflict{
		Source:   src,
		Existing: existing,
		DestDir:  destDir,
		Multiple: op.multiple,
	})
	switch res.Action {
	case ConflictSkip:
		op.skipped = true
		return "", false, nil
	case ConflictSkipAll:
		op.sticky, op.stickySet = ConflictSkipAll, true
		op.skipped = true
		return "", false, nil
	case ConflictOverwrite:
		return existing.Name, true, nil
	case ConflictOverwriteAll:
		op.sticky, op.stickySet = ConflictOverwriteAll, true
		return existing.Name, true, nil
	case ConflictRename:
		if res.NewName != "" && res.NewName != existing.Name {
			return res.NewName, true, nil
		}
		return existing.Name, true, nil
	default:
		return "", false, errConflictCancelled
	}
}

// checkLocalConflict reports whether destDir/name is occupied on the local
// disk and resolves the conflict if so.
func (op *Op) checkLocalConflict(destDir, name string, src Entry) (string, bool, error) {
	p := filepath.Join(osPath(destDir), name)
	info, err := os.Stat(p)
	if err != nil {
		return name, true, nil // absent or unreadable: proceed
	}
	existing := Entry{Name: name, Path: p, IsDir: info.IsDir(), Size: info.Size(), ModTime: info.ModTime()}
	return op.resolveConflict(destDir, src, existing)
}

// checkRemoteConflict reports whether destDir/name is occupied in the remote
// destination backend and resolves the conflict if so. Listings are memoized
// per op so a directory import lists each destination directory once.
func (op *Op) checkRemoteConflict(destDir, name string, src Entry) (string, bool, error) {
	if op.destFS == nil || IsLocal(destDir) {
		return op.checkLocalConflict(destDir, name, src)
	}
	if op.listCache == nil {
		op.listCache = map[string][]Entry{}
	}
	entries, ok := op.listCache[destDir]
	if !ok {
		var err error
		entries, err = op.destFS.List(destDir)
		if err != nil {
			return name, true, nil // cannot list: proceed (historic behaviour)
		}
		op.listCache[destDir] = entries
	}
	for _, e := range entries {
		if e.Name == name {
			return op.resolveConflict(destDir, src, e)
		}
	}
	return name, true, nil
}

type readerCtx struct {
	r  io.Reader
	op *Op // the active op carrying pause/cancel state (the progress op)
}

func (r *readerCtx) Read(p []byte) (int, error) {
	if err := r.op.wait(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	r.op.AddBytesRead(int64(n))
	return n, err
}

// progressWriter counts bytes uploaded by a ProgressImporter into the op so the
// UI can render a live import progress bar. Returning an error when the op is
// cancelled aborts the upload's request body, stopping the transfer mid-flight.
type progressWriter struct{ op *Op }

func (w *progressWriter) Write(p []byte) (int, error) {
	if err := w.op.wait(); err != nil {
		return 0, err
	}
	w.op.AddBytesRead(int64(len(p)))
	return len(p), nil
}

// RemoveLocal deletes a local file or directory tree.
func RemoveLocal(e Entry) error {
	return os.RemoveAll(osPath(e.Path))
}

func statEntry(path string) (Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		Name:    info.Name(),
		Path:    path,
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}, nil
}

func copyDir(dirOp *Op) error {
	if !dirOp.A.IsDir {
		return errors.New("op is not a directory")
	}
	var ops []*Op
	walk := func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dirOp.A.Path, srcPath)
		if err != nil {
			return err
		}
		destDir := filepath.Join(dirOp.B.Path, dirOp.destName(), filepath.Dir(rel))
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return err
		}
		src, err := statEntry(srcPath)
		if err != nil {
			return err
		}
		dst, err := statEntry(destDir)
		if err != nil {
			return err
		}
		ops = append(ops, &Op{A: src, B: dst, OpType: OpCopy})
		dirOp.AddTotalSize(src.Size)
		return nil
	}
	if err := filepath.WalkDir(dirOp.A.Path, walk); err != nil {
		return err
	}
	for _, op := range ops {
		name, proceed, err := dirOp.checkLocalConflict(op.B.Path, op.A.Name, op.A)
		if err != nil {
			return err
		}
		if !proceed {
			continue
		}
		op.renameTo = name
		if err := copyFile(op, dirOp); err != nil {
			return err
		}
	}
	dirOp.SetStatus(StatusCompleted)
	return nil
}

func copyFile(op *Op, parent *Op) error {
	if op.A.IsDir {
		return errors.New("source is a directory, expected a file: " + op.A.Path)
	}

	// progressOp is the op shown in the ops list; it also carries the pause/
	// cancel state (leaf ops from copyDir have none of their own).
	progressOp := op
	if parent != nil {
		progressOp = parent
	} else {
		op.AddTotalSize(op.A.Size)
	}

	srcPath := op.A.Path
	if op.materializedPath != "" {
		srcPath = op.materializedPath
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dest := op.destPath()
	dst, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer dst.Close()

	written, err := io.Copy(dst, &readerCtx{r: src, op: progressOp})
	if err != nil {
		// Drop the partial destination so a cancelled/failed copy leaves no
		// truncated file behind.
		os.Remove(dest)
		return err
	}
	if written != op.A.Size {
		return errors.New("bytes written (" + strconv.Itoa(int(written)) +
			") differs from source size (" + strconv.Itoa(int(op.A.Size)) + ")")
	}
	return os.Chtimes(dest, time.Now(), op.A.ModTime)
}

// AddTotalSize grows the expected byte count as a walk discovers it.
func (op *Op) AddTotalSize(n int64) {
	op.progMu.Lock()
	op.total += n
	op.progMu.Unlock()
}
