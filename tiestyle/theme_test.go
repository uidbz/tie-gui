package tiestyle

import (
	"image/color"
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

func TestColorTokensResolve(t *testing.T) {
	names := []fyne.ThemeColorName{
		ColorRail, ColorSidebar, ColorRaised, ColorCode, ColorBorder, ColorMuted,
		ColorOK, ColorOKWash, ColorWarn, ColorErr, ColorErrWash, ColorIdle,
		ColorWarnWash, ColorAccentWash,
	}
	th := &Theme{base: theme.DefaultTheme()}
	for _, name := range names {
		d := th.Color(name, theme.VariantDark)
		l := th.Color(name, theme.VariantLight)
		if d == nil || l == nil {
			t.Fatalf("%s: nil color", name)
		}
		if d == l {
			t.Errorf("%s: same color in both variants", name)
		}
	}
}

func TestContrast(t *testing.T) {
	th := &Theme{base: theme.DefaultTheme()}
	for _, v := range []fyne.ThemeVariant{theme.VariantDark, theme.VariantLight} {
		bg := th.Color(theme.ColorNameBackground, v)
		for _, c := range []struct {
			name string
			fg   color.Color
			min  float64
		}{
			{"foreground", th.Color(theme.ColorNameForeground, v), 7},
			{"muted", th.Color(ColorMuted, v), 4.5},
			{"accent", th.Color(theme.ColorNamePrimary, v), 3},
			{"error", th.Color(theme.ColorNameError, v), 3},
		} {
			if got := contrastRatio(c.fg, bg); got < c.min {
				t.Errorf("variant %v: %s contrast %.2f < %.1f", v, c.name, got, c.min)
			}
		}
	}
}

func TestForcedMode(t *testing.T) {
	th := &Theme{base: theme.DefaultTheme(), mode: "light"}
	if got := th.variant(theme.VariantDark); got != theme.VariantLight {
		t.Fatalf("forced light theme variant = %v, want light", got)
	}
	th.mode = "dark"
	if got := th.variant(theme.VariantLight); got != theme.VariantDark {
		t.Fatalf("forced dark theme variant = %v, want dark", got)
	}
	th.mode = ""
	if got := th.variant(theme.VariantLight); got != theme.VariantLight {
		t.Fatalf("system theme variant = %v, want light", got)
	}
}

func TestRadii(t *testing.T) {
	th := &Theme{base: theme.DefaultTheme()}
	for _, c := range []struct {
		name fyne.ThemeSizeName
		want float32
	}{
		{theme.SizeNameInputRadius, 6},
		{theme.SizeNameSelectionRadius, 4},
		{theme.SizeNameButtonRadius, 6},
		{theme.SizeNameCardRadius, 8},
	} {
		if got := th.Size(c.name); got != c.want {
			t.Errorf("Size(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

func contrastRatio(a, b color.Color) float64 {
	la := luminance(a)
	lb := luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	lin := func(v uint32) float64 {
		s := float64(v>>8) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}
