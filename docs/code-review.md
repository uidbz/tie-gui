# tie-gui — code review, risk map and simplification options

Review date: 2026-10-17 · Commit reviewed: `dd6a23c` (master) · ~46k lines of Go
(173 files; `third_party/fyne` excluded), 60 test files.

This is a whole-repository review of the GUI monorepo: correctness, data safety,
concurrency, resource use, security, API/package design, desktop-app fit, and
options for refactoring and simplification. Every finding names a file and line,
a concrete failure scenario, and a recommended fix. Where several fixes are
possible the recommended one is marked **(recommended)**.

## How to read this

- **Severity**: `CRITICAL` (data loss / crash / wedges the app), `HIGH`
  (silently wrong results or likely data damage), `MEDIUM` (real bug in a
  narrower scenario, or a serious latent risk), `LOW` (cleanup, polish, minor
  inefficiency).
- **Evidence**:
  - **[reproduced]** — confirmed with a throwaway program or test during the
    review (reproducers are in the [appendix](#appendix-a--reproducers); none
    were committed).
  - **[code-read]** — follows directly from reading the code; not executed.
- `go build ./...` and `go vet ./...` are clean.
- **`go test -race ./...` is not clean.** Without `-race` every package passes.
  With `-race`, `gallery` fails 8 tests with **39 data races** and
  `cmd/tie-fm/internal/ui` fails `TestPreviewTapAssociation`. Most of those
  reports come from the *test harness* driving Fyne layout off the UI goroutine
  (the software test driver runs `fyne.Do` inline), so they are not all
  production bugs — but they mean the package's central threading invariant
  ("tile and grid state is touched only on the UI goroutine") is enforced by
  nothing, and the genuine races in
  [§4.1](#41-the-imageinfo-threading-contract-is-unenforced) live in the same
  code. See [§10](#10-tooling-and-process).
- The apps pin `github.com/uidbz/tie` **v0.6.0** and `github.com/uidbz/pwplay`
  **v0.3.1**. Several findings are *inherited* from those modules but land on
  tie-gui users, and several have a tie-gui-side mitigation; each is marked
  **(inherits: tie)** where the root fix belongs upstream.

---

## 1. Executive summary

The GUI code is unusually well commented and the hard-won concurrency lessons
are visible: the `placeMu` / install-before-enqueue ordering in `PlaceTiles`, the
`Info`-pointer staleness guard in the tile updater, the at-least-once spool +
cursor design of the play-history recorder, the settle-waits around the pwplay
queue, and the consistent `go func() { … fyne.Do(…) }` discipline in tie-fm and
tie-audio's UI layers. Archive extraction sanitizes member paths and refuses
symlinks; command launching never goes through a shell.

The biggest remaining risks are in five places: **the tag-search trie**
(memory and Unicode), **tie-fm's move engine** (self-containment and
progress-state races), **the gallery's UI-thread/network coupling**, **the
credential-bearing config file**, and **the absence of any timeout or
cancellation on the store clients**.

### Top 12, in priority order

| # | Sev | Finding | Where | Issue |
|---|-----|---------|-------|-------|
| 1 | CRITICAL | Moving a directory into one of its own subdirectories **deletes the whole tree and reports success** **[reproduced]** | `cmd/tie-fm/internal/fs/ops.go:788-811`, `966-1016` | #2 |
| 2 | CRITICAL | The tag trie allocates a 256-way node per character: **20k tags → 386k nodes → 901 MB**, rebuilt on the UI goroutine on every tag click **[reproduced]** | `tagselection/trie/trie.go:37-40,62-86`; `tagselection/tagselection.go:662-674`; `cmd/tie-view/main.go:716-718,769-771` | #7 |
| 3 | CRITICAL | `KeysWithPrefix` rebuilds keys with `%c` on a **byte**, so every non-ASCII tag is mangled — `café` → `cafÃ©`, `日本語` → mojibake — displayed that way and written back to the store **[reproduced]** | `tagselection/trie/trie.go:113`; `tagselection/tagselection.go:128-135` | #3 |
| 4 | HIGH | `Operations.Active()` is read by the UI while the ops goroutine appends to and `slices.Delete`s the same slice **[reproduced]** | `cmd/tie-fm/internal/fs/ops.go:99,113,120,125` | #10 |
| 5 | HIGH | The settings editor writes the tie config — plaintext filehost and triplestore **passwords** — with mode `0744`: world-readable, and executable **[reproduced]** | `tieconfig/editor.go:203` → `uidbz/conf` `conf.go:207` | #5 |
| 6 | HIGH | `LoadGallery`/`ChangePage` **block the UI goroutine on network I/O**, and no store client anywhere has a timeout or context: a stalled triplestore or filehost freezes the app with no way out **[code-read]** | `gallery/gallery.go:657,716,738`; `tie/client/tie.go:297-309` (no `Timeout`) | #11, 12 |
| 7 | HIGH | `TieFS.Materialize` treats a **partially downloaded** temp file as complete (the download is not atomic) and never cleans the temp dir; `tmpDir` itself is written from several goroutines **[code-read]** | `cmd/tie-fm/internal/fs/tie.go:114-133`; `tie/io/getlib/getlib.go:92-111` | #9 |
| 8 | HIGH | `Materialize` builds its temp path from a **store-supplied filename**: `filepath.Join(tmpDir, hash+"-"+e.Name)` — a `filename` triple containing `../` writes outside the temp dir **[code-read]** | `cmd/tie-fm/internal/fs/tie.go:125` | #9 |
| 9 | HIGH | Directory listings silently truncate at **1000 children**: `ReadTieDir` issues a query with no `Limit`, which tie v0.6.0 caps at 1000, and discards `TotalCount` so the GUI cannot even detect it **[code-read]** (inherits: tie) | `tie/client/tag.go:1033` + `tie/tiedb/collection.go:808`; callers `cmd/tie-view/tree.go:203,271`, `cmd/tie-fm/internal/fs/tie.go:69`, `cmd/tie-audio/internal/data/albums.go:200,683,706` | #8 |
| 10 | HIGH | `gallery.Decode` has **no size or dimension limit**: one crafted archive member or tie blob (a 40 000×40 000 PNG) OOM-kills the viewer while thumbnailing **[code-read]** | `gallery/helper.go:27-59`; `gallery/tilelayout.go:1093-1101` | #4 |
| 11 | HIGH | The tile cache is keyed by `Path`, so two entries with the **same content hash** get the *same* `*Tile` widget installed in two grid slots — one Fyne object, two parents **[code-read]** | `gallery/tilelayout.go:494-495,559-561` | #14 |
| 12 | HIGH | `mpvplayer`: `Close()` frees the render context and mpv handle while the GL thread can be inside `RenderInto`, and `RenderInto`/`command` do not check for a closed handle → segfault on window close **[code-read]** | `mpvplayer/mpv.go:204-210,262-279,320-330` | #13 |

The rest of the document expands on these and on ~50 further findings, followed
by a desktop-app conventions review ([§6](#6-desktop-app-conventions-review)),
refactoring options ([§7](#7-refactoring-options)), simplifications
([§8](#8-simplification-options)), a risk map
([§9](#9-risk-map-where-bugs-are-likely-to-live)) and a suggested order of work
([§11](#11-suggested-roadmap)).

---

## 2. Critical findings

### 2.1 CRITICAL — Moving a directory into its own subdirectory deletes it  **[reproduced]**

`cmd/tie-fm/internal/fs/ops.go:763-812` (`doMove`), with `copyDir` at `:966-1016`.

`doMove` has no containment check. The sequence for "move `root` into
`root/sub`":

1. `op.A.Path == op.B.Path` (`:764`) does not catch it — the paths differ.
2. The same-filesystem fast path `os.Rename(op.A.Path, dest)` (`:790`) fails with
   `EINVAL` (POSIX refuses to move a directory into itself), and the error is
   **discarded** — the code treats any rename failure as "cross-device, fall
   back to copy".
3. `copyDir(op)` (`:798`) walks `A` and copies every file into
   `A/sub/root/…` — i.e. **inside the source tree**.
4. `os.RemoveAll(op.A.Path)` (`:807`) then deletes the source — taking the
   freshly written copies with it.

Reproducer ([A.1](#a1-move-a-directory-into-its-own-subdirectory)): a tree of 7
entries, `Move(root, root/sub)`. Result: `Status=StatusCompleted`, `Err=nil`,
and the tree is **gone** — one entry left (the now-empty `root`). The user gets
a success notification and no data.

The same missing check applies to `Copy` in the other direction: copying a tree
into itself grows it until the disk fills (`copyDir`'s `WalkDir` descends into
the copy it is writing).

**Fix (recommended):** one guard in `Operations.newOp`/`Do`, used by every
transfer path:

```go
// sameOrInside reports whether child is path or lives below it.
func sameOrInside(parent, child string) bool {
    p, c := filepath.Clean(parent), filepath.Clean(child)
    if p == c { return true }
    rel, err := filepath.Rel(p, c)
    return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
```

`doMove` returns `errors.New("cannot move a folder into itself")` when
`sameOrInside(op.A.Path, dest)`; `doCopy` returns the same for a directory
source. Do it on **absolute, cleaned** paths, and also reject the case where the
destination *directory* is inside the source. Add the reproducer as a
regression test, plus a `Copy`-into-itself variant. Note the tie backend needs
the equivalent check on `tie:` URIs (`path`-based, not `filepath`).

### 2.2 CRITICAL — The tag trie costs ~45 KB per tag and is rebuilt on the UI goroutine  **[reproduced]**

`tagselection/trie/trie.go:37-40` (`newNode`), `:62-86` (`Put`/`put`).

```go
const radix = 256
func newNode() *Node { return &Node{links: make([]*Node, radix)} }
```

Every node eagerly allocates 256 pointers (2 KB). Measured
([A.2](#a2-trie-memory)): **20 000 realistic tag names → 385 786 nodes →
901 MB heap**. That is ~45 KB per tag.

This is not a synthetic worst case. `cmd/tie-view/main.go:716-718` loads **every
tag in the collection** into the trie at start-up (`ts.AddTag(tag)` per tag from
`(tags, all)`), and `cmd/tie-view/main.go:769-771` and `:783-787` **throw the
trie away and rebuild it on the UI goroutine on every tag selection**
(`ClearAllTags()` then a `Put` loop inside `fyne.Do`). tie-fm's tag panel
(`internal/ui/tagpanel.go:131-132`) and tie-audio's browser
(`internal/ui/browse.go:591,637,651`) do the same. A photo library with a few
thousand people/place/event tags already costs hundreds of megabytes; a large
one OOMs the app, and every click pays a full rebuild plus a GC storm.

`collect` (`:98-117`) compounds it: it walks all 256 child slots at every node
and rebuilds the key with `fmt.Sprintf("%s%c", prefix, c)` — one allocation and
one string copy per node, so a prefix search over N tags is O(256·nodes) with
quadratic string building.

**Fix (recommended):** delete the trie and use a sorted slice plus binary
search:

```go
type tagIndex struct {
    tags []string          // original case, sorted by lowercase form
    lower []string         // parallel, pre-lowercased sort key
}
func (t *tagIndex) Search(prefix string, max int) []string  // sort.SearchStrings on lower
```

A 20k-tag index is then ~1 MB, search is `O(log n + k)`, results come back in
stable sorted order (the trie's byte order is neither case- nor
locale-correct), and rebuilding is a slice copy instead of a graph rebuild. If
fuzzy/substring matching is wanted later, add a `strings.Contains` pass over the
sorted slice or pull in a proper prefix index — not a 256-ary byte trie.

Keep `AddTag`/`ClearAllTags`/`KeysWithPrefix` as the public surface so the four
call sites don't change. Do the rebuild off the UI goroutine and swap the
result in with `fyne.Do` (the pattern `ReadCustomAsync` already uses).

### 2.3 CRITICAL — Non-ASCII tags are corrupted and written back to the store  **[reproduced]**

`tagselection/trie/trie.go:113`:

```go
q = trie.collect(node.links[c], fmt.Sprintf("%s%c", prefix, c), q)
```

`c` is a **byte index** (0-255), but `%c` formats it as a **rune**, so any byte
≥ 0x80 is re-encoded as its Latin-1 code point — two UTF-8 bytes instead of one.
Reproducer ([A.3](#a3-trie-corrupts-non-ascii-keys)):

```
put: café  naïve  über  日本語
got: cafÃ©  naÃ¯ve  Ã¼ber  æ¥æ¬èª
```

The corrupted key is not cosmetic. `tagselection.go:126-135` lowercases the
query, calls `KeysWithPrefix`, then looks the returned key up in `caseMap`
(populated with correctly lowercased keys at `:662-668`). For a non-ASCII tag
the lookup **misses**, so the mangled string is what the dropdown shows
(`:111`) and what selecting it writes as a tag. The corruption therefore leaves
the widget and lands in the shared triplestore, where every other tie client —
including the CLI — sees it.

**Fix:** with the §2.2 rewrite this disappears (a sorted slice never
reconstructs keys). If the trie survives even briefly, the one-line fix is to
build keys from bytes:

```go
q = trie.collect(node.links[c], prefix+string(rune(c)&0xFF), q)  // still wrong for multi-byte
```

— which is why the real fix is to stop reconstructing keys from the path
through a byte trie: store the word at its terminal node and return that.
Add a regression test with accented, Cyrillic and CJK tag names asserting
round-trip equality, and a `tagselection` test that a search result is exactly
the tag that gets written.

---

## 3. High-severity findings

### 3.1 HIGH — The ops progress list races with the ops goroutine  **[reproduced]**

`cmd/tie-fm/internal/fs/ops.go:99` (`o.active = append(...)`), `:113` +
`:122-130` (`o.remove` → `slices.Delete`), `:120` (`Active()` returns the slice
header to the caller).

`run()` owns `active` on the ops goroutine; `Active()` is called from the UI
goroutine by the progress view (`internal/ui/fileopview.go:42,50,100`) and by
`main.go:75-78` inside `ActiveChanged`. There is no mutex and no copy.
Reproducer ([A.4](#a4-operationsactive-data-race)): enqueueing copies from one
goroutine while polling `Active()` from another trips the race detector on both
the append and the `slices.Delete`.

Beyond the race: `slices.Delete` shifts the backing array, so a UI frame that
captured the slice can read a torn view — a row for an op that already finished,
or the same op twice. The per-op fields are unsynchronised too: `Status`, `Err`,
`TotalBytesRead` (`:930`, `:943`), `TotalSize` (`:749`, `:995`) are written by
the ops goroutine and read by `PctComplete()` (`:399-404`) and the progress
renderer.

**Fix (recommended):** give `Operations` a `sync.Mutex` guarding `active`, and
make `Active()` return a copy (`append([]*Op(nil), o.active...)`). Guard the
`Op` progress fields with the same mutex (or make `TotalBytesRead` an
`atomic.Int64` and `Status` an `atomic.Int32`), and add a `-race` test that
polls `Active()` while ops run — the existing `ops_test.go` drives everything
sequentially, which is why the suite is green.

### 3.2 HIGH — Plaintext credentials written world-readable  **[reproduced]**

`tieconfig/editor.go:203` saves the tie client config through
`conf.WriteConfig`, which is `os.WriteFile(outputFile, b, 0766)`
(`uidbz/conf@v1.1.6` `conf.go:207`). The tie config carries
`Username`/`Password` for the triplestore and every filehost
(`tie/client/tie.go:165`).

Reproducer ([A.5](#a5-config-file-mode)): `conf.WriteConfig` on a normal umask
produces `-rwxr--r--` containing `Password = 'topsecret'`. So on any shared
machine every local user can read the credentials — and with a permissive umask
they can also **overwrite** them (0766 is group/other-writable before umask, and
the exec bits are simply wrong for a data file).

**Fix:** the root fix is `conf.saveToml` using `0600` and writing through a
temp file + rename so an existing file's mode is preserved and a partial write
is never observable **(recommended)** — file it upstream. tie-gui should not
wait for it: after `conf.WriteConfig`, `os.Chmod(savePath, 0600)`, and create
the config directory `0700`. Add a test asserting the mode of a saved config,
and make the settings editor's password field render masked. Longer term,
prefer an auth token over a password in the GUI's own defaults
(`tie/client/tie.go:21-31` seeds `defaultuser`/`defaultpassword`).

### 3.3 HIGH — The UI goroutine blocks on network I/O, and nothing has a timeout  **[code-read]**

Three facts combine into an app freeze:

1. `Gallery.LoadGallery` (`gallery/gallery.go:657`) starts with
   `viewer.loading.Wait()` on the **calling** goroutine, and `loading` counts the
   `ReadCustomAsync` fetch goroutine (`:1387-1400`) that runs the tie query.
2. `ChangeGallery` (`:712-716`) and `ChangePage` (`:734-738`) drain the load
   queue and then `currentlyLoading.Wait()` — waiting for in-flight
   `GetThumbnail` calls, which for a tie cache miss **download the full blob**
   (`tiethumb/tiethumb.go:96-110`).
3. No HTTP client in the path has a `Timeout` or takes a `context`:
   `client.HTTPClientFor` (`tie/client/tie.go:297-309`) returns
   `http.DefaultClient` or a bare `&http.Client{Transport: …}`.

Both `LoadGallery` and `ChangePage` are called from UI handlers — the "Load Next
Page ▼" button (`gallery/tilelayout.go:250-262`), the pagination links, tag
selection. So one stalled triplestore or filehost (dead TCP connection, NFS
backing store, laptop sleep) freezes the window: the click does nothing, the
window stops repainting, and there is no cancel. The gallery's retry budget
(`maxTileLoadAttempts = 3`, `tilelayout.go:483`) then re-issues the same hung
request three times per tile.

**Fix (recommended), in two parts:**
1. **Timeouts and cancellation.** Wrap every store call in tie-gui with a
   `context.Context`: add `GetThumbnail(ctx, info)` to `gallery.Thumbnailer` and
   `ThumbReader`, thread a per-page `context.Context` through `placeTiles` →
   `imagesToLoad` → workers, and cancel it when a new placement starts. Build
   the HTTP clients here rather than waiting on tie: a small
   `tieconfig.HTTPClient(host, timeout)` returning a pooled
   `&http.Client{Timeout: 30s, Transport: shared}` and use it in `tiethumb`,
   `tie-view/tie.go`, `tie-fm/internal/fs/tie.go` and `tie-audio`'s session.
2. **Never wait for the network on the UI goroutine.** Move the drain +
   `currentlyLoading.Wait()` into the same background goroutine that calls
   `placeTiles`, and let `LoadGallery` return immediately; a stale page stays on
   screen until the new one is ready (the `placeMu` + last-writer-wins design
   already supports this).

### 3.4 HIGH — `Materialize` caches truncated downloads as complete  **[code-read]**

`cmd/tie-fm/internal/fs/tie.go:114-133`:

```go
dest := filepath.Join(t.tmpDir, e.Hash+"-"+e.Name)
if _, err := os.Stat(dest); err == nil { return dest, nil }   // "already downloaded"
if err := t.tc.Download("", e.Hash, dest); err != nil { return "", err }
```

`tc.Download` → `getlib.DownloadFile` (`tie/io/getlib/getlib.go:92-111`) writes
straight to the final path with `os.Create` + `io.Copy` — no temp file, no
rename, no size check. So a cancelled export (`op.Cancel()` aborts the body
reader), a dropped connection or an OOM mid-download leaves a **truncated file
at the cache path**, and every later `Materialize` of that entry returns it as
if it were complete. The user gets a corrupt video that won't play or a corrupt
exported photo, and no retry can fix it because the `Stat` always hits.

Two more problems in the same function: `t.tmpDir` is created lazily and
assigned without synchronization while `Materialize` is called from the ops
goroutine (`ops.go:690,738`) *and* the UI goroutine
(`internal/ui/preview.go:150,362`, `internal/ui/filemanager.go:615`) — a data
race; and the temp directory is **never removed**, so every export and every
preview download leaves its bytes on disk for the life of the session (and
forever if the app is killed).

**Fix (recommended):** download to `tmpDir/<hash>.part`, `f.Sync()`, then
`os.Rename` into place — the same atomicity rule as tie's filehost upload
(review §2.3). Key the file on the **hash alone** (see §3.5), verify the size
against `e.Size` (or re-hash on a `HEAD` mismatch) before returning it, guard
`tmpDir` with a mutex or create it in `NewTieFS`, and register a cleanup
(`os.RemoveAll(tmpDir)` on `TieFS` close, plus a start-up sweep of stale
`.part` files).

### 3.5 HIGH — A store-supplied filename escapes the temp directory  **[code-read]**

`cmd/tie-fm/internal/fs/tie.go:125`: `filepath.Join(t.tmpDir, e.Hash+"-"+e.Name)`.

`e.Name` comes from the tie store's `filename` triple (`client.ReadTieDir`
copies it verbatim into `File.Filename`, and `TieFS.List` at `:104` into
`Entry.Name`). tie's own review found `name`/`filename` corruption
(`TrimRight`) and nothing constrains the value to a basename: a store written by
`restore`, by another client, or by an attacker who controls one triple can put
`../../.bashrc` or an absolute-looking component in it. `filepath.Join` then
collapses the `..` sequences and the download **writes outside `tmpDir`**, at
whatever path the user's process can write.

This is the mirror of tie's filehost traversal finding: tie-gui trusts
store-supplied strings as paths in six places, not just here —
`internal/fs/tie.go:104` builds the virtual `tie:` URI from `a.Filename`, and
`gallery/tilelayout.go:718` (`os.ReadFile(previewPath)`) reads paths that
originated as archive member names.

**Fix:** one helper, `fs.SafeName(s string) string`, that takes
`path.Base`, strips NUL/`/`, rejects `.`/`..`, and falls back to the hash when
the result is empty; use it wherever a store-supplied name becomes a path.
Better: key the temp file on the content hash alone (`tmpDir/<hash>`), which
removes the untrusted component entirely and fixes the §3.4 cache-key ambiguity
at the same time. Add a test with `filename = "../escape"` asserting the
materialized path stays inside `tmpDir`.

### 3.6 HIGH — Directories with more than 1000 children truncate silently  **[code-read]** (inherits: tie)

tie v0.6.0 caps any query with `Limit == 0` at 1000 triples
(`tie/tiedb/collection.go:808-810`), and `client.ReadTieDir` issues exactly that
query — `tie/client/tag.go:1033`, no `Limit` — while **discarding** the
`TotalCount` return value. So a tie directory with 1 500 files shows 1 000 in
every tie-gui surface, with no indicator:

| Surface | Consequence |
|---|---|
| `cmd/tie-fm/internal/fs/tie.go:69` (`TieFS.List`) | the file manager pane; a **copy/export of that directory transfers 1 000 of N files** and reports success |
| `cmd/tie-view/tree.go:203,271` and `tieurl.go:87,125` | the sidebar tree and archive/dir browsing miss children |
| `cmd/tie-audio/internal/data/albums.go:200,683,706`, `internal/ui/fstree.go:377` | album and directory listings miss tracks |
| `cmd/tie-view/deimport.go:38` | **de-import leaves the unlisted children as orphans** |

tie-gui's own queries are all correct (`Limit: -1` —
`cmd/tie-view/tie.go:418,450,597`, `albums.go:381,495`,
`internal/fs/tie.go:367` `ListTags(0, -1)`), so this is entirely the
`ReadTieDir`/`fileChildren` path.

**Fix (recommended) while the pin holds:** stop using `ReadTieDir` for listings
in tie-gui and issue the query directly with `Limit: -1` (the code to do this is
already written three times over in `TieFS.List`'s row-mapping switch — extract
it once into `tieconfig.ReadDirFull(tc, uid) ([]client.SubDirectory, []client.File, []client.ArchiveEntry, error)`).
Then assert `len(children) == total` and surface a warning row in the pane when
they differ, so a future regression is visible instead of silent. File the
upstream fix (tie review §3.1) and drop the workaround when the pin moves past
a release that normalizes `Limit <= 0`.

### 3.7 HIGH — One crafted image OOMs the viewer  **[code-read]**

`gallery/helper.go:27-59` (`Decode`) calls `image.Decode` with no dimension or
byte limit, and `NewImageTile` (`gallery/tilelayout.go:1093-1101`) immediately
copies the result into an `*image.RGBA` (`toRGBA`), doubling the footprint.

A 5 000-byte PNG can decode to 40 000 × 40 000 px = 6.4 GB of RGBA. tie-gui
decodes untrusted bytes from three sources: local archives
(`gallery/gallery.go:1402-1439`, `tilelayout.go:707-715`), tie archive blobs
(`tiethumb/tiethumb.go:103`), and any tie blob whose `thumbnail` relation was
poisoned. The decode happens on a loader worker, so the OOM killer takes the
whole app down — no error dialog, no recovery. `ScaleImage` then allocates a
third copy.

**Fix (recommended):** reject oversized sources before decoding. `image.Decode`
is cheap to guard: read the header with `image.DecodeConfig` first and refuse
anything over a configurable budget (e.g. 80 MP or 16 384 px on a side),
returning an error so the gallery keeps its placeholder (the retry budget
already handles a `nil` thumbnail gracefully). Apply the same check in
`tiethumb.GetThumbnail` and `dirPreviewThumbnail`. Cap the *encoded* thumbnail
size too, and consider `imaging`'s decode-free path for the scale step.

### 3.8 HIGH — Two entries with the same content hash share one `*Tile`  **[code-read]**

`gallery/tilelayout.go:494-495` and `:559-561`:

```go
if t, ok := layout.tileFromCache(tc.Path); ok { tile = t }   // same *Tile object
…
layout.tiles[idx] = r.tile
if idx < len(layout.grid.Objects) { layout.grid.Objects[idx] = r.tile }
```

The cache key is `ImageInfo.Path`, which for tie entries **is the content hash**
(`cmd/tie-view/tie.go:116-118`). Two gallery entries with the same hash — the
same file listed under two names in one tie directory, a duplicate from the
duplicate-UID problem tie's `verify` knows about, or the same image appearing on
both sides of a query — get the *same* `*Tile` widget in two grid slots. A Fyne
`CanvasObject` has one parent and one position: `Layout` then `Move`s the single
object twice per pass, so one of the two entries renders on top of the other and
the other slot stays blank; `cyclePreview` and the drag overlays act on both
"entries" at once; removing one entry (`Gallery.RemoveItem`) leaves a dangling
object in the other slot.

**Fix (recommended):** cache the *decoded thumbnail bitmap* (an `*image.RGBA`,
refcounted and shared) rather than the widget, and build a fresh `*Tile` per
slot around it. That also makes the cache budget byte-based (see §4.5) and stops
a cached tile from holding a stale `Info` pointer across page changes. If a
widget-level cache is kept for a fast path, key it on `(Path, slot)` — but the
bitmap cache is the honest fix.

### 3.9 HIGH — `mpvplayer` can free C objects the GL thread is using  **[code-read]**

`mpvplayer/mpv.go:204-210` (`RenderInto`), `:262-279` (`Close`), `:320-330`
(`command`).

`Close()` runs on the UI goroutine: it stops the event loop, calls
`mpv_render_context_free(p.render)`, then `mpv_terminate_destroy(p.mpv)`, then
`p.self.Delete()`. Meanwhile Fyne paints on the GL goroutine, and `RenderInto`
does `C.render_to_fbo(p.render, …)` with no lock and no nil check — if a paint
is in flight (or queued) when the window closes, it renders into a freed
`mpv_render_context`. After `Close`, `p.render` is nil and
`mpv_render_context_render(NULL, …)` segfaults; likewise `command()` (`:320`)
dereferences a nil `p.mpv`, while the property getters (`:281-305`) *do* check —
the guard is inconsistent. `SetOnUpdate` (`:181`) writes `p.onUpdate` from the
UI goroutine while `goRenderUpdate` (`:373-380`) reads it on an mpv thread,
unsynchronised.

**Fix (recommended):** add a `sync.Mutex` (or an `atomic.Bool` closed flag plus
a `sync.RWMutex` held across every C call) so `Close` waits for in-flight
renders, and check the closed flag at the top of `RenderInto`, `command` and
`ensureRender`. Store `onUpdate` in an `atomic.Pointer[func()]` — the pattern
`localBackend.onPlay` already uses (`cmd/tie-audio/internal/playback/local.go:26`).
Free the render context *before* terminating mpv (already the order) and delete
the cgo handle last (also already the order) — keep a comment stating why.
Add a test that opens and closes the player while forcing repaints.

---

## 4. Medium-severity findings

### 4.1 The `ImageInfo` threading contract is unenforced

`ImageInfo` is shared between the UI goroutine and the loader workers with no
synchronization on several fields:

| Field | Written | Read |
|---|---|---|
| `previewIndex` | UI: `Tile.cyclePreview` (`tilelayout.go:1199`) | workers: `dirPreviewThumbnail` (`:694`, `:775`), `videoThumbnail` (`:901`) |
| `PreviewReaders` | worker: `dirPreviewThumbnail` (`:765-769`) | UI: `PreviewCount()` (`:1195`), swipe |
| `Width`, `Height`, `ThumbnailIsScaled` | worker: `tiethumb.GetThumbnail` (`tiethumb.go:133-135`), `GetThumbnail` (`tilelayout.go:649,669`) | UI: `newImageTileFromImage` (`:1135`), `ReadCustom` (`gallery.go:1375-1377`) |
| `loadAttempts` | placement goroutine: `placeTiles` (`:230`); workers (`:506-507`) | workers (`:506`) |

Swipe a directory tile while its thumbnail is still loading, or change page
while a retry is in flight, and these are unsynchronised concurrent accesses on
the same words. The 39 `-race` failures in `gallery` are mostly the test harness
(`forceGridLayout`, `gallery_test.go:99`, calling `Scroll.Resize` →
`TileLayout.Layout` off the UI goroutine, racing the `fyne.Do` install the test
driver runs inline), so they are not all production faults — but they are proof
that nothing enforces the rule, and the table above is the part that is real.

**Fix (recommended):** make `ImageInfo` explicitly split into an immutable part
(built once by `ReadCustom`: `Path`, `DisplayName`, `order`, `CustomReader`) and
a mutable part guarded by a mutex or atomics (`previewIndex`, `PreviewReaders`,
`Width/Height`, `ThumbnailIsScaled`, `loadAttempts`). Then make the test suite
race-clean: drive layout through `fyne.Do`/the driver's own queue instead of
calling `Scroll.Resize` from the test goroutine, so `go test -race` becomes a
usable guard (see §10).

### 4.2 Thumbnail work is never cancelled

`placeTiles` (`tilelayout.go:229-233`) enqueues a page's worth of work
(`ImagesPerPage`, 500 on desktop) into a 1024-slot channel; workers
(`imageLoader`, `:491-519`) drain it. Changing page drains only what is still
*queued* (`gallery.go:712-715,734-737`) — anything a worker has already taken
keeps running to completion, including a full-blob download for a tile that
scrolled away. With the default 8 workers, eight hung or slow connections stall
the whole gallery, and scrubbing a fast-moving scrollbar re-queues page after
page of work that is discarded on arrival (the `Info` guard at `:556` drops the
result but not the cost).

**Fix:** thread a per-placement `context.Context` (see §3.3) into
`GetThumbnail`/`Thumbnailer`, and make the drain loop cancel the previous
placement's context. Bound in-flight work per page rather than per layout
(`errgroup.SetLimit`), and drop queued items whose `ImageInfo` no longer belongs
to the current page *before* fetching, not after.

### 4.3 `deimportDir` has no cycle guard and no atomicity

`cmd/tie-view/deimport.go:37-71`. The recursion follows `dir.SubDirs` with no
visited set. tie's parent graph is a DAG only by convention — a hand-edited
dump, a `restore` of a corrupt TSV, or tie's own duplicate-path/orphan history
can produce `A parent B, B parent A`, and then this function recurses until the
stack overflows and the process dies **mid-deletion**, with part of the tree
already stripped of triples.

Partial failures are also silent: a failed child delete is printed and skipped
(`:45`), then the parent's triples are deleted anyway (`:59-69`), turning the
surviving child into an orphan with a dangling parent edge.

**Fix:** carry a `visited map[client.DirUID]bool` (or a depth cap) through the
recursion; abort the parent delete when any child failed and say so; and confirm
the destructive operation with the number of items that will be removed (the
count is already known after `ReadTieDir`). Note `orphanedTags` (`:75-89`) is
correct — `Limit: 2` is a deliberate "is it unique" probe — but it costs one
query per tag per item; batch it.

### 4.4 `ReadImageArchive` accumulates and leaks handles

`gallery/gallery.go:1402-1439`. Two problems:

- It **appends** to `viewer.imageFiles` without clearing it (`:1428`), unlike
  `ReadCustom` which deliberately swaps (`:1342-1343,1380`). Opening the same
  archive twice, or an archive after a directory, duplicates every entry with
  stale `order` indices.
- `file, err := fsys.Open(path)` (`:1418`) is **never closed**. The sibling code
  path for the same library does close (`:1195-1202`, `file.Close()` at `:1202`),
  so this is an inconsistency rather than a design. Reproducing with a 600-member
  zip ([A.6](#a6-archive-member-handle-leak)) shows handles accumulating; they
  are eventually reclaimed by the `os.File` finalizer, so it degrades rather
  than exhausts — but on a large archive it hits `EMFILE` before the GC runs.

**Fix:** `defer file.Close()` inside the walk callback (or close before
`IsImage` returns), clear `viewer.imageFiles` at the top of the function, and
add a test that calling it twice yields the same count.

### 4.5 The tile cache budget counts tiles, not bytes

`gallery/tilecache.go:10-24` caps the cache at 1500 entries (300 on mobile) and
evicts in insertion order — `get` does not move the entry (`:28-33` says so), so
it is FIFO, not LRU, despite the name and the CLAUDE.md description. The
footprint is unbounded per entry: a cached tile holds a decoded `*image.RGBA`
thumbnail, which is `(TileWidth*2) × (TileWidth*2·aspect)` bytes. At the default
`TileWidth = 300` a portrait thumbnail is ~600×1200 = 2.8 MB, so 1500 tiles is
up to **~4 GB**, and the user can raise `TileWidth` in the config. Conversely
the FIFO order evicts the page the user just scrolled back to, which is exactly
the case the 1500 budget was raised to serve.

**Fix:** make the budget a byte count (`maxCacheBytes`, default ~150 MB desktop /
40 MB mobile), track each entry's `Bounds().Dx()*Dy()*4`, and use real LRU
(`container/list`, moved on hit). This pairs with §3.8: cache the bitmap, not
the widget.

### 4.6 `tablewidget` is forked, diverged, and half-tested

`cmd/tie-fm/internal/widget/tablewidget/` and
`cmd/tie-audio/internal/widget/tablewidget/` are the same package twice:
`tabledata.go` is byte-identical, `tablewidget.go` differs by 25 lines and
`flextable.go` by **239 lines**, and only tie-fm's copy has tests
(`sort_test.go`). Any fix landed in one copy silently stays broken in the other,
and tie-audio's tables — the browse wall, the playlist — are untested.

**Fix:** promote one `internal/tablewidget` (or a top-level `tablewidget/`
package, matching `tagselection/`) and reconcile the two variants into options
on the widget. Start by porting tie-fm's `sort_test.go` to tie-audio's copy to
find where behaviour has already drifted, then merge.

### 4.7 Local thumbnail generation reads each file into RAM two or three times

`gallery/tilelayout.go:619-675`: `GetThumbnail` does `io.ReadAll(r)` to hash the
whole file (`:627`), then `Decode` (`helper.go:27-59`) seeks and — for JPEG —
`getOrientation` does `io.ReadAll(reader)` over the whole file **again**
(`helper.go:63`), then `image.Decode` builds the full bitmap, then `ScaleImage`
allocates a second one. For a 40 MB photo that is ~120 MB of transient
allocations per thumbnail, on 8 workers in parallel.

**Fix:** hash incrementally (`io.Copy` into `highwayhash`/`sha256` while
teeing the first 64 KB for sniffing), read EXIF from a bounded head window
(`exif` only needs the APP1 segment), and reuse the decode buffer. One pass, one
buffer, streaming.

### 4.8 No connection reuse for thumbnail and blob fetches

`client.HTTPClientFor` (`tie/client/tie.go:297-309`) returns
`http.DefaultClient` when the host needs no auth, but builds a **fresh
`http.Client` with a fresh `http.Transport`** whenever the host is authenticated
or insecure. tie-gui calls it per operation — `tiethumb` (`:166,202,222`),
`cmd/tie-view/tie.go:133`, `tie-fm`'s `TieFS` — so an authenticated filehost
re-negotiates TCP (and TLS) for every thumbnail. Cache one client per
`(URL, username, insecure)` in tie-gui (a small map under a mutex in
`tieconfig`) until tie does it upstream.

### 4.9 Extraction and copy do not preserve permissions faithfully

`cmd/tie-fm/internal/fs/extract.go:302` opens members with
`info.Mode().Perm()|0600`, so an archive member with mode `0777` extracts
world-writable, and `copyFile` (`ops.go:1043`) always creates local copies
`0755` regardless of the source. Neither is a security hole in the local→local
case, but both surprise users: a `0600` private key copied through tie-fm
becomes group/other-readable, and an archive's `0777` stays `0777`.

**Fix:** copy the source mode explicitly (`os.Chmod` after the copy, masked by a
umask-aware default), and mask archive modes (`perm & 0755`).

### 4.10 Launched children are never reaped

`cmd/tie-fm/internal/ui/launch.go:49,52,112` call `cmd.Start()` and drop the
`*exec.Cmd`. Every opened file leaves a zombie until tie-fm exits — harmless for
a few, visible after a long session of opening images and terminals. **Fix:**
`go func() { _ = cmd.Wait() }()` after each `Start`, or use
`exec.Command(...).Run()` in a goroutine.

### 4.11 `gallery` shadows the builtin `max`

`gallery/tilelayout.go:1087-1091` declares `const max = lvlDeep * dirWidth` at
package scope, which shadows Go 1.21's builtin `max` for the whole package.
`scrollToOffset` (`:434-436`) then declares a local `max` variable, so the
shadowing is already invisible-but-load-bearing: the first person to write
`max(a, b)` in `gallery` gets a type error at best, or a silently wrong constant
after a refactor. Rename to `dirLevels`/`cacheDirWidth` and use the builtin.

### 4.12 Short reads in the type sniffers

`gallery/helper.go:104-111` and `:137-148` fill a 261-byte buffer with a single
`file.Read(head)`. `io.Reader` may return fewer bytes; for a pipe, a network
backed file, or a `fs.FS` member this routinely returns a short prefix, and
`filetype.IsImage`/`IsArchive` then answer `false` for a real image. Use
`io.ReadFull`.

### 4.13 `TileLayout.Clear` is dead, and `Gallery.RemoveItem` has no caller

`gallery/tilelayout.go:171-174` (`Clear`) is exported and never called; it also
mutates `layout.tiles` **off** the UI-thread contract (no `fyne.Do`), so leaving
it around invites exactly the race §4.1 describes. `Gallery.RemoveItem`
(`gallery.go:696-706`) is likewise unused in-tree. Delete both, or make them
correct and used.

### 4.14 tie-audio's settle-waits can block a caller goroutine for a second

`cmd/tie-audio/internal/playback/local.go:104-111` (`waitTotal`) polls up to
200 × 5 ms. The UI calls these inside `go func()` (`internal/ui/queue.go:247,287,438,…`),
so today it is off the UI goroutine — but the guard is a convention at each call
site, not a property of the API. Document that every `PlaybackBackend` method
may block, or move the settle into a callback, so a future direct call from a
tap handler cannot reintroduce a freeze.

---

## 5. Low-severity findings and performance

### 5.1 Correctness / robustness (low)

| # | Where | Finding | Fix |
|---|-------|---------|-----|
| L1 | `tiethumb/tiethumb.go:127,184,188`; `tieconfig/path.go:60` | Libraries report errors with `fmt.Println` to **stdout**. A GUI embedder cannot capture or silence them, and `tie-view`'s own stdout is invisible on Windows. | Take a `*slog.Logger` (default discard) or return the errors; see §6.1. |
| L2 | `cmd/tie-view/main.go:660,710,780,816,833`; `tie.go:296,340-350,423,456,630`; `tree.go:93,193,205`; `imagetagger.go:150-420`; `quicktag.go:302-497`; `tie-audio/internal/ui/browse.go:351,427,448,585,645`; `fstree.go:96,221`; `archive.go:218,222`; `tie-fm/internal/ui/preview.go:364,374` | ~60 more `fmt.Println`/`fmt.Printf` error reports in app code, on stdout, with inconsistent prefixes (`Error …`, `quicktag: …`, `imageTagger: …`). | One logger per app; stderr; consistent `tie-<app>: <op>: <err>`. |
| L3 | `mpvplayer/mpv.go:171-174` | mpv log messages go to **stdout** via `fmt.Printf` from the event-loop goroutine. | Route to the app logger (stderr). |
| L4 | `gallery/tilelayout.go:667` | Thumbnail cache files are written `0755` (executable); `:862` and `:951` use `0644`. | `0644` everywhere. |
| L5 | `cmd/tie-fm/internal/fs/ops.go:1056-1059` | `copyFile` compares `written` against `op.A.Size`, a stat from planning time; a file that changes during the copy fails the op spuriously. | Re-stat the source, or compare against the bytes actually read. |
| L6 | `cmd/tie-fm/internal/fs/ops.go:883-891` | `checkLocalConflict` treats a `Stat` error as "absent, proceed", so a permission error silently becomes an overwrite. | Distinguish `ErrNotExist` from other errors and surface the latter. |
| L7 | `cmd/tie-fm/internal/fs/ops.go:896-918` | `checkRemoteConflict` proceeds without prompting when the destination cannot be listed ("historic behaviour"). | Keep the behaviour but log it, and offer a strict mode. |
| L8 | `cmd/tie-fm/internal/ui/launch.go:72-90` | Commands are split with `strings.Fields`, so an association like `my app --flag "%f"` cannot express an argument containing a space. Documented, but users hit it. | Use a shell-like splitter (`github.com/kballard/go-shellquote`) or document `%f` quoting. |
| L9 | `gallery/gallery.go:117-122` | `IsImageFromPath` registers `defer file.Close()` before checking `err`. Harmless today (`os.File.Close` nil-checks) but fragile. | Check `err` first. |
| L10 | `cmd/tie-view/deimport.go:75-89` | `orphanedTags` issues one query per tag per item; de-importing a 500-file tree costs thousands of round-trips. | One batched query, or `CoTags`-style aggregation. |
| L11 | `cmd/tie-audio/internal/history/recorder.go:302-322` | `flush` sends the whole spool in one `RecordPlays` call; offline for weeks means one huge batch that can exceed request limits. | Chunk (e.g. 500 events per call), report progress. |
| L12 | `gallery/tilecache.go:65` | `c.order = c.order[1:]` never releases the head of the backing array. | Use `container/list` (also gives real LRU, §4.5). |
| L13 | `cmd/tie-fm/internal/fs/tie.go:245-250`, `cmd/tie-view/tie.go:109-114`, `cmd/tie-audio/internal/data/albums.go:110-116` | `StreamURL` is `host.URL + "/" + hash`. If a user configures a filehost URL with `user:pass@` userinfo, the credential is handed to libmpv/pwplay and lands in their logs. | Reject userinfo in configured host URLs at config load; use the auth transport only. |
| L14 | `gallery/helper.go:90-100` | `ReadDir` returns index `0` both for "not found" and "found first entry". | Return `-1` and handle it. |
| L15 | `cmd/tie-fm/internal/fs/extract.go:208-233` | Extraction walks the archive twice (plan pass, write pass); for a remote archive that means two full passes over a materialized file. | Keep the member list from pass 1 and reuse it (it already is kept — the second walk re-opens only to stream bodies; make that explicit). |

### 5.2 Performance

Ordered by expected impact. Per CLAUDE.md, benchmark old vs new under the same
conditions before keeping any of these.

1. **Tag trie → sorted slice** (§2.2). Largest win by far: ~900 MB → ~1 MB at
   20k tags, and it removes a UI-goroutine rebuild from every tag click.
2. **Cancel and time out thumbnail work** (§3.3, §4.2). Scrubbing a 500-tile
   page currently downloads work that is thrown away; a cancelled context cuts
   both bandwidth and the 8-worker stall.
3. **Byte-budgeted LRU tile cache** (§4.5). Turns a 4 GB worst case into a
   bounded one and makes scroll-back actually hit.
4. **Single-pass local thumbnail ingest** (§4.7). Cuts ~3 full-file reads to one
   per thumbnail miss.
5. **Connection reuse** (§4.8). On an authenticated filehost over a WAN, one TLS
   handshake per thumbnail is the dominant cost of a first page load.
6. **`Layout` re-measures labels every pass** (`tilelayout.go:320-329`): it
   scans all tiles for `subLabel` on every layout pass, including every scroll
   and resize. Cache the flag when tiles are installed.
7. **`dirPreviewCache` reads the whole cached JPEG per tile**
   (`tilelayout.go:880-886`) and the in-memory tile cache is consulted first, so
   a page of directory tiles does one small file read each — fine, but the
   `finishDirPreview` badge-and-encode path (`:845-865`) re-encodes a JPEG on
   every cache miss; consider caching the *unbadged* scaled image and compositing
   the badge at draw time.
8. **`orphanedTags` round-trips** (L10) dominate de-import time on large trees.

---

## 6. Desktop-app conventions review

The project already gets several things right: config by file with an in-app
editor, XDG-style config/state/cache locations (`tieconfig.Dir`), a
`-config` flag, per-app collection profiles, stderr-free operation for the
servers it talks to, and TTY-independent behaviour (nothing here pretends to be
a pipe). The items below are where it drifts.

### 6.1 Libraries write to stdout ("rule of silence")

`tiethumb` and `tieconfig` — both *libraries* — report errors with
`fmt.Println` to stdout (L1), and the four `main` packages add ~60 more
(L2). A library cannot assume stdout is a terminal or even that it exists (an
Android app has no stdout at all — these messages are simply lost on the mobile
builds, which is where the network is least reliable).

**Recommendation:** libraries never touch `os.Stdout`/`os.Stderr`. Give
`tiethumb.Thumbnailer` and `gallery.Gallery` a `Logger *slog.Logger` (default:
discard) plus the typed callbacks that already exist for progress. Each `main`
wires a stderr handler and honours `-q`/`-v`. On Android, route the same logger
to `__android_log_print`.

### 6.2 No logging, no crash surface

There is no log file, no structured logging, and no recovered panic anywhere in
the repo. A Fyne app that panics on a loader goroutine dies with the message on
a console that a desktop launcher user never sees. **Recommendation:** one
`slog` setup in a shared `gallery.AppBootstrap` (it already exists as the app
helper), writing to `$XDG_STATE_HOME/tie/<app>.log` with rotation, plus a
`recover()` at the top of every long-lived goroutine that logs and reports a
toast rather than dying.

### 6.3 Config: writes on read paths, and no way to see what was resolved

`tieconfig.Load` (`path.go:56-65`) silently substitutes
`client.DefaultConfig()` when the file is missing — including its
`defaultuser`/`defaultpassword` — and prints a message to stdout for any other
error. The settings editor then writes that default (with the §3.2 mode). A user
with a typo in `-config` gets a working-looking app pointed at `localhost`.

**Recommendation:** distinguish "no config" (say so, and for read-only commands
continue with defaults) from "unreadable config" (fail). Add a
`tie-<app> conf show` that prints the **resolved** triplestore URL, filehost
URL, collection and config path — the single most useful debugging command for
this project — and mask passwords in it.

### 6.4 Exit codes and signals

The GUI apps have no non-zero exit path for a failed start that a script or a
desktop launcher can see (a failed tie connection is logged and the app opens
empty). **Recommendation:** exit `2` for a usage/config error and `4` for an
unreachable store when a `--check` (or `-version`-style probe) flag is given, so
a session manager can tell "misconfigured" from "server down".

### 6.5 Small things that are already right and worth keeping

- `fyne.Do` discipline in `cmd/tie-fm/internal/ui` and `cmd/tie-audio/internal/ui`
  is consistent and commented — the pattern to copy elsewhere.
- Archive extraction rejects `..` and absolute member paths and never
  materializes symlinks or devices (`extract.go:80-90,182-189`).
- Launching never goes through a shell (`launch.go:72-90`), so filenames cannot
  inject commands.
- The play-history recorder is at-least-once, idempotent and crash-safe
  (`internal/history/recorder.go`), with the cursor advanced only after the
  spool lands.
- `TieFS`/`LocalFS`/`MTPFS` behind one `fs.FileSystem` interface with capability
  interfaces (`Importer`, `DirTyper`, `DirLabeler`, `Streamer`) is a clean
  design, and the ops engine degrades correctly when a backend lacks one.
---

## 7. Refactoring options

Each option lists the alternatives considered and a recommendation, ordered by
value per unit of effort.

### 7.1 One store-access layer for all four apps (recommended)

Every app re-implements the same store plumbing: resolve host → build an HTTP
client → query with `Limit: -1` → map rows → materialize a blob → write a
triple. That is why the §3.6 truncation, the §3.4/§3.5 materialize bugs, and the
§4.8 no-pooling problem each exist three times.

- **Option A (recommended).** A `tiestore` package (sibling of `tieconfig`)
  owning:
  - `Client(host) *http.Client` — pooled, with a timeout (§3.3, §4.8);
  - `ReadDir(ctx, tc, uid)` — full listing, truncation-checked (§3.6);
  - `Materialize(ctx, e)` — atomic, hash-keyed, cleaned up (§3.4, §3.5);
  - `Fetcher` with context cancellation for the thumbnail pipeline (§4.2).
  All four apps call it. The `gallery.Thumbnailer` and `fs.FileSystem`
  interfaces stay as they are.
- **Option B.** Fix each site in place. Cheaper now, but the next app repeats
  the mistake.

### 7.2 `gallery`: split the state and make the threading explicit

`gallery/gallery.go` (1439 lines) and `gallery/tilelayout.go` (1494 lines) mix
widget construction, page state, the loader pipeline and the cache.

- Split along the seams that already exist as comments: `page.go` (imageFiles,
  offset, pagination), `loader.go` (workers, results, flush), `cache.go`
  (tileCache + disk cache), `view.go` (widget construction). Pure move, own
  commit.
- Then apply §4.1: an immutable `ImageInfo` core plus a mutex-guarded mutable
  part, and one documented rule at the top of the package — *"grid and tile
  objects are touched only inside `fyne.Do`"* — enforced by the race-clean test
  suite (§10) rather than by comment.

### 7.3 `Operations`: make the engine's state safe by construction

`ops.go` (1061 lines) is one goroutine's data structure with a public read API
that ignores that fact (§3.1).

- **Recommended:** a `mu sync.Mutex` on `Operations` guarding `active` and the
  `Op` progress fields; `Active()` returns a copy; `Status`/`TotalBytesRead`
  become atomics. Add the containment guard from §2.1 as a single
  `checkContainment()` called from `doCopy`, `doMove`, `doImport`, `doExport`
  and `doExtract`.
- **Alternative:** make the ops goroutine the only owner and publish immutable
  snapshots over a channel to the UI. Cleaner, larger diff; do it only if the
  mutex version shows contention (it won't).

### 7.4 Tag index: sorted slice, not trie (recommended)

See §2.2. Keep the `TagSelection` public API; replace `trie.Trie` with a
`tagIndex` value type. This is a contained change: `trie/` has one consumer.

### 7.5 `mpvplayer`: one lifecycle, one lock

`MPVPlayer` currently has three lifecycles (mpv handle, render context, cgo
handle) and no lock (§3.9). Recommended: a `closed atomic.Bool` plus an
`RWMutex` held around every C call, `Close` taking the writer lock; document the
free order (render ctx → mpv → handle) as load-bearing. The screenshot path
(`screenshot.go`) already joins its drain goroutine correctly — copy that
discipline into the player.

### 7.6 Shared `tablewidget` (recommended)

See §4.6. One package, options struct for the divergences, tests for both
consumers.

### 7.7 App bootstrap: one place for flags, logging, config and cleanup

The four `main`s each re-parse flags, load config, adjust for mobile, wire
hotkeys and set up the window. `gallery/apphelper.go` exists but is thin.
Extend it (or a new `internal/app`) to own: flag parsing, `tieconfig` load +
`conf show`, the §6.2 logger, signal handling, and temp-dir cleanup (which today
nobody does — §3.4).

---

## 8. Simplification options

Concrete deletions and collapses, each small enough for one commit.

| # | Simplification | Removes | Risk |
|---|----------------|---------|------|
| S1 | `trie/` → sorted-slice tag index (§2.2, §7.4) | ~120 lines of trie, 900 MB at 20k tags, the UTF-8 corruption (§2.3) | Low; one consumer, keep the API |
| S2 | `tiestore.ReadDir` replacing the four `ReadTieDir` call sites (§3.6, §7.1) | the 1000-child truncation, three copies of row→entry mapping | Low; covered by new tests |
| S3 | `tiestore.Materialize` replacing `TieFS.Materialize` (§3.4, §3.5) | the partial-download cache, the untrusted-name path join, the leaked temp dir | Low |
| S4 | Delete `TileLayout.Clear` and `Gallery.RemoveItem` (§4.13) | 20 lines and two threading violations | None (no callers) |
| S5 | Delete the `IsImageFromPath`/`IsArchiveFromPath` duplication in favour of one `sniff(path)` using `io.ReadFull` (§4.12, L9) | ~25 lines | None |
| S6 | One `SafeName` helper for every store-supplied name used as a path (§3.5) | the traversal class | Low |
| S7 | Merge the two `tablewidget` packages (§4.6) | ~600 duplicated lines | Low; port `sort_test.go` first |
| S8 | Replace `fmt.Println` error reporting in `tiethumb`/`tieconfig` with a logger (L1) | stdout writes from libraries | None |
| S9 | `gallery.Decode`: one `DecodeConfig` guard instead of ad-hoc size checks at each call site (§3.7) | the OOM class | Low |
| S10 | Cache one HTTP client per host in `tieconfig` (§4.8) | per-call transports | Low |
| S11 | `orphanedTags` batched into one query (L10) | N round-trips per de-import | Low |
| S12 | `gallery`'s `const max` → named constants (§4.11) | builtin shadowing | None |

---

## 9. Risk map: where bugs are likely to live

| Area | Why it's risky | Guard-rails to add |
|------|----------------|--------------------|
| `cmd/tie-fm/internal/fs/ops.go` (transfers) | Destructive, multi-backend, cross-device fallbacks, conflict state machine (`sticky`, `skipped`, `renameTo`), no containment checks | §2.1 guard + a table-driven test over (local,tie,mtp)×(file,dir)×(inside,outside); a property test: after any op, source intact unless the copy verified |
| `gallery` threading (tilelayout, gallery) | Three goroutine families (placement, workers, updater) plus the UI thread; invariants live in comments; the test driver breaks them | §4.1 split + §10 race-clean suite; a `-race` stress test (place pages, swipe previews, change pages concurrently for 10 s) in CI |
| `gallery` caches (tileCache, disk cache) | Byte-blind budgets, FIFO-as-LRU, widget identity reused across slots | §3.8, §4.5; a test that two same-hash entries render separately |
| `mpvplayer` (cgo) | Three C lifecycles, callbacks from mpv threads, GL-thread re-entry | §3.9; a close-while-painting test; run the cgo tests under `-race` and ASan if practical |
| `tagselection` | Shared by four apps; the trie was the only non-trivial algorithm in the repo | §2.2/§2.3; Unicode round-trip tests; a fuzz test comparing search results against a naive filter |
| Store access (all apps) | Three hand-rolled clients, no timeouts, no cancellation, untrusted store strings | §7.1; a fake store that delays and errors, run against every app's browse path |
| Config & credentials | Secrets in a plaintext file written by a dependency | §3.2; a mode assertion test; mask in UI and `conf show` |
| tie-audio playback | Engine callbacks, MPRIS D-Bus handlers, queue mutation races | keep the `go func()`+`fyne.Do` discipline; a test that drives track-end during queue mutation |
| Album import (`albumimport.go`, `ImportAlbum`) | Heuristic placement (`SubPaths`, `ExactDest`) plus dir-type stamping; partial failure leaves half-labelled trees | journal per chunk and echo it, as tie's repair does |

---

## 10. Tooling and process

- **No CI.** There is no `.github/` (or any workflow) in the repository. Every
  finding in §2 and §3 would have been caught by a job running
  `go build ./... && go vet ./... && go test -race ./...`. Add one for Linux
  (the CGo/GL deps need `libgl1-mesa-dev xorg-dev libmpv-dev`) and keep the
  Android build as a separate job.
- **`go test -race` is red today** (§ "How to read this"): 39 races in
  `gallery`, one test in `cmd/tie-fm/internal/ui`. Fix the harness first
  (drive layout through the driver queue, not `forceGridLayout`), then make
  `-race` the default in the `test` target rather than an opt-in.
- **The Makefile has `test` but no `vet`, `lint` or `race` target**, and no
  `check` aggregate. Add:
  ```make
  check: vet race
  vet:  ; go vet ./...
  race: ; go test -race ./...
  ```
- **No linter is installed or configured** (`staticcheck`, `golangci-lint` both
  absent). Several findings here are one-line linter catches (unused `Clear`,
  shadowed builtin `max`, `defer` before error check, `os.WriteFile` mode).
- **The build scripts have no failure discipline.** None of the nine `*.sh`
  scripts sets `set -euo pipefail`; `vendor-android-libs.sh:66` runs
  `rm -rf "$ABI_DIR" "$DST/include"` after unguarded downloads, so a failed
  fetch can delete the vendored libs it was about to refresh.
- **`push-to-repo.sh` is `git add -A && git commit -m "$@" && git push`.** It
  commits anything untracked (scratch builds, `.build/`, APKs before the
  gitignore covers them) and mangles multi-argument messages. Replace with an
  explicit stage list, or delete it.
- **`release_version.sh`** (`git tag v0.$@`) accepts no arguments and produces
  the tag `v0.`; validate the input.
- **Docs drift.** CLAUDE.md calls the tile cache "LRU" (it is FIFO, §4.5),
  places the memory budget in tile counts (it is not byte-bounded), and does not
  mention that `LoadGallery`/`ChangePage` block on the network (§3.3). Update
  after the fixes, and add the threading rule from §7.2.
- **Scratch hygiene.** `git status` is clean at review time, but `.build/`,
  `package/` and `fyne-cross/` are gitignored while ad-hoc `*.apk.idsig` and
  test binaries are not consistently covered; keep the ignore list current so
  `git add -A` (see above) stays safe.

### Test gaps worth closing first

1. tie-fm transfers: move/copy a directory into itself (§2.1); a partial
   download followed by a second `Materialize` (§3.4); `Active()` polled during
   ops under `-race` (§3.1).
2. tagselection: Unicode tag round-trip (§2.3); a 20k-tag memory budget
   assertion (§2.2); search results equal a naive filter's results (fuzz).
3. gallery: two entries with the same hash render separately (§3.8); a
   decompression-bomb member is refused (§3.7); `ReadImageArchive` twice does not
   duplicate (§4.4); the whole package under `-race` (§10).
4. tieconfig: the saved config's mode is `0600` (§3.2).
5. mpvplayer: close while repainting (§3.9).

---

## 11. Suggested roadmap

Ordered so that each week's work is independently shippable and the highest-risk
behaviour is fixed first. Every item is a GitHub issue in `uidbz/tie-gui`; the
ordered list and the Top-12 cross-reference live in the tracking issue #1.

**Week 1 — data loss and crashes (small diffs, high value)**
1. §2.1 containment guard in the transfer engine + regression tests. → #2
2. §2.3 trie UTF-8 fix (stop reconstructing keys) + Unicode tests. → #3
3. §3.7 decode-size guard (decompression bomb). → #4
4. §3.2 config file mode `0600` + directory `0700` + upstream issue to `conf`. → #5
5. §4.4 `ReadImageArchive`: clear the list, close the handles. → #6

**Week 2 — the tag index and the store layer**
6. §2.2 / §7.4 / S1: sorted-slice tag index, rebuilt off the UI goroutine. → #7
7. §3.6 / S2: `ReadDir` with `Limit: -1` and a truncation assertion. → #8
8. §3.4 / §3.5 / S3 / S6: atomic, hash-keyed, sanitized `Materialize` + temp-dir
   cleanup. → #9
9. §3.1 ops state: mutex + copied `Active()` + atomic progress fields. → #10

**Week 3 — responsiveness and lifetimes**
10. §3.3 timeouts, connection pooling and context cancellation through the
    thumbnail pipeline. → #11
11. §3.3 never wait for the network on the UI goroutine (`LoadGallery`,
    `ChangePage`). → #12
12. §3.9 `mpvplayer` lifecycle lock and closed checks. → #13
13. §4.5 byte-budgeted LRU tile cache; §3.8 cache bitmaps, not widgets. → #14

**Week 4 — make the safety enforceable**
14. §10 CI workflow; make `go test -race` green (fix the `gallery` harness,
    §4.1); add `make check`. → #15
15. §4.1 `ImageInfo` immutable-core split. → #16
16. §4.6 / S7 merge the `tablewidget` forks. → #17
17. §6.1 / §6.2 logger everywhere; remove library stdout writes (S8). → #18

**Later — structure**
18. §7.1 `tiestore` package, §7.2 `gallery` file split, §7.5 mpv lifecycle,
    §7.7 app bootstrap, plus the remaining S-items. Each is mechanical, its own
    commit, behaviour unchanged and tests green before and after. → #19
19. The non-critical follow-ups of §4.3, §4.9-§4.13, §5.1 and §10. → #20

---

## Appendix A — reproducers

None of these were committed; they were run as temporary `_test.go` files in the
affected package or as a scratch program, and deleted afterwards.

### A.1 move a directory into its own subdirectory

`cmd/tie-fm/internal/fs/zz_scratch_move_test.go`: build `root/sub/f0…f4.txt`
(7 entries), then `ops.Move(Entry{root,IsDir}, Entry{root/sub,IsDir})`.

```
move-into-subdir: status=2 err=<nil> entries before=7 after=1 remaining=[root]
```

`status=2` is `StatusCompleted`. The tree is gone.

### A.2 trie memory

```go
tr := trie.NewTrie()
for i := 0; i < 20000; i++ {
    tr.Put(fmt.Sprintf("artist-%d-album-%d-track", i*7919%100000, i*104729%100000))
}
// nodes = 385786   HeapAlloc delta = 901.3 MB
runtime.KeepAlive(tr)   // without this the GC collects the trie and the delta reads 0.1 MB
```

### A.3 trie corrupts non-ASCII keys

```go
for _, w := range []string{"café", "naïve", "日本語", "über"} { tr.Put(w) }
tr.KeysWithPrefix("")
// → ["cafÃ©" "naÃ¯ve" "Ã¼ber" "æ\u0097¥æ\u009c¬èª\u009e"]
```

### A.4 `Operations.Active()` data race

One goroutine enqueues 200 `Copy` ops, another calls `Active()` 2000 times:

```
WARNING: DATA RACE
Read at 0x… by goroutine 12:  fs.(*Operations).Active()  ops.go:120
Previous write at 0x… by goroutine 10: fs.(*Operations).run()  ops.go:99
WARNING: DATA RACE
Write at 0x… by goroutine 10: fs.(*Operations).remove()  ops.go:125
```

### A.5 config file mode

```go
conf.WriteConfig("/tmp/x.toml", struct{ Username, Password string }{"u", "topsecret"})
// mode=-rwxr--r--   content="Username = 'u'\nPassword = 'topsecret'\n"
```

### A.6 archive member handle leak

`gallery.ReadImageArchive` on a 600-member zip, counting `/proc/self/fd`:

```
members=600  fds before=8 after=16  leaked=8
```

The count is bounded only because the `os.File` finalizer eventually closes
them; the sibling path at `gallery.go:1202` closes explicitly.
