// Package tiestyle is the shared visual theme for the tie GUI apps.
//
// The palette is the tie house style: a layered dark/light surface set with a
// teal accent, warm status colors, hairline borders, and rounded controls. It
// maps both the stock Fyne color names and the tie.* surface tokens, so stock
// widgets and app-drawn surfaces repaint together on a light/dark switch.
package tiestyle

import (
	"image/color"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Tie's own theme color names. Widgets that draw a surface (sidebars, cards,
// code/table blocks) resolve these through theme.ColorForWidget so a theme
// switch repaints them with the rest of the UI.
const (
	ColorRail       fyne.ThemeColorName = "tie.rail"       // rail + status/header bar
	ColorSidebar    fyne.ThemeColorName = "tie.sidebar"    // sidebar/inspector panels
	ColorRaised     fyne.ThemeColorName = "tie.raised"     // cards, composers, pills
	ColorCode       fyne.ThemeColorName = "tie.code"       // code/table blocks
	ColorBorder     fyne.ThemeColorName = "tie.border"     // hairlines, card borders
	ColorMuted      fyne.ThemeColorName = "tie.muted"      // captions, secondary text
	ColorOK         fyne.ThemeColorName = "tie.ok"         // ready/online
	ColorWarn       fyne.ThemeColorName = "tie.warn"       // busy/warning
	ColorErr        fyne.ThemeColorName = "tie.err"        // offline/error
	ColorIdle       fyne.ThemeColorName = "tie.idle"       // unknown/idle
	ColorOKWash     fyne.ThemeColorName = "tie.okWash"     // included/positive chip fill
	ColorErrWash    fyne.ThemeColorName = "tie.errWash"    // excluded/negative chip fill
	ColorWarnWash   fyne.ThemeColorName = "tie.warnWash"   // approval/attention wash
	ColorAccentWash fyne.ThemeColorName = "tie.accentWash" // selectable highlight
)

// palette is one variant's color set. The light theme is authored separately
// rather than derived by inverting the dark one.
type palette struct {
	background  color.Color
	foreground  color.Color
	muted       color.Color
	rail        color.Color
	sidebar     color.Color
	raised      color.Color
	code        color.Color
	border      color.Color
	button      color.Color
	disabledBtn color.Color
	input       color.Color
	inputBorder color.Color
	separator   color.Color
	placeholder color.Color
	accent      color.Color
	onAccent    color.Color
	ok          color.Color
	okWash      color.Color
	warn        color.Color
	err         color.Color
	errWash     color.Color
	idle        color.Color
	onStatus    color.Color
	hover       color.Color
	pressed     color.Color
	selection   color.Color
	focus       color.Color
	scrollBar   color.Color
	scrollBarBG color.Color
	shadow      color.Color
	overlay     color.Color
	menu        color.Color
	warnWash    color.Color
	accentWash  color.Color
}

func rgba(r, g, b, a uint8) color.NRGBA { return color.NRGBA{R: r, G: g, B: b, A: a} }

var darkPalette = palette{
	background:  rgba(0x17, 0x18, 0x1b, 0xff),
	foreground:  rgba(0xe6, 0xe7, 0xea, 0xff),
	muted:       rgba(0x9a, 0x9c, 0xa3, 0xff),
	rail:        rgba(0x11, 0x12, 0x14, 0xff),
	sidebar:     rgba(0x1c, 0x1d, 0x21, 0xff),
	raised:      rgba(0x23, 0x24, 0x29, 0xff),
	code:        rgba(0x12, 0x13, 0x16, 0xff),
	border:      rgba(0x2e, 0x30, 0x36, 0xff),
	button:      rgba(0x28, 0x29, 0x2e, 0xff),
	disabledBtn: rgba(0x1f, 0x20, 0x24, 0xff),
	input:       rgba(0x1c, 0x1d, 0x21, 0xff),
	inputBorder: rgba(0x38, 0x3a, 0x41, 0xff),
	separator:   rgba(0x2e, 0x30, 0x36, 0xff),
	placeholder: rgba(0x6f, 0x72, 0x7a, 0xff),
	accent:      rgba(0x2d, 0xd4, 0xbf, 0xff),
	onAccent:    rgba(0x06, 0x1f, 0x1c, 0xff),
	ok:          rgba(0x4a, 0xde, 0x80, 0xff),
	okWash:      rgba(0x4a, 0xde, 0x80, 0x26),
	warn:        rgba(0xf5, 0x9e, 0x0b, 0xff),
	err:         rgba(0xef, 0x44, 0x44, 0xff),
	errWash:     rgba(0xef, 0x44, 0x44, 0x26),
	idle:        rgba(0x6b, 0x72, 0x80, 0xff),
	onStatus:    rgba(0x0b, 0x0d, 0x10, 0xff),
	hover:       rgba(0xff, 0xff, 0xff, 0x0d),
	pressed:     rgba(0xff, 0xff, 0xff, 0x1f),
	selection:   rgba(0x2d, 0xd4, 0xbf, 0x33),
	focus:       rgba(0x2d, 0xd4, 0xbf, 0x40),
	scrollBar:   rgba(0xff, 0xff, 0xff, 0x33),
	scrollBarBG: rgba(0x00, 0x00, 0x00, 0x00),
	shadow:      rgba(0x00, 0x00, 0x00, 0x77),
	overlay:     rgba(0x17, 0x18, 0x1b, 0xff),
	menu:        rgba(0x23, 0x24, 0x29, 0xff),
	warnWash:    rgba(0xf5, 0x9e, 0x0b, 0x1f),
	accentWash:  rgba(0x2d, 0xd4, 0xbf, 0x1f),
}

var lightPalette = palette{
	background:  rgba(0xff, 0xff, 0xff, 0xff),
	foreground:  rgba(0x1f, 0x23, 0x28, 0xff),
	muted:       rgba(0x5f, 0x66, 0x70, 0xff),
	rail:        rgba(0xee, 0xf0, 0xf2, 0xff),
	sidebar:     rgba(0xf7, 0xf8, 0xf9, 0xff),
	raised:      rgba(0xf3, 0xf4, 0xf6, 0xff),
	code:        rgba(0xf6, 0xf8, 0xfa, 0xff),
	border:      rgba(0xe2, 0xe4, 0xe8, 0xff),
	button:      rgba(0xef, 0xf1, 0xf3, 0xff),
	disabledBtn: rgba(0xf2, 0xf3, 0xf5, 0xff),
	input:       rgba(0xff, 0xff, 0xff, 0xff),
	inputBorder: rgba(0xd0, 0xd3, 0xd9, 0xff),
	separator:   rgba(0xe2, 0xe4, 0xe8, 0xff),
	placeholder: rgba(0x8a, 0x91, 0x9b, 0xff),
	accent:      rgba(0x0f, 0x76, 0x6e, 0xff),
	onAccent:    rgba(0xff, 0xff, 0xff, 0xff),
	ok:          rgba(0x15, 0x80, 0x3d, 0xff),
	okWash:      rgba(0x15, 0x80, 0x3d, 0x1f),
	warn:        rgba(0xb4, 0x53, 0x09, 0xff),
	err:         rgba(0xb9, 0x1c, 0x1c, 0xff),
	errWash:     rgba(0xb9, 0x1c, 0x1c, 0x1f),
	idle:        rgba(0x8a, 0x91, 0x9b, 0xff),
	onStatus:    rgba(0xff, 0xff, 0xff, 0xff),
	hover:       rgba(0x00, 0x00, 0x00, 0x0c),
	pressed:     rgba(0x00, 0x00, 0x00, 0x14),
	selection:   rgba(0x0f, 0x76, 0x6e, 0x26),
	focus:       rgba(0x0f, 0x76, 0x6e, 0x33),
	scrollBar:   rgba(0x00, 0x00, 0x00, 0x33),
	scrollBarBG: rgba(0x00, 0x00, 0x00, 0x00),
	shadow:      rgba(0x00, 0x00, 0x00, 0x33),
	overlay:     rgba(0xff, 0xff, 0xff, 0xff),
	menu:        rgba(0xff, 0xff, 0xff, 0xff),
	warnWash:    rgba(0xf5, 0x9e, 0x0b, 0x18),
	accentWash:  rgba(0x0f, 0x76, 0x6e, 0x1a),
}

func paletteFor(variant fyne.ThemeVariant) *palette {
	if variant == theme.VariantLight {
		return &lightPalette
	}
	return &darkPalette
}

// Theme maps the stock Fyne color names onto the tie palette and resolves the
// tie.* names. mode forces a variant ("light"/"dark"); "" follows the system
// setting.
type Theme struct {
	base fyne.Theme
	mode string
}

// New returns the tie theme. TIE_THEME=light|dark forces a variant; otherwise
// the system light/dark setting is followed.
func New() *Theme {
	return &Theme{base: theme.DefaultTheme(), mode: normMode(os.Getenv("TIE_THEME"))}
}

// Apply installs the tie theme on app. Re-applying replaces the theme rather
// than wrapping it, so repeated calls do not compound.
func Apply(app fyne.App) {
	if app == nil {
		return
	}
	app.Settings().SetTheme(New())
}

func normMode(mode string) string {
	switch mode {
	case "light", "dark":
		return mode
	}
	return ""
}

func (t *Theme) variant(v fyne.ThemeVariant) fyne.ThemeVariant {
	switch t.mode {
	case "light":
		return theme.VariantLight
	case "dark":
		return theme.VariantDark
	}
	return v
}

// Color implements fyne.Theme.
func (t *Theme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	p := paletteFor(t.variant(v))
	switch name {
	case theme.ColorNameBackground:
		return p.background
	case theme.ColorNameForeground:
		return p.foreground
	case theme.ColorNamePrimary:
		return p.accent
	case theme.ColorNameForegroundOnPrimary:
		return p.onAccent
	case theme.ColorNameError:
		return p.err
	case theme.ColorNameForegroundOnError:
		return p.onStatus
	case theme.ColorNameSuccess:
		return p.ok
	case theme.ColorNameForegroundOnSuccess:
		return p.onStatus
	case theme.ColorNameWarning:
		return p.warn
	case theme.ColorNameForegroundOnWarning:
		return p.onStatus
	case theme.ColorNameButton:
		return p.button
	case theme.ColorNameDisabled:
		return p.muted
	case theme.ColorNameDisabledButton:
		return p.disabledBtn
	case theme.ColorNameInputBackground:
		return p.input
	case theme.ColorNameInputBorder:
		return p.inputBorder
	case theme.ColorNamePlaceHolder:
		return p.placeholder
	case theme.ColorNameHover:
		return p.hover
	case theme.ColorNamePressed:
		return p.pressed
	case theme.ColorNameSelection:
		return p.selection
	case theme.ColorNameFocus:
		return p.focus
	case theme.ColorNameSeparator:
		return p.separator
	case theme.ColorNameScrollBar:
		return p.scrollBar
	case theme.ColorNameScrollBarBackground:
		return p.scrollBarBG
	case theme.ColorNameShadow:
		return p.shadow
	case theme.ColorNameOverlayBackground:
		return p.overlay
	case theme.ColorNameMenuBackground:
		return p.menu
	case theme.ColorNameHeaderBackground:
		return p.rail
	case theme.ColorNameInnerWindowBorder:
		return p.border
	case theme.ColorNameHyperlink:
		return p.accent
	case ColorRail:
		return p.rail
	case ColorSidebar:
		return p.sidebar
	case ColorRaised:
		return p.raised
	case ColorCode:
		return p.code
	case ColorBorder:
		return p.border
	case ColorMuted:
		return p.muted
	case ColorOK:
		return p.ok
	case ColorOKWash:
		return p.okWash
	case ColorWarn:
		return p.warn
	case ColorErr:
		return p.err
	case ColorErrWash:
		return p.errWash
	case ColorIdle:
		return p.idle
	case ColorWarnWash:
		return p.warnWash
	case ColorAccentWash:
		return p.accentWash
	}
	return t.base.Color(name, v)
}

// Font implements fyne.Theme.
func (t *Theme) Font(style fyne.TextStyle) fyne.Resource { return t.base.Font(style) }

// Icon implements fyne.Theme.
func (t *Theme) Icon(name fyne.ThemeIconName) fyne.Resource { return t.base.Icon(name) }

// Size implements fyne.Theme. Rounded controls and cards are part of the tie
// look; all other sizes follow the base theme.
func (t *Theme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameInputRadius:
		return 6
	case theme.SizeNameSelectionRadius:
		return 4
	case theme.SizeNameButtonRadius:
		return 6
	case theme.SizeNameCardRadius:
		return 8
	case theme.SizeNameScrollBar:
		return 8
	}
	return t.base.Size(name)
}
