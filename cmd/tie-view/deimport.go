package main

import (
	"fmt"

	"github.com/uidbz/tie/client"
)

// deimportFile removes every metadata triple for hash (filename, media-type,
// tie-type, tags, parent edges, etc.). Tags that end up referenced by nothing
// are also removed from the global "tags" registry and favourites list.
func deimportFile(tc *client.TieClient, hash string) error {
	run := newDeimport(tc)
	if err := run.file(hash); err != nil {
		return err
	}
	return run.finish()
}

// deimportDir recursively removes all metadata for the directory and every
// file/subdirectory it contains, then removes the directory's own triples.
func deimportDir(tc *client.TieClient, uid client.DirUID) error {
	run := newDeimport(tc)
	if err := run.dir(uid); err != nil {
		return err
	}
	return run.finish()
}

// deimportRun carries the state one de-import needs: the directories already
// visited, and the tags seen along the way.
//
// The tag registry used to be cleaned per item, one query per tag per item, so
// de-importing a 500-file tree cost thousands of round-trips and a tag shared
// by two items in the same tree was left behind (each item's query still saw
// the other). Collecting the tags and cleaning once at the end is one query per
// distinct tag and gets the shared case right.
type deimportRun struct {
	tc      *client.TieClient
	visited map[client.DirUID]bool
	tags    map[string]bool
}

func newDeimport(tc *client.TieClient) *deimportRun {
	return &deimportRun{
		tc:      tc,
		visited: map[client.DirUID]bool{},
		tags:    map[string]bool{},
	}
}

func (r *deimportRun) file(hash string) error {
	row, err := r.tc.Get(hash)
	if err != nil {
		return fmt.Errorf("get %s: %w", hash, err)
	}

	b := r.tc.NewBatch()
	for rel, vals := range row.Attributes {
		for _, v := range vals {
			b.Delete(hash, rel, v)
		}
	}
	r.noteTags(row)

	_, err = r.tc.Batch(b)
	return err
}

// dir removes the subtree below uid.
//
// The visited set is a cycle guard: tie's parent graph is a DAG only by
// convention, and a corrupt restore or a leftover duplicate-path edge makes an
// unguarded recursion recurse until the stack overflows — mid-deletion, with
// half the tree already gone.
//
// A failed child aborts the directory's own deletion. Deleting the parent
// triples while a child still points at it would orphan that child: nothing
// would reach it to delete it later.
func (r *deimportRun) dir(uid client.DirUID) error {
	if r.visited[uid] {
		return nil
	}
	r.visited[uid] = true

	dir, err := client.ReadTieDir(r.tc, uid)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", uid, err)
	}

	failed := false
	for _, f := range dir.Files {
		if e := r.file(f.Uid); e != nil {
			log.Error("de-import file", "file", f.Filename, "err", e)
			failed = true
		}
	}
	for _, sub := range dir.SubDirs {
		if e := r.dir(sub.Uid); e != nil {
			log.Error("de-import subdir", "subdir", sub.Uid, "err", e)
			failed = true
		}
	}
	if failed {
		return fmt.Errorf("de-import of %s incomplete: a child failed to delete", uid)
	}

	// Delete the directory node's own triples.
	dirRow, err := r.tc.Get(string(uid))
	if err != nil {
		return fmt.Errorf("get dir row %s: %w", uid, err)
	}
	b := r.tc.NewBatch()
	for rel, vals := range dirRow.Attributes {
		for _, v := range vals {
			b.Delete(string(uid), rel, v)
		}
	}
	r.noteTags(dirRow)

	_, err = r.tc.Batch(b)
	return err
}

// noteTags records the row's tags for the registry cleanup at the end.
func (r *deimportRun) noteTags(row client.Row) {
	for _, tag := range client.RowValues(row, "tag") {
		r.tags[tag] = true
	}
}

// finish drops the tags that nothing references any more from the global tag
// registry and the favourites list.
func (r *deimportRun) finish() error {
	if len(r.tags) == 0 {
		return nil
	}
	b := r.tc.NewBatch()
	for tag := range r.tags {
		if !r.isOrphaned(tag) {
			continue
		}
		b.Delete(string(client.RelTags), string(client.RelAll), tag)
		b.Delete(string(client.RelTags), string(client.RelFavorite), tag) // idempotent; noop if not a favourite
	}
	_, err := r.tc.Batch(b)
	return err
}

// isOrphaned reports whether no item in the store references tag any more.
func (r *deimportRun) isOrphaned(tag string) bool {
	rows, _, err := r.tc.Query(client.QuerySpec{
		Terms:   []string{tag},
		Reverse: true,
		Filter:  "tag",
		Limit:   1,
	})
	if err != nil {
		// Unknown: keep the tag. A query failure must not delete metadata.
		log.Warn("tag reference query", "tag", tag, "err", err)
		return false
	}
	return len(rows) == 0
}
