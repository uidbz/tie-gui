# Migration Guide: tie v0.3.5 → master (v0.4.1-dev)

## Overview
This document describes the changes needed to migrate imgview to the latest tie master branch.

## Config File Changes

The tie config file format has changed. You need to update `~/.config/tie/config.toml` (and any other tie config files).

### Old Format (v0.3.5)
```toml
[FileHosts]
fast = 'https://localhost:1169'
backup = 'https://localhost:1169'
```

### New Format (master/v0.4.x)
```toml
DefaultFileHosts = ['fast']

[FileHosts.fast]
URL = 'https://localhost:1169'
Insecure = true  # Set to true for self-signed certificates

[FileHosts.backup]
URL = 'https://localhost:1169'
Insecure = true
```

**Notes:**
- The `Insecure` field controls whether to skip TLS certificate verification. Set to `true` for self-signed certificates or localhost development. imgview honors it for both downloads and thumbnail uploads.
- imgview fetches content from the `fast` filehost when configured, otherwise from the first entry of `DefaultFileHosts`.

## Code Changes

### 1. Triple-set API replaced by flat Rows

`SimpleGet`, `Get(key, GetOptions)` and the `TripleSet` result type are gone. The
client surface is now:

- `Get(key) (Row, error)` — one key's forward attributes.
- `Query(QuerySpec) ([]Row, int, error)` — tag/association search with ordering and pagination.
- `Expand(keys) ([]Row, error)` — many keys in one round trip.
- `Set(key, relation, values)` — replace a relation's values in one op.

A `Row` is `{Key, Attributes: map[relation][]values}`; read it with the helpers
`client.RowValues(row, rel)`, `client.RowFirst(row, rel)` and `client.RowHas(row, rel, val)`.

**Before:**
```go
viewer.Tie.SimpleGet("tags", func(r client.GetReply) {
    if r.Success {
        r.Result.ForEachValue2(func(key, val1, val2 string) { ... })
    }
})
```

**After:**
```go
go func() {
    row, err := viewer.Tie.Get("tags")
    if err == nil {
        fyne.Do(func() {
            for _, tag := range client.RowValues(row, "all") {
                ts.AddTag(tag)
            }
        })
    }
}()
```

### 2. GetOptions/Transform replaced by QuerySpec

There is no positional seed key anymore; `Terms` is the full AND-list. With
`Expand: true` each match's own forward attributes are attached to its Row, so
per-image lookups (e.g. thumbnail mappings) need no extra round trips.

**Before:**
```go
o := client.GetOptions{
    Include: include[1:],
    Exclude: exclude,
    Reverse: true,
    Filter:  filter,
    Sort:    tiedb.SortOptions{Limit: -1},
}
r, err := viewer.Tie.Get(include[0], o)
r.Result.ForEachKey(func(hash string) { ... })
```

**After:**
```go
rows, _, err := viewer.Tie.Query(client.QuerySpec{
    Terms:   include, // full AND-list, no seed
    Exclude: exclude,
    Reverse: true,
    Filter:  filter,
    Expand:  true,
    Limit:   -1, // disable pagination
})
for _, row := range rows {
    hash := row.Key
    thumbHash := client.RowFirst(row, "thumbnail")
}
```

### 3. FileHost Type Change

**Before:**
```go
host := viewer.Tie.Config.FileHosts["fast"] // Returns string
```

**After:**
```go
host := viewer.Tie.Config.FileHosts["fast"]     // FileHost struct
url := host.URL
insecure := host.Insecure
```

### 4. getlib/putlib take an *http.Client

```go
r, err := getlib.ReadFile(httpClient, host, hash)
// Pass nil to use http.DefaultClient; pass a custom client for
// InsecureSkipVerify (self-signed filehosts).
```

Uploads PUT to `<filehost>/upload/<hash>` (content-addressed), e.g. via
`putlib.PutConfig{Client: httpClient}.UploadMultipart(...)`.

## Tie-mode thumbnails live on the filehost

Thumbnails for tie images are no longer stored in a local directory. On first
view imgview generates the thumbnail from the full blob, uploads it to the
filehost (content-addressed, so identical thumbnails dedupe across machines),
and records a `(imageHash, "thumbnail", thumbHash)` triple via `Tie.Set`.
Later views follow the mapping and fetch the thumbnail blob from the filehost;
the mapping is trusted once written. The local `ThumbnailDir` setting only
applies to non-tie (local disk) images.

## Building

Build with the `migrated_fynedo` tag to assert the fyne.Do threading model
(without it fyne prints a migration warning on startup):

```bash
go build -tags migrated_fynedo ./cmd/imgview ./cmd/tieview
```

The build script has been updated to include this tag automatically.

## Testing

After migration, test both normal and tie modes:

```bash
# Normal mode
./imgview /path/to/images

# Tie mode
./tieview -tag favorite
```

Expected behavior:
- No fyne.Do threading warnings (with the build tag).
- Tie mode shows the query results on startup and after changing the tag selection.
- First tie-mode view uploads thumbnails to the filehost (`Set` requests); subsequent
  views only fetch the thumbnail blobs.
- TLS errors are expected if tie-daemon isn't running with proper certificates.
- Config parsing should succeed with no errors.
