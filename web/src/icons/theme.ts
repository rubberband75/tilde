import { shallowRef } from 'vue'
import type { Entry } from '@/api/types'

// The icon theme is bundled under /icons/<theme>/<context>/<size>/<name>.png
// by scripts/fetch-icons.sh, which also writes /icons/index.json mapping
// each icon name to its context directory.

interface IconIndex {
  theme: string
  sizes: number[]
  icons: Record<string, string>
}

export const iconIndex = shallowRef<IconIndex | null>(null)
let loading: Promise<void> | null = null

export function loadIconIndex(): Promise<void> {
  loading ??= fetch('/icons/index.json')
    .then((r) => (r.ok ? r.json() : null))
    .then((idx: IconIndex | null) => {
      iconIndex.value = idx ?? { theme: 'none', sizes: [], icons: {} }
    })
    .catch(() => {
      iconIndex.value = { theme: 'none', sizes: [], icons: {} }
    })
  return loading
}

export interface ResolvedIcon {
  src: string
  srcset: string
}

/** Finds the first available icon among names, for a display size in CSS px. */
export function resolveIcon(names: string[], size: number): ResolvedIcon | null {
  const idx = iconIndex.value
  if (!idx || idx.sizes.length === 0) return null
  for (const name of names) {
    const ctx = idx.icons[name]
    if (!ctx) continue
    const base = `/icons/${idx.theme}/${ctx}`
    const sizes = [...idx.sizes].sort((a, b) => a - b)
    const s1 = sizes.find((s) => s >= size) ?? sizes[sizes.length - 1]
    const s2 = sizes.find((s) => s >= size * 2)
    const src = `${base}/${s1}/${name}.png`
    let hi = s2 ? `${base}/${s2}/${name}.png` : ''
    if (!s2 && s1 === 48) hi = `${base}/48@2x/${name}.png`
    return { src, srcset: hi ? `${src} 1x, ${hi} 2x` : '' }
  }
  return null
}

const archiveTypes = new Set([
  'application/zip',
  'application/x-tar',
  'application/gzip',
  'application/x-gzip',
  'application/x-bzip2',
  'application/x-xz',
  'application/x-7z-compressed',
  'application/x-rar',
  'application/vnd.rar',
  'application/x-compressed-tar',
  'application/zstd',
])

/** Icon names to try for a MIME type, most specific first. */
export function iconsForMime(mime: string): string[] {
  const [major] = mime.split('/')
  const names = [mime.replace('/', '-')]
  if (archiveTypes.has(mime)) names.push('package-x-generic')
  if (mime.includes('shellscript')) names.push('text-x-script')
  if (mime === 'application/x-zerosize') names.push('text-x-generic')
  if (mime === 'application/x-executable' || mime === 'application/x-sharedlib') names.push('application-x-executable')
  if (mime.includes('opendocument.text') || mime.includes('wordprocessing') || mime === 'application/msword')
    names.push('x-office-document')
  if (mime.includes('spreadsheet') || mime.includes('ms-excel')) names.push('x-office-spreadsheet')
  if (mime.includes('presentation') || mime.includes('ms-powerpoint')) names.push('x-office-presentation')
  names.push(`${major}-x-generic`)
  if (major === 'text') names.push('text-x-generic')
  names.push('unknown', 'text-x-generic')
  return names
}

/** Icon names for a file entry; placeIcons maps special folders to icons. */
export function iconsForEntry(e: Entry, placeIcons?: Map<string, string>): string[] {
  if (e.type === 'dir') {
    const special = placeIcons?.get(e.path)
    return special ? [special, 'folder'] : ['folder']
  }
  return iconsForMime(e.mime)
}

// Simple built-in glyphs, used until the icon theme has been fetched (or
// for names it lacks). Generic shapes, not copies of any theme.
const glyphs: Record<string, string> = {
  folder:
    '<svg viewBox="0 0 48 48"><path d="M5 12a3 3 0 0 1 3-3h11l4 4h17a3 3 0 0 1 3 3v21a3 3 0 0 1-3 3H8a3 3 0 0 1-3-3z" fill="#7aa86a"/><path d="M5 18h38v19a3 3 0 0 1-3 3H8a3 3 0 0 1-3-3z" fill="#8fbf7d"/></svg>',
  file: '<svg viewBox="0 0 48 48"><path d="M12 5h17l9 9v29H12z" fill="#e8e8e8"/><path d="M29 5v9h9" fill="#c9c9c9"/></svg>',
  terminal:
    '<svg viewBox="0 0 48 48"><rect x="5" y="8" width="38" height="32" rx="3" fill="#2d2d33"/><path d="M12 18l6 5-6 5M21 30h10" stroke="#8fd98a" stroke-width="3" fill="none" stroke-linecap="round"/></svg>',
  computer:
    '<svg viewBox="0 0 48 48"><rect x="6" y="8" width="36" height="25" rx="2" fill="#3a3a42"/><rect x="9" y="11" width="30" height="19" fill="#5b8fbf"/><path d="M18 33h12l2 6H16z" fill="#8a8a90"/></svg>',
  trash:
    '<svg viewBox="0 0 48 48"><path d="M12 14h24l-2 28H14z" fill="#9a9aa0"/><rect x="9" y="9" width="30" height="5" rx="1" fill="#b4b4ba"/></svg>',
  drive:
    '<svg viewBox="0 0 48 48"><rect x="6" y="14" width="36" height="20" rx="3" fill="#8a8a90"/><circle cx="35" cy="24" r="2.5" fill="#35a854"/></svg>',
  info: '<svg viewBox="0 0 48 48"><circle cx="24" cy="24" r="18" fill="#5b8fbf"/><rect x="22" y="21" width="4" height="13" fill="#fff"/><circle cx="24" cy="15.5" r="2.5" fill="#fff"/></svg>',
}

export function fallbackGlyph(names: string[]): string {
  const n = names.join(' ')
  if (/terminal/.test(n)) return glyphs.terminal
  if (/trash/.test(n)) return glyphs.trash
  if (/computer/.test(n)) return glyphs.computer
  if (/drive|media/.test(n)) return glyphs.drive
  if (/folder|user-home|user-desktop|file-manager|nemo/.test(n)) return glyphs.folder
  if (/about|monitor|info/.test(n)) return glyphs.info
  return glyphs.file
}
