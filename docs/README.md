# Documentation

User and developer documentation for the tie-gui clients
(imgview, tie-view, tie-fm, tie-audio-player).

## For users

- **[QUICKSTART.md](QUICKSTART.md)** — install, first run, configuration,
  keyboard reference, troubleshooting.
- **[ANDROID.md](ANDROID.md)** — building and installing the Android APKs.

## For developers

- **[ARCHITECTURE.md](ARCHITECTURE.md)** — gallery library design: layout
  engine, extension API (`CustomReader`, `Thumbnailer`, callbacks),
  platform abstraction, threading model, caching strategy.
- **[OPTIMIZATIONS.md](OPTIMIZATIONS.md)** — memory and performance
  optimizations (LRU tile cache, painter culling, mobile config tuning).
- **[../CLAUDE.md](../CLAUDE.md)** — detailed codebase reference
  (LLM-oriented): key types, thumbnail pipeline, tag sidebar, image
  tagger, common patterns.
- **[../gallery/extension.go](../gallery/extension.go)** — the documented
  stable extension contract between the gallery library and applications.

## The tie project

These clients are front-ends for [tie](https://github.com/uidbz/tie). For
the data model, daemon, filehost, and CLI, see the tie repository and its
docs (media-database walkthrough, mount reference, internals).
