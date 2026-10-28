// Words for resource figures (D61), shared by the applet and System Monitor.
import { formatSize } from './format'

export function percent(v: number): string {
  return `${v < 10 ? v.toFixed(1) : Math.round(v)}%`
}

export function rate(bytesPerSecond: number): string {
  return `${formatSize(Math.round(bytesPerSecond))}/s`
}

/** Used memory as a share of the total. */
export function share(used: number, total: number): number {
  return total > 0 ? (used / total) * 100 : 0
}
