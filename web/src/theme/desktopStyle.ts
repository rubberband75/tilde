// A "desktop style" is a color theme plus layout (decision: theme tokens +
// layout config). Ubuntu or Pop!_OS styles differ structurally (top bar,
// dock, buttons on the left), so they need more than a color file.

export type WindowButton = 'minimize' | 'maximize' | 'close'

export interface DesktopStyle {
  id: string
  name: string
  /** Value for <html data-theme>, selecting the token file. */
  theme: string
  panelPosition: 'bottom' | 'top'
  /** Window buttons on each side of the title bar. */
  buttons: { left: WindowButton[]; right: WindowButton[] }
}

export const mintYDark: DesktopStyle = {
  id: 'mint-y-dark',
  name: 'Mint-Y Dark',
  theme: 'mint-y-dark',
  panelPosition: 'bottom',
  buttons: { left: [], right: ['minimize', 'maximize', 'close'] },
}

/** v1 ships one style; saving a choice per user comes later. */
export const currentStyle: DesktopStyle = mintYDark
