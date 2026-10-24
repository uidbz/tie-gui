package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/musicbrainz"
)

// ShowMusicBrainzSettings edits tie-fm's MusicBrainz backend settings.
func ShowMusicBrainzSettings(win fyne.Window, cfg *config.Config) {
	mb := cfg.MusicBrainz.WithDefaults()

	const localLabel = "Local database"
	const webLabel = "Web service"

	source := widget.NewRadioGroup([]string{localLabel, webLabel}, nil)
	source.Horizontal = true
	if mb.Source == musicbrainz.SourceWeb {
		source.SetSelected(webLabel)
	} else {
		source.SetSelected(localLabel)
	}

	dsn := widget.NewEntry()
	dsn.SetText(mb.DBDSN)
	dsn.SetPlaceHolder(musicbrainz.DefaultDSN)

	webURL := widget.NewEntry()
	webURL.SetText(mb.WebBaseURL)
	webURL.SetPlaceHolder(musicbrainz.DefaultWebBaseURL)

	coverURL := widget.NewEntry()
	coverURL.SetText(mb.CoverArtBaseURL)
	coverURL.SetPlaceHolder(musicbrainz.DefaultCoverArtBaseURL)

	contact := widget.NewEntry()
	contact.SetText(mb.Contact)
	contact.SetPlaceHolder("you@example.org (optional, for the web service)")

	autoMatch := widget.NewCheck("Look up releases automatically when importing albums", nil)
	autoMatch.SetChecked(mb.AutoMatch)
	coverArt := widget.NewCheck("Download missing cover art (Cover Art Archive)", nil)
	coverArt.SetChecked(mb.FetchCoverArt)

	localBox := container.NewVBox(
		widget.NewLabelWithStyle("Local database (full MusicBrainz mirror)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		dsn,
		widget.NewLabel("PostgreSQL DSN; the default points at the local mirror on port 5432."),
	)
	webBox := container.NewVBox(
		widget.NewLabelWithStyle("Web service", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewForm(
			widget.NewFormItem("Base URL", webURL),
			widget.NewFormItem("Contact", contact),
		),
		widget.NewLabel("The public API is limited to about one request per second."),
	)
	coverForm := widget.NewForm(widget.NewFormItem("Cover Art Archive", coverURL))

	updateVisibility := func() {
		if source.Selected == webLabel {
			localBox.Hide()
			webBox.Show()
		} else {
			localBox.Show()
			webBox.Hide()
		}
	}
	source.OnChanged = func(string) { updateVisibility() }
	updateVisibility()

	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord

	current := func() config.MusicBrainz {
		out := mb
		if source.Selected == webLabel {
			out.Source = musicbrainz.SourceWeb
		} else {
			out.Source = musicbrainz.SourceLocal
		}
		out.DBDSN = strings.TrimSpace(dsn.Text)
		out.WebBaseURL = strings.TrimSpace(webURL.Text)
		out.CoverArtBaseURL = strings.TrimSpace(coverURL.Text)
		out.Contact = strings.TrimSpace(contact.Text)
		out.AutoMatch = autoMatch.Checked
		out.FetchCoverArt = coverArt.Checked
		return out
	}

	var testBtn *widget.Button
	testBtn = widget.NewButton("Test connection", func() {
		status.SetText("Testing…")
		testBtn.Disable()
		c := current()
		go func() {
			p, err := openMusicBrainz(c)
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				err = p.Ping(ctx)
				cancel()
				p.Close()
			}
			fyne.Do(func() {
				testBtn.Enable()
				if err != nil {
					status.SetText("Connection failed: " + err.Error())
					return
				}
				status.SetText("Connection OK (" + c.Source + ")")
			})
		}()
	})

	content := container.NewVBox(
		widget.NewLabelWithStyle("MusicBrainz tagging", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Source used for album/track lookups in album import and on tie: paths."),
		source,
		widget.NewSeparator(),
		localBox,
		webBox,
		coverForm,
		widget.NewSeparator(),
		autoMatch,
		coverArt,
		container.NewHBox(testBtn),
		status,
	)

	d := dialog.NewCustomConfirm("MusicBrainz settings", "Save", "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		c := current()
		if c.Source == musicbrainz.SourceLocal && c.DBDSN == "" {
			dialog.ShowError(fmt.Errorf("local database DSN must not be empty"), win)
			return
		}
		if c.Source == musicbrainz.SourceWeb && c.WebBaseURL == "" {
			dialog.ShowError(fmt.Errorf("web service base URL must not be empty"), win)
			return
		}
		cfg.MusicBrainz = c
		if err := cfg.Save(); err != nil {
			dialog.ShowError(err, win)
		}
	}, win)
	d.Resize(fyne.NewSize(620, 520))
	d.Show()
}
