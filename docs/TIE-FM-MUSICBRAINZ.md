# MusicBrainz tagging in tie-fm

tie-fm can look albums and tracks up on MusicBrainz and write the result into
the tie store. Two places use it:

* **Import as albums…** (local directories): match each scanned album before or
  during review; the chosen release drives the album metadata, the rendered
  destination, per-track titles/numbers and (optionally) cover art — or, with
  **Only import tags from MusicBrainz**, just adds the release's
  genres/community tags.
* **tie: paths**: right-click an album directory or an audio file →
  **MusicBrainz…** to match and re-tag an already imported item.

## Backends

Chosen in **Menu → MusicBrainz…**:

| Source | Description |
| --- | --- |
| **Local database** (default) | Direct SQL against a full MusicBrainz PostgreSQL mirror (`musicbrainz` + `cover_art_archive` schemas). Offline, no rate limit. Default DSN: `postgres://musicbrainz:musicbrainz@127.0.0.1:5432/musicbrainz_db?sslmode=disable`. |
| **Web service** | `https://musicbrainz.org/ws/2` (Lucene search, ~1 request/second, needs internet). Set a contact URL/address for the User-Agent. |

Both backends return the same model, so the UI is identical. The local backend
matches album titles fuzzily in Go (normalised exact/prefix/contains + token
overlap + year/track-count bonuses) after narrowing by artist, so mistyped tags
still find the release.

The settings dialog has a **Test connection** button and persists to tie-fm's
own `config.toml`:

```toml
[MusicBrainz]
Source = "local"
DBDSN = "postgres://musicbrainz:musicbrainz@127.0.0.1:5432/musicbrainz_db?sslmode=disable"
WebBaseURL = "https://musicbrainz.org"
CoverArtBaseURL = "https://coverartarchive.org"
AutoMatch = true
FetchCoverArt = true
```

## Album import

1. Right-click local directories → **Import as albums…**.
2. The form gains three options (defaults from the settings):
   * **Look up releases on MusicBrainz** — after the scan, every album is
     searched and the best match (score ≥ 0.55) is applied automatically.
   * **Only import tags from MusicBrainz (no metadata)** — keep the album's own
     tags/placement and apply only the matched release's genres/community tags
     after import (the same union the tag panel's tags-only action uses).
     Requires the lookup; it disables the cover-art option and neither MBIDs
     nor track/album metadata are written. The review dialog still shows the
     match and lets you pick another release, but picking one does **not**
     re-render the destination.
   * **Download missing cover art** — when the album has no cover/thumbnail and
     the release has a front image in the Cover Art Archive, it is downloaded
     after import (not available in tags-only mode).
3. In the review dialog each row shows `MB: Artist — Album (year)` when
   matched, plus an **MB…** button to search again/choose another release.
   Picking a release re-renders the destination from the template, so
   `{albumartist}/{year} - {album}` uses the matched values (full-metadata
   mode only).
4. Import writes the normal tags first, then a single background pass applies:
   * album dir: `artist`, `album-artist`, `album`, `year`, genres as tags,
     `musicbrainz-release-id`, `musicbrainz-release-group-id`,
     `musicbrainz-artist-id`;
   * tracks: `title`, `artist`, `album`, `album-artist`, `year`, `track`,
     `disc`, `track-total`, `disc-total`, `musicbrainz-recording-id`,
     `musicbrainz-track-id`, release/group/artist IDs;
   * `IndexCollection` refreshes the album summary/member-of edges;
   * the front cover becomes the album's `thumbnail` relation (blob uploaded
     with infinite retention) when cover art is enabled.
   In tags-only mode the pass writes just the album directory's
   genres/community tags (union with the tags entered for the import) and
   nothing else.
   Track matching is by `(disc, track)`, then track number (single-disc),
   then normalised title, then remaining order — so files with partial tags
   still line up.

## Tags only (tag panel)

The right-hand **tag panel** (the "tag bag") is the tags-only entry point:

1. Browse a `tie:/…` location and select an album row (the leftmost icon
   column; clicking the name opens the folder).
2. In **Tags of selection**, press **Add MusicBrainz tags…**.
3. Pick the release. Only its **genres + community tags** are unioned into the
   album's tags — no artist/album/year, no MBIDs, no track metadata, no cover
   art. Existing tags and their spelling are preserved; duplicates are ignored
   case-insensitively.
4. With several albums selected, each is matched automatically and its own tags
   are added; a summary lists unmatched/failed albums.

The newly added chips appear in the editor immediately; the full-metadata
match remains available from the row context menu's **MusicBrainz…** action.

## tie: paths

Right-click a row in a `tie:/…` listing:

* **album directory** → release search (prefilled with the directory's
  `album-artist`/`album`/`year`), apply writes the same metadata as above;
* **audio file** → recording search (prefilled with `artist`/`title`), apply
  writes `title`, `artist`, the containing album/year, and
  `musicbrainz-recording-id`/`musicbrainz-release-id`;
* **multiple selected tracks** → treated as their shared parent album.

A failed cover-art download is reported as a non-fatal omission: the tag
writes stand.

## Tests

```sh
go test ./cmd/tie-fm/internal/musicbrainz ./cmd/tie-fm/internal/ui
# integration against the local mirror (after importing it):
TIE_FM_MB_TEST=1 go test ./cmd/tie-fm/internal/musicbrainz -run TestLocalProviderIntegration -v
```

The web provider tests use `httptest` fixtures; the local integration test is
opt-in and skips without `TIE_FM_MB_TEST`.
