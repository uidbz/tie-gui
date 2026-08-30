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

// streamableExts are audio/video types worth streaming over HTTP (players like
// mpv/vlc open a URL natively) rather than downloading fully before playback.
var streamableExts = map[string]bool{
	"mp4": true, "mkv": true, "webm": true, "mov": true, "avi": true,
	"m4v": true, "mpg": true, "mpeg": true, "wmv": true, "flv": true, "ts": true,
	"mp3": true, "flac": true, "m4a": true, "aac": true, "ogg": true,
	"opus": true, "wav": true, "wma": true,
}

// isStreamable reports whether name's extension is an audio/video type that a
// media player can stream from a URL instead of a downloaded file.
func isStreamable(name string) bool {
	return streamableExts[config.ExtKey(name)]
}

// openLocal launches a local file using the command configured for its file
// type, falling back to xdg-open when none is set.
func openLocal(cfg *config.Config, localPath, name string) error {
	if cfg != nil {
		if cmdline := cfg.AppFor(name); cmdline != "" {
			cmd := buildCommand(cmdline, localPath)
			if cmd == nil {
				return fmt.Errorf("invalid open command %q", cmdline)
			}
			return cmd.Start()
		}
	}
	return exec.Command("xdg-open", localPath).Start()
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
// the association for that extension, persists, and (when open is true) opens
// localPath now. onChanged runs after a successful save.
func promptOpenWith(win fyne.Window, cfg *config.Config, name, localPath string, open bool, onChanged func()) {
	ext := config.ExtKey(name)
	if ext == "" {
		dialog.ShowInformation("Open with", "This file has no extension to associate an app with.", win)
		return
	}
	entry := widget.NewEntry()
	entry.SetPlaceHolder("e.g. mpv %f  or  gimp")
	entry.SetText(cfg.AppFor(name))
	dialog.ShowForm(fmt.Sprintf("Open .%s with", ext), "Save", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Command", entry),
			widget.NewFormItem("", widget.NewLabel("%f is replaced by the file path (else appended).")),
		},
		func(ok bool) {
			if !ok {
				return
			}
			cfg.SetApp(ext, entry.Text)
			if err := cfg.Save(); err != nil {
				dialog.ShowError(err, win)
			}
			if onChanged != nil {
				onChanged()
			}
			if open && localPath != "" {
				if err := openLocal(cfg, localPath, name); err != nil {
					dialog.ShowError(err, win)
				}
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
		if ext != "" {
			cmdEntry.SetText(cfg.FileApps[ext])
		}
		dialog.ShowForm("File association", "Save", "Cancel",
			[]*widget.FormItem{
				widget.NewFormItem("Extension", extEntry),
				widget.NewFormItem("Command", cmdEntry),
			},
			func(ok bool) {
				if !ok {
					return
				}
				newExt := config.ExtKey("." + strings.TrimPrefix(extEntry.Text, "."))
				// If the key was renamed, drop the old entry.
				if ext != "" && newExt != ext {
					cfg.SetApp(ext, "")
				}
				cfg.SetApp(newExt, cmdEntry.Text)
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
			b.Objects[0].(*widget.Label).SetText(fmt.Sprintf(".%s  →  %s", ext, cfg.FileApps[ext]))
			btns := b.Objects[1].(*fyne.Container).Objects
			btns[0].(*widget.Button).OnTapped = func() { editExt(ext) }
			btns[1].(*widget.Button).OnTapped = func() {
				cfg.SetApp(ext, "")
				save()
			}
		})

	add := widget.NewButtonWithIcon("Add", theme.ContentAddIcon(), func() { editExt("") })
	content := container.NewBorder(nil, add, nil, nil, list)
	d := dialog.NewCustom("File associations", "Close", content, win)
	d.Resize(fyne.NewSize(480, 360))
	d.Show()
}
