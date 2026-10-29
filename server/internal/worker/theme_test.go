package worker

import "testing"

func TestDesktopTheme(t *testing.T) {
	for _, c := range []struct {
		apps, shell string
		want        DesktopTheme
	}{
		{"Mint-Y-Dark-Aqua", "Mint-Y-Dark-Aqua", DesktopTheme{"cinnamon", "dark", "aqua"}},
		{"Mint-Y-Teal", "Mint-Y-Dark-Teal", DesktopTheme{"cinnamon", "mixed", "teal"}},
		{"Mint-Y", "Mint-Y", DesktopTheme{"cinnamon", "light", "green"}},
		{"Mint-Y-Dark", "", DesktopTheme{"cinnamon", "dark", "green"}},
		{"Adwaita-dark", "cinnamon", DesktopTheme{"cinnamon", "dark", "green"}},
	} {
		if got, ok := cinnamonTheme(c.apps, c.shell); !ok || got != c.want {
			t.Errorf("cinnamonTheme(%q, %q) = %+v", c.apps, c.shell, got)
		}
	}
	if _, ok := cinnamonTheme("", ""); ok {
		t.Error("no theme name counted as Cinnamon's")
	}
	for _, c := range []struct {
		scheme, accent string
		want           DesktopTheme
	}{
		{"prefer-dark", "teal", DesktopTheme{"gnome", "dark", "teal"}},
		{"default", "yellow", DesktopTheme{"gnome", "light", "sand"}},
		{"prefer-light", "", DesktopTheme{"gnome", "light", "green"}},
		{"prefer-dark", "slate", DesktopTheme{"gnome", "dark", "grey"}},
	} {
		if got := gnomeTheme(c.scheme, c.accent); got != c.want {
			t.Errorf("gnomeTheme(%q, %q) = %+v", c.scheme, c.accent, got)
		}
	}
}
