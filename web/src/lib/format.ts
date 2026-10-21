/** Formats bytes the way GLib's g_format_size does (SI units: kB, MB, GB). */
export function formatSize(bytes: number): string {
  if (bytes < 1000) return bytes === 1 ? '1 byte' : `${bytes} bytes`
  const units = ['kB', 'MB', 'GB', 'TB', 'PB']
  let v = bytes / 1000
  let i = 0
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

const dateFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })

export function formatDate(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : dateFmt.format(d)
}

/** A short human label for a MIME type, for the list view's Type column. */
export function mimeLabel(mime: string): string {
  if (mime === 'inode/directory') return 'Folder'
  if (mime === 'application/octet-stream') return 'Binary'
  if (mime === 'application/x-zerosize') return 'Empty document'
  if (mime === 'application/x-executable') return 'Program'
  const [major, minor = ''] = mime.split('/')
  const sub = minor.replace(/^x-/, '').replace(/[-+.]/g, ' ')
  switch (major) {
    case 'text':
      return minor === 'plain' ? 'Plain text' : `${sub} text`
    case 'image':
      return `${sub.toUpperCase()} image`
    case 'audio':
      return `${sub.toUpperCase()} audio`
    case 'video':
      return `${sub.toUpperCase()} video`
  }
  return sub || mime
}
