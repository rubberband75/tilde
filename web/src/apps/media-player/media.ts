// What the media player decides without the DOM: which files it plays and
// how a time reads.
import type { Entry } from '@/api/types'

/** Audio types that are lists of other files, not sound; the text editor opens them. */
export const playlistTypes = ['audio/x-mpegurl', 'audio/mpegurl', 'audio/x-scpls', 'application/vnd.apple.mpegurl']

/**
 * A sound or video file. Whether the browser can decode it shows only
 * when it tries; the player then says so.
 */
export function isMedia(entry: Pick<Entry, 'type' | 'mime'>): boolean {
  if (entry.type !== 'file' || playlistTypes.includes(entry.mime)) return false
  return entry.mime.startsWith('audio/') || entry.mime.startsWith('video/')
}

/** Seconds as the player shows them: "4:05", or "1:02:03" past an hour. */
export function formatTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '0:00'
  const s = Math.floor(seconds)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = String(s % 60).padStart(2, '0')
  return h ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`
}
