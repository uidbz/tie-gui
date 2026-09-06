package ui

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/config"
)

// streamableExts are audio/video types likely to be opened in players like
// mpv/vlc that stream over HTTP, so the "App supports streaming" checkbox is
// pre-checked for them on new associations.
var streamableExts = map[string]bool{
	"mp4": true, "mkv": true, "webm": true, "mov": true, "avi": true,
	"m4v": true, "mpg": true, "mpeg": true, "wmv": true, "flv": true, "ts": true,
	"mp3": true, "flac": true, "m4a": true, "aac": true, "ogg": true,
	"opus": true, "wav": true, "wma": true,
}

// isStreamable reports whether name's extension is an audio/video type whose
// association is likely to stream from a URL instead of a downloaded file.
func isStreamable(name string) bool {
	return streamableExts[config.ExtKey(name)]
}

// openLocal launches a local file (or, for streaming associations on tie
// entries, a filehost URL) using the command configured for its file type,
// falling back to xdg-open when none is set.
func openLocal(cfg *config.Config, target, name string) error {
	if cfg != nil {
		if assoc, ok := cfg.AppFor(name); ok {
			cmd := buildCommand(assoc.Command, target)
			if cmd == nil {
				return fmt.Errorf("invalid open command %q", assoc.Command)
			}
			return cmd.Start()
		}
	}
	return exec.Command("xdg-open", target).Start()
}

// buildCommand splits a stored command line into an *exec.Cmd for opening file.
// A "%f" token in any argument is replaced with the path; otherwise the path is
// appended as the final argument. Arguments are split on whitespace (no shell,
// so quoted arguments containing spaces are not supported).
func buildCommand(cmdline, file string) *exec.Cmd {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return nil
	}
	replaced := false
	args := make([]string, 0, len(fields))
	for _, f := range fields[1:] {
		if strings.Contains(f, "%f") {
			f = strings.ReplaceAll(f, "%f", file)
			replaced = true
		}
		args = append(args, f)
	}
	if !replaced {
		args = append(args, file)
	}
	return exec.Command(fields[0], args...)
}

// promptOpenWith asks for a command to open files of name's type, stores it as
// the association for that extension, and persists. onSaved runs after a
// successful save.
func promptOpenWith(win fyne.Window, cfg *config.Config, name string, onSaved func()) {
	ext := config.ExtKey(name)
	if ext == "" {
		dialog.ShowInformation("Open with", "This file has no extension to associate an app with.", win)
		return
	}
	entry := widget.NewEntry()
	entry.SetPlaceHolder("e.g. mpv %f  or  gimp")
	streamCheck := widget.NewCheck("App supports streaming (pass tie URL instead of downloading)", nil)
	streamCheck.Checked = isStreamable(ext)
	if assoc, ok := cfg.AppFor(name); ok {
		entry.SetText(assoc.Command)
		streamCheck.Checked = assoc.Stream
	}
	dialog.ShowForm(fmt.Sprintf("Open .%s with", ext), "Save", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Command", entry),
			widget.NewFormItem("", widget.NewLabel("%f is replaced by the file path (else appended).")),
			widget.NewFormItem("", streamCheck),
		},
		func(ok bool) {
			if !ok {
				return
			}
			cfg.SetApp(ext, config.AppAssoc{Command: entry.Text, Stream: streamCheck.Checked})
			if err := cfg.Save(); err != nil {
				dialog.ShowError(err, win)
			}
			if onSaved != nil {
				onSaved()
			}
		}, win)
}

// ShowFileAssociations shows a manager for per-extension open commands: view,
// edit, add, and remove associations. Changes are persisted immediately.
func ShowFileAssociations(win fyne.Window, cfg *config.Config) {
	var list *widget.List
	keys := func() []string {
		ks := make([]string, 0, len(cfg.FileApps))
		for k := range cfg.FileApps {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}
	current := keys()

	save := func() {
		if err := cfg.Save(); err != nil {
			dialog.ShowError(err, win)
		}
		current = keys()
		list.Refresh()
	}

	editExt := func(ext string) {
		extEntry := widget.NewEntry()
		extEntry.SetText(ext)
		cmdEntry := widget.NewEntry()
		cmdEntry.SetPlaceHolder("e.g. mpv %f")
		streamCheck := widget.NewCheck("App supports streaming (pass tie URL instead of downloading)", nil)
		if ext != "" {
			assoc := cfg.FileApps[ext]
			cmdEntry.SetText(assoc.Command)
			streamCheck.Checked = assoc.Stream
		} else {
			// New association: pre-check for media extensions until the user
			// toggles the checkbox explicitly.
			streamTouched := false
			streamCheck.OnChanged = func(bool) { streamTouched = true }
			streamCheck.SetChecked(false)
			extEntry.OnChanged = func(s string) {
				if !streamTouched {
					streamCheck.SetChecked(isStreamable(s))
				}
			}
		}
		dialog.ShowForm("File association", "Save", "Cancel",
			[]*widget.FormItem{
				widget.NewFormItem("Extension", extEntry),
				widget.NewFormItem("Command", cmdEntry),
				widget.NewFormItem("", streamCheck),
			},
			func(ok bool) {
				if !ok {
					return
				}
				newExt := config.ExtKey("." + strings.TrimPrefix(extEntry.Text, "."))
				// If the key was renamed, drop the old entry.
				if ext != "" && newExt != ext {
					cfg.SetApp(ext, config.AppAssoc{})
				}
				cfg.SetApp(newExt, config.AppAssoc{Command: cmdEntry.Text, Stream: streamCheck.Checked})
				save()
			}, win)
	}

	list = widget.NewList(
		func() int { return len(current) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil, nil,
				container.NewHBox(
					widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), nil),
					widget.NewButtonWithIcon("", theme.DeleteIcon(), nil),
				),
				widget.NewLabel("template"))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			ext := current[i]
			b := o.(*fyne.Container)
			label := fmt.Sprintf(".%s  →  %s", ext, cfg.FileApps[ext].Command)
			if cfg.FileApps[ext].Stream {
				label += "  (streams)"
			}
			b.Objects[0].(*widget.Label).SetText(label)
			btns := b.Objects[1].(*fyne.Container).Objects
			btns[0].(*widget.Button).OnTapped = func() { editExt(ext) }
			btns[1].(*widget.Button).OnTapped = func() {
				cfg.SetApp(ext, config.AppAssoc{})
				save()
			}
		})

	add := widget.NewButtonWithIcon("Add", theme.ContentAddIcon(), func() { editExt("") })
	content := container.NewBorder(nil, add, nil, nil, list)
	d := dialog.NewCustom("File associations", "Close", content, win)
	d.Resize(fyne.NewSize(480, 360))
	d.Show()
}
