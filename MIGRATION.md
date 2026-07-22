# Migration Guide: tie v0.3.5 → master (v0.4.0-dev)

## Overview
This document describes the changes needed to migrate imgview to use the latest tie master branch.

## Config File Changes

The tie config file format has changed. You need to update `~/.config/tie/config.toml` (and any other tie config files).

### Old Format (v0.3.5)
```toml
[FileHosts]
fast = 'https://localhost:1169'
backup = 'https://localhost:1169'
```

### New Format (master/v0.4.0)
```toml
[FileHosts.fast]
URL = 'https://localhost:1169'
Insecure = true  # Set to true for self-signed certificates

[FileHosts.backup]
URL = 'https://localhost:1169'
Insecure = true
```

**Note:** The `Insecure` field controls whether to skip TLS certificate verification. Set to `true` for self-signed certificates or localhost development.

## Code Changes

### 1. API Changes from Callbacks to Return Values

**Before:**
```go
viewer.Tie.SimpleGet("tags", func(r client.GetReply) {
    if r.Success {
        // Handle result
    }
})
```

**After:**
```go
go func() {
    r, err := viewer.Tie.SimpleGet("tags")
    if err == nil {
        // Handle result
        fyne.Do(func() {
            // UI operations must be wrapped in fyne.Do
        })
    }
}()
```

### 2. Transform API Removed

**Before:**
```go
intersect := make([]api.Transform, len(include)-1)
for i := 1; i < len(include); i++ {
    intersect[i-1] = api.Transform{
        Key:     include[i],
        Reverse: true,
    }
}
o := client.GetOptions{
    Intersect: intersect,
    // ...
}
```

**After:**
```go
// First tag is seed key, rest go in Include
var includeRest []string
if len(include) > 1 {
    includeRest = include[1:]
}
o := client.GetOptions{
    Include: includeRest,
    Exclude: exclude,
    // ...
}
```

### 3. FileHost Type Change

**Before:**
```go
host := viewer.Tie.Config.FileHosts["fast"] // Returns string
```

**After:**
```go
host := viewer.Tie.Config.FileHosts["fast"].URL // FileHost is now a struct
insecure := viewer.Tie.Config.FileHosts["fast"].Insecure
```

### 4. getlib.ReadFile Signature Change

**Before:**
```go
r, err := getlib.ReadFile(host, hash)
```

**After:**
```go
r, err := getlib.ReadFile(httpClient, host, hash)
// Pass nil for httpClient to use default
r, err := getlib.ReadFile(nil, host, hash)
```

## Building

The application now requires the `migrated_fynedo` build tag to indicate it has been fully migrated to the fyne.Do threading model:

```bash
go build -tags migrated_fynedo
```

The build script has been updated to include this tag automatically.

## Testing

After migration, test both normal and tie modes:

```bash
# Normal mode
./imgview /path/to/images

# Tie mode
./imgview -tie
```

Expected behavior:
- No fyne.Do threading warnings
- TLS errors are expected if tie-daemon isn't running with proper certificates
- Config parsing should succeed with no errors
