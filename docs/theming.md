# Theming

A desktop style has two parts:

1. **Design tokens**: CSS custom properties in
   `web/src/styles/themes/<theme>.css`, scoped by `[data-theme='<theme>']` on
   `<html>`.
2. **Layout**: `web/src/theme/desktopStyle.ts` — panel position and window
   button placement (later: dock, top bar, menu type).

Components only read tokens (`var(--panel-bg)`), never raw colors. A new
theme is a new token file plus, if the layout differs, a new `DesktopStyle`.

## Where the Mint-Y Dark values come from

From [linuxmint/mint-themes](https://github.com/linuxmint/mint-themes),
`src/Mint-Y`:

| Tokens | Source |
| --- | --- |
| Panel, window list, applets, menu, entries, tooltip | `cinnamon/sass/_colors.scss`, compiled `cinnamon-dark.css` (`.panel-bottom`, `.window-list-item-box`, `.menu`, `.menu-favorites-box`, `StEntry`, `#Tooltip`) |
| App windows (headerbar, views, sidebar, buttons, entries, selection, scrollbars, terminal) | `gtk-3.0/sass/_colors.scss`, compiled `gtk-dark.css` (`button`, `entry`, `.view`, `.nemo-window .sidebar`, `selection`, `vte-terminal`) |

Key values: accent `#35a854`; panel `rgba(29, 29, 33, 0.99)`, 40 px; panel
hover/active `#41584d` with an accent border; menu `#222226` with `#303036`
boxes and 6 px radius; window background `#2e2e33`; views `#333339`; Nemo
sidebar `#2c2c31`; borders `#202023`; buttons `#38383e`; text `#dadada`.

Window title bars approximate Mint-Y Dark's Muffin/Metacity theme (`#222226`
focused). Compare against a real Mint desktop and adjust the `--wm-*` tokens.

## Adding a theme

1. Copy `mint-y-dark.css` to `web/src/styles/themes/<id>.css`, change the
   selector to `[data-theme='<id>']` and the values.
2. Import it in `web/src/main.ts`.
3. Add a `DesktopStyle` in `desktopStyle.ts` (`theme: '<id>'`).
4. If the layout differs (top bar, dock, buttons on the left), make the shell
   components read the style instead of assuming the bottom panel.

Saving the chosen style per user (`~/.config/tilde/settings.json`, written by
the worker) is planned for the Settings app.

## Fonts

Mint uses the Ubuntu font at 10 pt, Ubuntu Medium for title bars and DejaVu
Sans Mono for monospace. Tilde bundles the Ubuntu font as `Tilde UI` (D56,
`styles/fonts.css`, built by `make fonts`): an installed Ubuntu is used
first, then the bundled file, and while it loads the fallbacks (Arial or
its metric twins Liberation Sans and Arimo, then DejaVu Sans) are scaled to
take Ubuntu's space, so nothing moves when it arrives. Monospace is not
bundled: DejaVu Sans Mono, then Ubuntu Mono, Menlo or Consolas.
