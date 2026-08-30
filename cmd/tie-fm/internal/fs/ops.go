package fs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
)

type OpType int

const (
	OpCopy OpType = iota
	OpMove
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
	queued        chan *Op
	active        []*Op
	reg           *Registry
	ActiveChanged func()
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

func (o *Operations) run() {
	var opID int
	for op := range o.queued {
		op.opID = opID
		opID++
		o.active = append(o.active, op)
		op.StartTime = time.Now()
		o.ActiveChanged()
		if err := op.ctx.Err(); err != nil {
			// Cancelled while still queued: skip the transfer entirely.
			op.Status = StatusError
			op.Err = err
		} else {
			op.Status = StatusRunning
			if err := op.Do(); err != nil {
				op.Status = StatusError
				op.Err = err
			}
		}
		o.remove(op.opID)
		if op.OnComplete != nil {
			op.OnComplete(op)
		}
	}
}

func (o *Operations) Active() []*Op { return o.active }

func (o *Operations) remove(opID int) {
	for i, x := range o.active {
		if x.opID == opID {
			o.active = slices.Delete(o.active, i, i+1)
			o.ActiveChanged()
			return
		}
	}
}

// Copy enqueues a copy of source into dest. done, if non-nil, runs after the op
// finishes (success or error), from the ops goroutine — wrap UI work in fyne.Do.
func (o *Operations) Copy(source, dest Entry, done func(*Op)) *Op {
	op := o.newOp(source, dest, OpCopy, done)
	o.queued <- op
	return op
}

// Move enqueues a move of source into dest. See Copy for the done callback.
func (o *Operations) Move(source, dest Entry, done func(*Op)) *Op {
	op := o.newOp(source, dest, OpMove, done)
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
		OnComplete: done,
		ctx:        ctx,
		cancel:     cancel,
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
	A              Entry
	B              Entry
	OpType         OpType
	TotalSize      int64
	TotalBytesRead int64
	StartTime      time.Time
	Status         Status
	Err            error

	OnComplete func(*Op) // optional; called after the op finishes (ok or error)

	importer Importer   // non-nil when the destination is a remote backend (tie, mtp)
	srcFS    FileSystem // non-nil when the source is a remote backend (export)
	// materializedPath, when set, is the local path copyFile reads from instead
	// of op.A.Path — used when a remote source has been downloaded to a temp file.
	materializedPath string
	opID             int

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	cond   *sync.Cond // signaled on Resume/Cancel to wake a paused transfer
	paused bool
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
	if op.TotalSize == 0 {
		return 0
	}
	return float32(op.TotalBytesRead) / float32(op.TotalSize)
}

func (op *Op) Do() error {
	switch op.OpType {
	case OpCopy:
		return op.doCopy()
	case OpMove:
		return op.doMove()
	}
	return nil
}

func (op *Op) doCopy() error {
	if op.A.Path == op.B.Path {
		return errors.New("source and destination are identical")
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
	if op.A.IsDir {
		return copyDir(op)
	}
	if err := copyFile(op, nil); err != nil {
		return err
	}
	op.Status = StatusCompleted
	return nil
}

// doImport copies a local file (or tree) into the tie filesystem via the
// destination backend's Importer. Directories are walked and each file imported
// under the mirrored subpath.
func (op *Op) doImport() error {
	if op.A.IsDir {
		return op.importDir()
	}
	op.TotalSize = op.A.Size
	if err := op.importFile(op.B.Path, op.A.Path, op.A.Name); err != nil {
		return err
	}
	op.Status = StatusCompleted
	return nil
}

// importFile imports a single file through the destination backend, streaming
// upload progress into op.TotalBytesRead when the backend supports it. Backends
// that only implement Importer (no progress) have their bytes counted at the
// end so the bar still reaches 100%.
func (op *Op) importFile(destDir, srcPath, name string) error {
	if pi, ok := op.importer.(ProgressImporter); ok {
		return pi.ImportWithProgress(destDir, srcPath, name, &progressWriter{op: op})
	}
	before := op.TotalBytesRead
	if err := op.importer.Import(destDir, srcPath, name); err != nil {
		return err
	}
	if info, err := os.Stat(srcPath); err == nil {
		op.TotalBytesRead = before + info.Size()
	}
	return nil
}

// importDir walks a local directory and imports each file into the tie tree
// under B.Path/<dirname>/<relative-subdirs>, mirroring copyDir's placement.
func (op *Op) importDir() error {
	base := op.B.Path + "/" + op.A.Name
	// Size the whole tree up front so the progress bar has a fixed denominator
	// and climbs smoothly, rather than jumping as each file's size is discovered.
	if err := filepath.WalkDir(op.A.Path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				op.TotalSize += info.Size()
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
		return op.importFile(destDir, srcPath, filepath.Base(srcPath))
	}
	if err := filepath.WalkDir(op.A.Path, walk); err != nil {
		return err
	}
	op.Status = StatusCompleted
	return nil
}

// doExport copies a tie file (or tree) out to a local destination. Each tie file
// is materialized (downloaded) to a temp file first, then byte-copied to disk.
func (op *Op) doExport() error {
	if op.A.IsDir {
		return op.exportDir()
	}
	local, err := op.srcFS.Materialize(op.A)
	if err != nil {
		return err
	}
	op.materializedPath = local
	if err := copyFile(op, nil); err != nil {
		return err
	}
	op.Status = StatusCompleted
	return nil
}

// exportDir walks a tie directory (via the source FileSystem's List) and exports
// each file under B.Path/<dirname>/<relative-subdirs>, mirroring copyDir.
func (op *Op) exportDir() error {
	base := filepath.Join(op.B.Path, op.A.Name)
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
			local, err := op.srcFS.Materialize(child)
			if err != nil {
				return err
			}
			sub := &Op{
				A:                child,
				B:                Entry{Path: destDir, IsDir: true},
				OpType:           OpCopy,
				materializedPath: local,
			}
			op.TotalSize += child.Size
			if err := copyFile(sub, op); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(op.A, ""); err != nil {
		return err
	}
	op.Status = StatusCompleted
	return nil
}

func (op *Op) doMove() error {
	if op.A.Path == op.B.Path {
		return errors.New("source and destination are identical")
	}
	if op.srcFS != nil {
		return errors.New("moving out of a remote device is not supported; copy instead")
	}
	if op.importer != nil {
		if err := op.doImport(); err != nil {
			return err
		}
		return os.RemoveAll(op.A.Path)
	}
	dest := op.destPath()
	// Fast path: a same-filesystem rename moves without copying bytes.
	if err := os.Rename(op.A.Path, dest); err == nil {
		op.TotalSize = op.A.Size
		op.TotalBytesRead = op.A.Size
		op.Status = StatusCompleted
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
	if err := os.RemoveAll(op.A.Path); err != nil {
		return err
	}
	op.Status = StatusCompleted
	return nil
}

// destPath resolves the concrete destination path for A given B (which may be a
// directory to place A into, or an explicit target path).
func (op *Op) destPath() string {
	if op.B.IsDir {
		return filepath.Join(op.B.Path, op.A.Name)
	}
	return op.B.Path
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
	r.op.TotalBytesRead += int64(n)
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
	w.op.TotalBytesRead += int64(len(p))
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
		destDir := filepath.Join(dirOp.B.Path, dirOp.A.Name, filepath.Dir(rel))
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
		dirOp.TotalSize += src.Size
		return nil
	}
	if err := filepath.WalkDir(dirOp.A.Path, walk); err != nil {
		return err
	}
	for _, op := range ops {
		if err := copyFile(op, dirOp); err != nil {
			return err
		}
	}
	dirOp.Status = StatusCompleted
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
		op.TotalSize += op.A.Size
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
