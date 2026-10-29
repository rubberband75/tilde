# Theming

A desktop style has two parts:

1. **Design tokens**: CSS custom properties in
   `web/src/styles/themes/mint-y.css`.
2. **Layout**: `web/src/theme/desktopStyle.ts` — panel position and window
   button placement (later: dock, top bar, menu type).

Components only read tokens (`var(--panel-bg)`), never raw colors.

## Modes and colours (D77)

Mint-Y comes in three modes and ten colours, as Mint's Themes settings
offer them. `<html>` carries three attributes, set by `lib/theme.ts` from
System Settings (or what the desktop at the machine says):

| Attribute | Values | Selects |
| --- | --- | --- |
| `data-shell` | `dark`, `light` | the panel, menus, login and lock screens: Mint's Cinnamon theme |
| `data-apps` | `dark`, `light` | windows and their contents: Mint's GTK theme |
| `data-accent` | `green`, `aqua`, `blue`, `grey`, `orange`, `pink`, `purple`, `red`, `sand`, `teal` | `--accent` and the tooltip |

Dark is a dark shell and dark windows; light is both light; Mint's
"mixed" is a dark shell with light windows. Tokens that follow the
accent (selection, the window list's underline, switches, the close
button, tiling previews, tooltips) are written with `var(--accent)` or
`color-mix()` once, in the shared block. A new token goes in every block
it belongs to: the shared one, or both shell blocks, or both app blocks.

The browser remembers the last look in `localStorage` (`tilde:look`), so
the login screen shows it before anyone logs in.

## Where the values come from

From [linuxmint/mint-themes](https://github.com/linuxmint/mint-themes),
`src/Mint-Y`, as installed in `/usr/share/themes` (mint-themes 2.3.8):

| Tokens | Source |
| --- | --- |
| Shell, dark and light: panel, window list, applets, menu, entries | `Mint-Y-Dark/cinnamon/cinnamon.css` and `Mint-Y/cinnamon/cinnamon.css` (`.panel-bottom`, `.window-list-item-box`, `.applet-box`, `.menu`, `.menu-favorites-box`, `#menu-search-entry`) |
| Windows, dark and light: headerbar, views, sidebar, buttons, entries, selection, scrollbars, popups, terminal | `Mint-Y-Dark/gtk-3.0/gtk.css` and `Mint-Y/gtk-3.0/gtk.css` (`button`, `entry`, `.view`, `.nemo-window .sidebar`, `menu`, `vte-terminal`); window borders from the `wm_*` colours `metacity-1` reads |
| Accents | each `Mint-Y-<Colour>`'s `theme_selected_bg_color`, and `#Tooltip` in its Cinnamon theme |

Light Mint-Y has a light panel (`rgba(243,243,244,0.99)`, text `#303030`),
menus `#ebebed` with `#d3d3d7` boxes, windows `#f8f8f9`, white views, a
`#f6f6f7` Nemo sidebar, `#c8c8ce` borders; its terminal stays dark. In
both modes the panel's hover and active items are a neutral highlight
with an accent underline. The light editor's syntax colours are Tilde's
(xed's default scheme has none for Mint-Y); the lock screen's clock is
white in every mode, as cinnamon-screensaver's.

The dark tokens predate this and come from an older mint-themes; a few
differ from 2.3.8's (views `#2e2e33`, buttons `#333338`, popup hover
`rgba(255,255,255,.1)`). They are kept as they are until compared on a
real Mint desktop.

Folder icons stay Mint-Y's green in every colour: Tilde bundles the one
icon theme.

## Fonts

Mint uses the Ubuntu font at 10 pt, Ubuntu Medium for title bars and DejaVu
Sans Mono for monospace. Tilde bundles the Ubuntu font as `Tilde UI` (D56,
`styles/fonts.css`, built by `make fonts`): an installed Ubuntu is used
first, then the bundled file, and while it loads the fallbacks (Arial or
its metric twins Liberation Sans and Arimo, then DejaVu Sans) are scaled to
take Ubuntu's space, so nothing moves when it arrives. Monospace is not
bundled: DejaVu Sans Mono, then Ubuntu Mono, Menlo or Consolas.
