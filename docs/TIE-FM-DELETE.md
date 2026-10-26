# tie-fm delete on `tie:` entries

`Delete` is available for `tie:` rows (context menu) and removes **references**,
never bytes. It acts on the whole selection when the clicked row is part of it:
each item is removed in turn (off the UI goroutine), the pane reloads once, and
any failures are reported together:

- **File / archive:** removes the `(hash, "parent", <dir>)` edge. If that was
  the hash's last parent edge, its per-hash metadata (filename, name, tie-type,
  tags, media metadata) is removed too. The content blob stays in the
  filehost.
- **Directory:** children are unlinked first (files/archives, then subdirs
  bottom-up), and empty directory nodes lose their parent edge and `path`
  triples. Metadata is dropped when the last parent is gone. A child linked
  under another directory is only unlinked from the deleted branch.
- **Tag-query rows:** a query has no containing directory, so the row's path is
  synthetic (`tie:/<name>`). Delete uses the subject's recorded parent edges:
  if the path resolves to one of them, only that edge is removed; otherwise
  every parent edge is removed. This mirrors "delete this item" from a search
  result rather than "unlink from this folder".

The pinned tie client (v0.7.4) exposes `client.DeleteFile` /
`client.DeleteDir`, which `TieFS` delegates to in `internal/fs/tie.go`
(`detachChild`, `deleteDir`) and follows with `Sync` so the unlink is durable
before the caller moves on. The shared-content guard is preserved: metadata
is only dropped when no parent edge remains, so the same content filed in two
directories survives a delete from one of them. Dropping the last parent also
drops the subject's other attributes (tags, `member-of`, media metadata).
A directory reachable from two parents is emptied when either branch is
deleted (the children are the same nodes); unlink it instead if that is not
wanted.

The blob is intentionally left alone. `cmd/tie-filehost`'s retention reaper
owns bytes: it deletes expired blobs unless they are still reachable from a
live directory manifest in any store. Tree deletes do not rewrite those
manifests, so the reaper cannot know that a triple-only delete made a blob
unreferenced. That is the missing piece for automatic space reclamation.

## Reference counting / garbage collection (future work)

A content hash can be reached through several graph edges; `parent` count is
not the whole story:

1. **Tree edges** — `(hash, "parent", DirUID)` for files and archives, and
   `(DirUID, "parent", parentDirUID)` for directories.
2. **Directory manifest blobs** — `(DirUID, "tiedir-hash", manifestHash)`;
   the manifest blob itself lists child hashes. The filehost reaper walks these
   manifests to protect children, independent of the triplestore.
3. **Version history** — `_prev` collections intentionally keep old content
   hashes (`<name>_prev` dirs / `version-of`), even after the current name is
   deleted.
4. **Derived relations** — `(hash, "thumbnail", thumbHash)`, dimensions,
   ratings, cover-art relations, and `member-of` edges (albums, playlists).
5. **Play history / stats** — play events reference track hashes; deleting a
   track should probably keep the history (or write a tombstone) rather than
   rewrite user activity.
6. **Metadata alone is not a reference** — tags/title/artist triples describe a
   hash but do not make its bytes reachable.

Implications:

- **Prefer reachability/mark-and-sweep over reference counters.** Counters
  drift on partial batches, crashes, manual `tie add`/`del`, and multiple
  collections; a sweep can be rerun and verified.
- **Scope matters.** A hash can appear in several collections (and versions/
  thumbnails). A GC in one collection must decide whether references from
  another collection protect the blob. Start with a global sweep across all
  reachable triplestores, or make the scope explicit (`--collection`).
- **The filehost has no delete API today**, only retention (finite or infinite)
  and the reaper. There are two viable directions:
  1. Add a filehost admin operation for "delete this unreferenced hash" (and
     dry-run / report mode), then have a `tie gc` command compute reachable
     hashes from the triplestore and submit the complement.
  2. Keep bytes entirely under filehost retention. This is only safe if
     imports use finite retention and users accept that a still-referenced
     blob can expire unless its manifest is live. It also cannot target the
     space of deleted items specifically.
- **Stale directory manifests can pin blobs forever.** The reaper treats every
  non-expired manifest as a live root. A real GC needs to drop manifests that
  no `tiedir-hash` triple references before sweeping their children, or the
  filehost needs a triplestore-aware reachability feed (unlikely — it stays
  content-only by design).
- **Safety rails:** a dry-run report (`would free N blobs / M bytes`), an
  explicit confirmation, a `tie dump` backup recommendation, and perhaps a
  quarantine period before bytes are removed. Deleting the last triple should
  never synchronously delete bytes from a UI action.
- **Deletion semantics to settle first:** should deleting a hash drop
  thumbnails, cover-art and `member-of` edges eagerly, or leave them for GC?
  Should version history be kept when the live file is deleted? Should play
  history be anonymized/tombstoned? These choices determine what the sweep
  considers reachable.

Until that exists, `tie-fm` delete is deliberately "delete the triples, leave
the blob", which is the safe behaviour for shared content and matches the
current filehost model.
