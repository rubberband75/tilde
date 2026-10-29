package worker

import (
	"context"
	"slices"
	"strings"
)

// The look (D77): Mint-Y's modes (dark; light; mixed, light windows with
// a dark panel and menus) and accent colours. Tilde's own choice is in
// the settings; "desktop" follows what the desktop at the machine says,
// read and never written (D67): Cinnamon's application and desktop
// themes, else GNOME's colour scheme and accent.

// Accents are Mint-Y's colours.
var accents = []string{"green", "aqua", "blue", "grey", "orange", "pink", "purple", "red", "sand", "teal"}

var themeModes = []string{"dark", "light", "mixed"}

// DesktopTheme is what the desktop says, for "follow the desktop".
type DesktopTheme struct {
	Source string `json:"source"` // cinnamon, gnome or default
	Mode   string `json:"mode"`
	Accent string `json:"accent"`
}

// defaultDesktopTheme is Tilde's look without a desktop: Mint-Y Dark.
func defaultDesktopTheme() DesktopTheme {
	return DesktopTheme{Source: "default", Mode: "dark", Accent: "green"}
}

func readDesktopTheme(ctx context.Context) DesktopTheme {
	if iface, ok := gsettingsList(ctx, "org.cinnamon.desktop.interface"); ok {
		shell, _ := gsettingsList(ctx, "org.cinnamon.theme")
		if t, ok := cinnamonTheme(gvariantString(iface["gtk-theme"]), gvariantString(shell["name"])); ok {
			return t
		}
	}
	if iface, ok := gsettingsList(ctx, "org.gnome.desktop.interface"); ok {
		return gnomeTheme(gvariantString(iface["color-scheme"]), gvariantString(iface["accent-color"]))
	}
	return defaultDesktopTheme()
}

// cinnamonTheme reads Mint's theme names: "Mint-Y-Dark-Aqua" for the
// applications and the desktop (panel, menus). Light applications with a
// dark desktop are Mint's "mixed". Themes other than Mint's say only
// whether they are dark, by name.
func cinnamonTheme(apps, shell string) (DesktopTheme, bool) {
	if apps == "" {
		return DesktopTheme{}, false
	}
	appsDark, accent := parseThemeName(apps)
	shellDark, _ := parseThemeName(shell)
	if shell == "" {
		shellDark = appsDark
	}
	mode := "light"
	switch {
	case appsDark:
		mode = "dark"
	case shellDark:
		mode = "mixed"
	}
	return DesktopTheme{Source: "cinnamon", Mode: mode, Accent: accent}, true
}

// parseThemeName: whether a GTK or Cinnamon theme is dark, and its Mint-Y
// accent (green, Mint-Y's own, for other themes).
func parseThemeName(name string) (dark bool, accent string) {
	lower := strings.ToLower(name)
	dark = strings.Contains(lower, "dark")
	accent = "green"
	if rest, ok := strings.CutPrefix(lower, "mint-y"); ok {
		for _, part := range strings.Split(rest, "-") {
			if slices.Contains(accents, part) {
				accent = part
			}
		}
	}
	return dark, accent
}

// gnomeTheme maps GNOME's colour scheme and accent (GNOME 47) to Mint-Y's:
// GNOME has no mixed mode, and its yellow and slate are Mint's sand and
// grey.
func gnomeTheme(scheme, accent string) DesktopTheme {
	t := DesktopTheme{Source: "gnome", Mode: "light", Accent: "green"}
	if scheme == "prefer-dark" {
		t.Mode = "dark"
	}
	switch accent {
	case "yellow":
		t.Accent = "sand"
	case "slate":
		t.Accent = "grey"
	default:
		if slices.Contains(accents, accent) {
			t.Accent = accent
		}
	}
	return t
}
