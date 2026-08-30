package ui

import (
	"embed"
	"mime"
	"path"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// iconFS holds the vendored KDE Breeze mimetype icons (light "breeze" and dark
// "breeze-dark" variants). They are LGPL-3+; see icons/LICENSE and
// icons/COPYRIGHT. Only the mimetype icons are vendored: Breeze's folder/places
// icons rely on CSS color-scheme classes that Fyne's SVG renderer ignores, so
// directories use the built-in (theme-aware) folder icon instead.
//
//go:embed icons/breeze icons/breeze-dark
var iconFS embed.FS

// iconCache resolves filetype icons from the embedded Breeze set and caches the
// loaded SVG resources.
type iconCache struct {
	mu  sync.Mutex
	res map[string]fyne.Resource // key: "<theme>/<name>"; nil value = known-missing
}

var icons = &iconCache{res: map[string]fyne.Resource{}}

// FileIcon returns a Breeze icon for a filename, choosing the light or dark
// variant from the current theme. It falls back to Fyne's generic file icon
// when no vendored Breeze icon matches.
func (c *iconCache) FileIcon(name string, variant fyne.ThemeVariant) fyne.Resource {
	themeName := "breeze"
	alt := "breeze-dark"
	if variant == theme.VariantDark {
		themeName, alt = alt, themeName
	}
	for _, cand := range iconNamesFor(name) {
		if r := c.load(themeName, cand); r != nil {
			return r
		}
		if r := c.load(alt, cand); r != nil {
			return r
		}
	}
	return theme.FileIcon()
}

// load returns the cached Breeze resource for (theme, name), reading it from the
// embedded FS on first request. A miss is cached as nil so repeated lookups are
// cheap.
func (c *iconCache) load(themeName, name string) fyne.Resource {
	key := themeName + "/" + name
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.res[key]; ok {
		return r
	}
	var res fyne.Resource
	if b, err := iconFS.ReadFile("icons/" + themeName + "/" + name + ".svg"); err == nil {
		res = fyne.NewStaticResource(name+".svg", b)
	}
	c.res[key] = res
	return res
}

// iconNamesFor returns Breeze mimetype icon-name candidates for a filename, most
// specific first, ending in generic fallbacks that always exist in Breeze.
func iconNamesFor(name string) []string {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	var c []string
	add := func(n string) {
		if n == "" {
			return
		}
		for _, e := range c {
			if e == n {
				return
			}
		}
		c = append(c, n)
	}

	if n, ok := extIcon[ext]; ok {
		add(n)
		if i := strings.IndexByte(n, '-'); i > 0 && n[:i] != "application" {
			add(n[:i] + "-x-generic")
		}
	}
	if mt := mime.TypeByExtension("." + ext); mt != "" {
		mt = strings.SplitN(mt, ";", 2)[0]
		if i := strings.IndexByte(mt, '/'); i > 0 {
			top := mt[:i]
			add(top + "-" + mt[i+1:])
			if top != "application" {
				add(top + "-x-generic")
			}
		}
	}
	add("application-octet-stream")
	return c
}

// extIcon maps common file extensions to Breeze mimetype icon base names. It
// covers cases where mime.TypeByExtension is missing or where a more specific
// Breeze icon exists than the MIME type implies.
var extIcon = map[string]string{
	"pdf": "application-pdf",
	"doc": "x-office-document", "docx": "x-office-document", "odt": "x-office-document", "rtf": "x-office-document",
	"xls": "x-office-spreadsheet", "xlsx": "x-office-spreadsheet", "ods": "x-office-spreadsheet", "csv": "x-office-spreadsheet",
	"ppt": "x-office-presentation", "pptx": "x-office-presentation", "odp": "x-office-presentation",
	"epub": "application-epub+zip",
	"txt":  "text-x-generic", "md": "text-x-generic", "log": "text-x-generic", "ini": "text-x-generic", "conf": "text-x-generic", "toml": "text-x-generic", "yaml": "text-x-generic", "yml": "text-x-generic",
	"html": "text-html", "htm": "text-html",
	"css":  "text-css",
	"xml":  "text-xml",
	"json": "application-json",
	"sh":   "application-x-shellscript", "bash": "application-x-shellscript", "zsh": "application-x-shellscript",
	"py":  "text-x-python",
	"go":  "text-x-go",
	"js":  "application-javascript",
	"c":   "text-x-csrc", "h": "text-x-chdr", "cpp": "text-x-c++src", "cc": "text-x-c++src", "hpp": "text-x-c++hdr",
	"zip": "application-zip", "gz": "application-gzip", "tar": "application-x-tar",
	"bz2": "application-x-bzip", "xz": "application-x-xz-compressed-tar", "7z": "application-x-7z-compressed",
	"rar": "application-vnd.rar", "deb": "application-vnd.debian.binary-package",
	"png": "image-png", "jpg": "image-jpeg", "jpeg": "image-jpeg", "gif": "image-gif",
	"webp": "image-webp", "bmp": "image-bmp", "tiff": "image-tiff", "svg": "image-svg+xml", "ico": "image-x-generic",
	"mp3": "audio-mpeg", "flac": "audio-x-flac", "wav": "audio-x-wav", "ogg": "audio-x-vorbis+ogg", "m4a": "audio-mp4", "opus": "audio-x-generic",
	"mp4": "video-mp4", "mkv": "video-x-matroska", "webm": "video-webm", "avi": "video-x-msvideo", "mov": "video-quicktime",
	"appimage": "application-vnd.appimage",
}
