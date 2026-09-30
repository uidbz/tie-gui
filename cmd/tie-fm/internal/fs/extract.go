package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mholt/archives"
)

// archiveExts are the (lowercase) filename suffixes tie-fm offers to extract,
// longest first so multi-part suffixes (".tar.gz") win over their tail
// (".gz" is not listed: a bare compressed file is not an archive). Detection
// is by name because remote entries (tie, mtp) cannot be sniffed without a
// download; the extraction itself identifies the format from content.
var archiveExts = []string{
	".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".tar.lz4", ".tar.br", ".tar.sz", ".tar.lz",
	".tgz", ".tbz2", ".tbz", ".txz", ".tzst",
	".zip", ".cbz", ".rar", ".cbr", ".7z", ".cb7", ".tar", ".cbt",
}

// IsArchiveName reports whether name looks like an archive tie-fm can extract.
func IsArchiveName(name string) bool {
	return archiveSuffix(name) != ""
}

func archiveSuffix(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range archiveExts {
		if strings.HasSuffix(lower, ext) && len(lower) > len(ext) {
			return ext
		}
	}
	return ""
}

// archiveStem is the archive name without its archive suffix ("a.tar.gz" →
// "a"), used to name the wrapping folder of a multi-entry archive.
func archiveStem(name string) string {
	if ext := archiveSuffix(name); ext != "" {
		return name[:len(name)-len(ext)]
	}
	return name
}

// Extract enqueues the extraction of archive into destDir (the directory
// holding it, for "extract here"). Either side may be remote: a remote
// archive (tie, mtp) is materialized first, and a remote destination receives
// the members through its Importer. Placement follows the usual "extract
// here" convention (see extractPlan): an archive with a single top-level
// entry extracts as-is, anything else lands in a folder named after the
// archive, and a name already present in destDir gets a " (N)" suffix so
// nothing existing is overwritten. See Copy for the done callback.
func (o *Operations) Extract(archive, destDir Entry, done func(*Op)) *Op {
	op := o.newOp(archive, destDir, OpExtract, done)
	if o.reg != nil {
		op.destFS = o.reg.For(destDir.Path)
	}
	o.queued <- op
	return op
}

// archiveMember is one extractable entry of an archive, by its sanitized
// slash-separated path inside the archive.
type archiveMember struct {
	name  string
	size  int64
	isDir bool
}

// cleanMemberPath sanitizes an archive member path: backslashes are
// normalized, and absolute paths or paths escaping the extraction root
// ("zip slip") are rejected by returning "".
func cleanMemberPath(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") {
		return ""
	}
	name = path.Clean(name)
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return ""
	}
	return name
}

// extractPlan decides where the members land below the destination directory
// and returns a mapping function from member path to destination-relative
// path. existing holds the names already present in the destination.
//
//   - one top-level entry (a folder, or a single file): extracted as-is, the
//     top-level name made unique against existing;
//   - several top-level entries: wrapped in a folder named after the archive
//     (made unique), so extracting never scatters files into the directory.
func extractPlan(archiveName string, members []archiveMember, existing map[string]bool) func(string) string {
	tops := map[string]bool{}
	for _, m := range members {
		top, _, _ := strings.Cut(m.name, "/")
		tops[top] = true
	}
	if len(tops) == 1 {
		var top string
		for t := range tops {
			top = t
		}
		unique := uniqueName(top, existing)
		return func(name string) string {
			_, rest, found := strings.Cut(name, "/")
			if !found {
				return unique
			}
			return unique + "/" + rest
		}
	}
	stem := archiveStem(archiveName)
	if stem == "" {
		stem = "extracted"
	}
	wrap := uniqueName(stem, existing)
	return func(name string) string { return wrap + "/" + name }
}

// uniqueName returns name, or "name (N)" (before the extension for files:
// "a (2).txt") with the smallest N ≥ 2 not in existing.
func uniqueName(name string, existing map[string]bool) string {
	if !existing[name] {
		return name
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if base == "" { // dot-file such as ".config": no extension split
		base, ext = name, ""
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, n, ext)
		if !existing[candidate] {
			return candidate
		}
	}
}

// openArchive opens the local archive file and identifies an extractor for it.
func openArchive(ctx context.Context, local, name string) (*os.File, archives.Extractor, error) {
	f, err := os.Open(local)
	if err != nil {
		return nil, nil, err
	}
	format, _, err := archives.Identify(ctx, name, f)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("%s: not a supported archive: %w", name, err)
	}
	ex, ok := format.(archives.Extractor)
	if !ok {
		f.Close()
		return nil, nil, fmt.Errorf("%s: not an extractable archive", name)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, ex, nil
}

// walkArchive runs fn over every regular file and directory member (sanitized
// path), skipping links, devices and unsafe paths.
func walkArchive(ctx context.Context, local, name string, fn func(m archiveMember, info archives.FileInfo) error) error {
	f, ex, err := openArchive(ctx, local, name)
	if err != nil {
		return err
	}
	defer f.Close()
	return ex.Extract(ctx, f, func(ctx context.Context, info archives.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		clean := cleanMemberPath(info.NameInArchive)
		if clean == "" {
			return nil
		}
		mode := info.Mode()
		if !mode.IsDir() && !mode.IsRegular() {
			return nil // symlinks, devices: never materialized
		}
		return fn(archiveMember{name: clean, size: info.Size(), isDir: mode.IsDir()}, info)
	})
}

// doExtract materializes the archive when remote, scans its members to plan
// placement and size the progress bar, then streams every member into the
// destination: written directly for a local destination, staged to a temp
// file and imported for a remote one.
func (op *Op) doExtract() error {
	local := osPath(op.A.Path)
	if op.srcFS != nil {
		m, err := op.srcFS.Materialize(op.A)
		if err != nil {
			return err
		}
		local = m
	}

	// Pass 1: the member list (headers only) for placement and total size.
	var members []archiveMember
	if err := walkArchive(op.ctx, local, op.A.Name, func(m archiveMember, _ archives.FileInfo) error {
		members = append(members, m)
		if !m.isDir {
			op.TotalSize += m.size
		}
		return nil
	}); err != nil {
		return err
	}
	if len(members) == 0 {
		return errors.New(op.A.Name + ": archive is empty")
	}

	existing := map[string]bool{}
	if op.destFS != nil {
		entries, err := op.destFS.List(op.B.Path)
		if err != nil {
			return err
		}
		for _, e := range entries {
			existing[e.Name] = true
		}
	}
	place := extractPlan(op.A.Name, members, existing)

	var staging string
	if op.importer != nil {
		d, err := os.MkdirTemp("", "tie-fm-extract-")
		if err != nil {
			return err
		}
		staging = d
		defer os.RemoveAll(staging)
	}

	// Pass 2: write the members.
	err := walkArchive(op.ctx, local, op.A.Name, func(m archiveMember, info archives.FileInfo) error {
		rel := place(m.name)
		if op.importer != nil {
			return op.importMember(rel, m, info, staging)
		}
		return op.writeMember(filepath.Join(osPath(op.B.Path), filepath.FromSlash(rel)), m, info)
	})
	if err != nil {
		return err
	}
	op.Status = StatusCompleted
	return nil
}

// writeMember extracts one member onto the local disk at dest.
func (op *Op) writeMember(dest string, m archiveMember, info archives.FileInfo) error {
	if m.isDir {
		return os.MkdirAll(dest, 0755)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	if err := op.copyMember(dest, info, true); err != nil {
		return err
	}
	return os.Chtimes(dest, time.Now(), info.ModTime())
}

// importMember stages one member in a temp file and imports it into the
// remote destination at B.Path/<dir of rel>. Directory members are skipped:
// remote backends create directories on demand when a file lands in them.
func (op *Op) importMember(rel string, m archiveMember, info archives.FileInfo, staging string) error {
	if m.isDir {
		return nil
	}
	tmp := filepath.Join(staging, "member")
	if err := op.copyMember(tmp, info, false); err != nil {
		return err
	}
	defer os.Remove(tmp)
	destDir := op.B.Path
	if dir := path.Dir(rel); dir != "." {
		destDir = strings.TrimSuffix(destDir, "/") + "/" + dir
	}
	return op.importFile(destDir, tmp, path.Base(rel))
}

// copyMember writes a member's bytes to dest. count selects whether the bytes
// advance the op's progress (local extraction); a remote import counts its
// upload instead, so the staging copy must not count twice.
func (op *Op) copyMember(dest string, info archives.FileInfo, count bool) error {
	src, err := info.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()|0600)
	if err != nil {
		return err
	}
	var r io.Reader = src
	if count {
		r = &readerCtx{r: src, op: op}
	} else {
		r = &ctxReader{r: src, op: op}
	}
	if _, err := io.Copy(dst, r); err != nil {
		dst.Close()
		os.Remove(dest)
		return err
	}
	return dst.Close()
}

// ctxReader honours pause/cancel like readerCtx without counting progress.
type ctxReader struct {
	r  io.Reader
	op *Op
}

func (r *ctxReader) Read(p []byte) (int, error) {
	if err := r.op.wait(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
