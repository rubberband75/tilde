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

export const mintY: DesktopStyle = {
  id: 'mint-y',
  name: 'Mint-Y',
  theme: 'mint-y',
  panelPosition: 'bottom',
  buttons: { left: [], right: ['minimize', 'maximize', 'close'] },
}

/** One layout; Mint-Y's modes and colours are tokens (D77, lib/theme.ts). */
export const currentStyle: DesktopStyle = mintY
