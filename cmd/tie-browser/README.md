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
- **F4** or the pencil opens the in-place designer; **Done** validates, saves,
  indexes and reloads. The page script is stopped while editing.
- `(dirUID, scene-doc, hash)` is the current version pointer; `member-of`
  edges list the media a scene presents, `links-to` records scene navigation,
  so "which scenes use this photo?" and "what links here?" are tie queries.
- The generated `thumbnail.png` is stored on the directory and refreshed after
  each save.

## Run

```sh
go run ./cmd/tie-browser
go run ./cmd/tie-browser -config work -collection main
go run ./cmd/tie-browser -home tie:/scenes/house/living-room
```

The first run creates `tie:/scenes/home` with a starter page. Opening tie media
uses the system handler; playback can later route through pwplay/mpv.

## Components

The `tie.*` family is registered by `scene/render/tie`:
`tie.image`, `tie.gallery`, `tie.collection`, `tie.tags`, `tie.related`,
`tie.document`, `tie.audio`, `tie.video`. The designer palette picks them up
automatically; `tie.gallery` runs a live tag query through the collection.

## Status

Implemented: scene pages over tie, home scene, F4 designer with tie components
and assets, save with conflict handling, member/link indexing, thumbnails.
Roadmap: native pwplay/mpv playback, tag picker/thumbnail asset picker,
Android touch-first layout, multi-scene story map over the store.
