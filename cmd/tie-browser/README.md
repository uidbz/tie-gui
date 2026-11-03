# tie-browser

A point-and-click browser for [tie](https://github.com/uidbz/tie) scenes, built
on [fynerisor](https://github.com/uidbz/fynerisor)'s scene runtime and designer.

A scene is a directory in a tie collection:

```
tie:/scenes/home/
  scene.json      content-addressed, versioned in _prev
  page.risor      optional sidecar script
  thumbnail.png   generated on save
```

- `scene.json` is a declarative page: x/y hotspots, layout containers, tie
  media components, actions and optional Risor behaviour.
- **Ctrl+E** (or the pencil) opens the in-place designer; **Done** validates,
  saves, indexes and reloads. **Ctrl+S** saves without leaving, including while
  an inspector field has focus. The page script is stopped while editing.
- A scene renders data-only first. A sidecar script adds behaviour afterwards,
  so a broken script never leaves a blank page (the error is shown in the
  status bar).
- `(dirUID, scene-doc, hash)` is the current version pointer; `member-of`
  edges list the media a scene presents, `links-to` records scene navigation,
  so "which scenes use this photo?" and "what links here?" are tie queries.
- The generated `thumbnail.png` is stored on the directory and refreshed after
  each save (best effort: asset loads get a short window to finish).

## Run

```sh
go run ./cmd/tie-browser
go run ./cmd/tie-browser -config work -collection main
go run ./cmd/tie-browser -home tie:/scenes/house/living-room
```

The first run creates `tie:/scenes/home` with a starter page, but only when
the scene directory does not exist at all: a directory that exists without a
document is reported instead of being overwritten. Loading happens in the
background, so the window appears immediately even when the triplestore is
unreachable (an error panel with Retry is shown).

`tie.audio` `play` actions stream the blob to a pwplay-server (`-pwplay`,
default `http://localhost:8080`); tapping a `tie.audio` card plays it even
without an explicit action. Playback replaces the pwplay queue and waits for
the async queue mutation to settle, so the tapped track actually starts.
Images/documents and the `tie.video` card use the system handler; downloads
run off the UI thread.

## Components

The `tie.*` family is registered by `scene/render/tie`:
`tie.image`, `tie.gallery`, `tie.collection`, `tie.tags`, `tie.related`,
`tie.document`, `tie.audio`, `tie.video`. The designer palette picks them up
automatically; `tie.gallery` runs a live tag query through the collection.

## Status

Implemented: scene pages over tie, home scene, Ctrl+E/F4 designer with tie
components and assets, save with conflict handling and a persistent save
indicator, queued/coalesced saves, member/link indexing, thumbnails,
media/scene asset pickers, pwplay audio playback, and a touch-first designer
layout. The story map loads linked scenes.

Roadmap: native mpv video surfaces, richer tag/chip pickers, and editing the
sidecar script in place.
