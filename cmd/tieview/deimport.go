package main

import (
	"fmt"

	"git.sr.ht/~uid/tie/client"
)

// deimportFile removes every metadata triple for hash (filename, media-type,
// tie-type, tags, parent edges, etc.). Tags that are not used by any other
// item are also removed from the global "tags" registry and favourites list.
func deimportFile(tc *client.TieClient, hash string) error {
	row, err := tc.Get(hash)
	if err != nil {
		return fmt.Errorf("get %s: %w", hash, err)
	}

	b := tc.NewBatch()
	for rel, vals := range row.Attributes {
		for _, v := range vals {
			b.Delete(hash, rel, v)
		}
	}

	// Clean up the global tag registry for any tags that become orphaned.
	for _, tag := range orphanedTags(tc, client.RowValues(row, "tag")) {
		b.Delete("tags", "all", tag)
		b.Delete("tags", "favorite", tag) // idempotent; noop if not a favourite
	}

	_, err = tc.Batch(b)
	return err
}

// deimportDir recursively removes all metadata for the directory and every
// file/subdirectory it contains, then removes the directory's own triples.
func deimportDir(tc *client.TieClient, uid client.DirUID) error {
	dir, err := client.ReadTieDir(tc, uid)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", uid, err)
	}

	for _, f := range dir.Files {
		if e := deimportFile(tc, f.Uid); e != nil {
			fmt.Printf("de-import file %q: %v\n", f.Filename, e)
		}
	}
	for _, sub := range dir.SubDirs {
		if e := deimportDir(tc, sub.Uid); e != nil {
			fmt.Printf("de-import subdir %s: %v\n", sub.Uid, e)
		}
	}

	// Delete the directory node's own triples.
	dirRow, err := tc.Get(string(uid))
	if err != nil {
		return fmt.Errorf("get dir row %s: %w", uid, err)
	}
	b := tc.NewBatch()
	for rel, vals := range dirRow.Attributes {
		for _, v := range vals {
			b.Delete(string(uid), rel, v)
		}
	}
	for _, tag := range orphanedTags(tc, client.RowValues(dirRow, "tag")) {
		b.Delete("tags", "all", tag)
		b.Delete("tags", "favorite", tag)
	}
	_, err = tc.Batch(b)
	return err
}

// orphanedTags returns the subset of tags that are referenced by at most one
// item in the store — i.e. they will become unused once that item is removed.
func orphanedTags(tc *client.TieClient, tags []string) []string {
	var result []string
	for _, tag := range tags {
		rows, _, err := tc.Query(client.QuerySpec{
			Terms:   []string{tag},
			Reverse: true,
			Filter:  "tag",
			Limit:   2,
		})
		if err == nil && len(rows) <= 1 {
			result = append(result, tag)
		}
	}
	return result
}
