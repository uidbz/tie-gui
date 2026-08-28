# Documentation Index

Comprehensive technical documentation for the imgview/tie-view project.

## Getting Started

- **[QUICKSTART.md](QUICKSTART.md)** — Get up and running in 5 minutes
  - Installation instructions for all platforms
  - First run examples
  - Basic configuration
  - Troubleshooting guide

## Architecture & Design

- **[ARCHITECTURE.md](ARCHITECTURE.md)** — Technical overview and design patterns
  - System architecture diagram
  - Gallery library components
  - Extension mechanism
  - Platform abstraction
  - Layout engine details
  - Threading model
  - Caching strategy
  - Mobile optimizations
  - Performance characteristics

## Development

- **[refactoring-progress.md](refactoring-progress.md)** — Complete refactoring change log
  - All six phases documented with commit references
  - Before/after comparisons
  - Rationale for each change
  - Verification results

- **[refactoring-design-decisions.md](refactoring-design-decisions.md)** — Design rationale and trade-offs
  - Why certain patterns were chosen
  - What alternatives were considered
  - Technical debt decisions

- **[code-quality-review-roadmap.md](code-quality-review-roadmap.md)** — Original refactoring plan
  - Initial assessment of codebase issues
  - Six-phase roadmap with priorities
  - Success criteria

## Codebase Reference

- **[../CLAUDE.md](../CLAUDE.md)** — Comprehensive LLM-oriented codebase reference
  - Repository structure
  - Key types and interfaces
  - Gallery layout algorithm
  - Thumbnail pipeline
  - Navigation and fullscreen
  - Tag sidebar (tie-view)
  - Image tagger (tie-view)
  - Config options
  - Threading model
  - Common patterns

- **[../gallery/extension.go](../gallery/extension.go)** — Extension API documentation
  - Core interfaces (CustomReader, Thumbnailer)
  - Optional behaviors (Openable, VideoFile, etc.)
  - Callback extension points
  - Stability guarantees
  - Usage examples

## Mobile Development

- **[ANDROID.md](ANDROID.md)** — Android-specific development notes
  - Build instructions
  - Platform-specific behaviors
  - Touch gesture handling
  - Storage access

## Quick Links

### For New Users
1. Start with [QUICKSTART.md](QUICKSTART.md)
2. Customize via config file (see QUICKSTART.md configuration section)
3. Explore keyboard shortcuts (see QUICKSTART.md reference card)

### For Developers
1. Read [ARCHITECTURE.md](ARCHITECTURE.md) for system overview
2. Review [../gallery/extension.go](../gallery/extension.go) for extension API
3. See [../CLAUDE.md](../CLAUDE.md) for detailed codebase reference
4. Check [refactoring-progress.md](refactoring-progress.md) for recent changes

### For Contributors
1. Read [refactoring-design-decisions.md](refactoring-design-decisions.md) to understand design philosophy
2. Follow patterns documented in [ARCHITECTURE.md](ARCHITECTURE.md)
3. Reference [refactoring-progress.md](refactoring-progress.md) for coding standards
4. See main [README.md](../README.md) contributing section

## Documentation Philosophy

**Clear and concise:** Technical accuracy without unnecessary verbosity

**Example-driven:** Show, don't just tell. Code examples for key concepts.

**Layered depth:** Quick start for users, architecture for developers, detailed reference for maintainers.

**Living documents:** Updated alongside code changes. Stale documentation is worse than no documentation.

## Document Status

| Document | Last Updated | Status |
|----------|-------------|--------|
| QUICKSTART.md | 2024-08-12 | ✅ Current |
| ARCHITECTURE.md | 2024-08-12 | ✅ Current |
| refactoring-progress.md | 2024-08-12 | ✅ Current |
| refactoring-design-decisions.md | 2024-08-12 | ✅ Current |
| code-quality-review-roadmap.md | 2024-08-12 | ✅ Current |
| CLAUDE.md | 2024-08-12 | ✅ Current |
| gallery/extension.go | 2024-08-12 | ✅ Current |
| ANDROID.md | 2024-08-07 | ✅ Current |

## Feedback

Found an error or unclear section? Please open an issue on the [issue tracker](https://todo.sr.ht/~uid/imgview).

Documentation improvements are always welcome!
