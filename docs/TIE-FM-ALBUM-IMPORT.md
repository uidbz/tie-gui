# tie-fm: Import as albums

Bulk-import a local music library into tie as labeled, well-placed albums.
One pass scans a directory tree, clusters the audio files into albums, lets
you review where every album will land, and imports the checked albums one
by one with progress and per-album error reporting.

Invoke it from a **local directory's context menu → "Import as albums…"**.
The import always targets the tie virtual filesystem; the current pane
location is ignored — destinations come from the template (below).

## The flow

1. **Form** — pick the directory type, the destination template, and
   optional tags (details below).
2. **Scan** — the planner walks the tree and probes every file (audio tags
   are read over a single open per file, on a wide worker pool — network
   mounts are slow but parallel). A progress dialog shows the probe count.
3. **Review** — one checkbox row per discovered album: title, rendered
   destination, track count, size, and warnings. Albums without a
   destination are fixed unchecked and cannot import. Nothing has uploaded
   yet; check what you want and confirm.
4. **Import** — one op per album in the operations queue (each with its own
   progress row). A failed album does not abort the rest; failures are
   summarized when the batch finishes, and a tie-browsing sibling pane
   reloads once at the end.

## The form

| Field | What it does |
|-------|--------------|
| **Directory type** | The dir-type label stamped on each imported album root (`audio-dir` makes tie-audio see it as an album) and the key used to look up the destination template. Built-ins plus a free-form **Custom** entry. |
| **Destination** | The template rendering each album's virtual path (see below). Pre-filled from the tie config's `[ImportDest]` entry for the picked type, falling back to `/{albumartist}/{year} - {album}`. Switching the type re-fills it unless you edited the field. Empty = keep the on-disk source paths (the legacy behavior). |
| **Remember as default for this type** | Writes the template into the tie config's `[ImportDest]` section for the picked dir-type, so future imports **and the `tie` CLI** render the same layout. |
| **Tags** | Comma-separated; applied to every imported track *and* to the album root, so the albums match tag queries (e.g. tie-audio's tag-driven cover wall). |

The template is validated when you click **Scan** — an unknown or
unterminated `{variable}` is rejected before the (potentially long) scan
starts. Templates that merely render empty for a specific album (e.g. a
missing year tag) are not errors here; they surface per album in the review
dialog.

## Destination templates

A template is a virtual path with `{variables}` rendered from each album's
**aggregated** tags (the modal value across its tracks):

| Variable | Value |
|----------|-------|
| `{albumartist}` | Album-artist tag, falling back to artist (compilations without an album-artist tag still render — and get a "mixed artists" warning) |
| `{artist}` | Artist tag |
| `{album}` | Album tag |
| `{year}` | Year tag (empty when untagged) |
| `{title}` / `{track}` | Modal track title/number — per-track values, rarely sensible at album level |

Any other text (slashes, spaces, `" - "`) is literal. Values are sanitized
to a single path segment: `/` and `\` become spaces, control characters are
dropped, `.`/`..` are neutralized.

**An album whose tags leave a referenced variable empty falls back** to its
source path (whole-directory group) or `<common-dir>/<album>` (tag cluster),
with a "template not applied" warning in the review dialog — it is never
placed at a path with blank segments.

Example — the layout this feature was built for:

```
/{albumartist}/{year} - {album}
→ tie:/Miles Davis/1959 - Kind of Blue
```

The same templates work in the tie CLI: `tie import audio-dir --albums
--dest "/{albumartist}/{year} - {album}" /music/incoming`, or persistently
in the tie config:

```toml
[ImportDest]
audio-dir = "/{albumartist}/{year} - {album}"
```

## Disc placement (cd1 / cd2)

Multi-disc albums normalize to `cd<N>` subdirectories below the album root,
**regardless of how the source library is organized**. The disc number comes
from the track's DISCNUMBER tag when present, falling back to a disc-like
directory name (`CD1`, `CD 1`, `disc 02`, `Disc 2`, `Disk 4`, also embedded:
`Album CD1`, `Album (Disc 2)`) — the tag wins when both exist.

- **Multi-disc** (more than one disc number, or a disc-total tag > 1):
  every disc'd file lands at `cd<N>/…` — `CD1/01.flac` → `cd1/01.flac`,
  `Disc 2/01.flac` → `cd2/01.flac`. Files with no disc signal at all (a
  `bonus/` folder, loose tracks) keep their relative placement.
- **Single-disc**: the disc level is dropped — `CD1/01.flac` → `01.flac`;
  the album sits flat at its root (no pointless `cd1`).
- **No disc structure**: files keep their relative placement verbatim.

A whole-directory album with a disc structure (e.g. one flat folder whose
tracks carry disc tags) is imported file by file rather than as a tree
mirror, so the normalization applies — its **cover art and other sidecar
files ride along** at their own mapped positions (`cover.jpg` at the root,
`Scans/front.jpg` preserved). If two files would land on the same path, the
album row gets a "destination collision" warning (importing both would
version one over the other).

The review dialog shows each album's root destination; disc routing happens
below it. Grouping is tie-fm's fixed `auto` mode: one audio-bearing
directory is one album, directories with conflicting album tags are split,
and directories sharing one album identity (multi-disc sets, scattered
rips) are merged.

## What gets written to tie

Per imported track: the blob upload plus the usual file triples (filename,
size, media-type, tie-type, `parent` edge) and audio metadata triples
(title, artist, album-artist, album, year, track, duration) — the same set
`tie import` writes. Re-importing a same-named file versions the old
content into the `_prev` history directory (bounded by the tie config's
`PrevVersions`).

Per album root: the dir-type label (e.g. `audio-dir`), the form's tags, the
folder name, the import date, and the aggregated artist/album/year — so
media apps can title and sort the album without listing it.

## Caveats

- **Album zips** (audio archives) import as single blobs at their on-disk
  location, classified `audio-archive` by content. Templates and disc
  placement do not apply — their tags are not readable at scan time.
- **Untagged albums** (no album/artist tags) cannot render a template and
  stay at their source paths; multi-disc sets whose tracks lack album tags
  cannot even merge — each disc directory becomes its own album. Tag first
  (e.g. with MusicBrainz Picard), then import.
- **Merged groups** (multi-disc sets and scattered rips unified by tags)
  import their audio files only — cover art in the source folders is left
  behind (a long-standing limitation; whole-tree albums keep theirs).
- **Archives and merged groups are additive**: they never version away
  files that other albums placed in the same directories.
- Albums nested inside another album's tree import are flagged in the
  review dialog (their files would appear in both).
