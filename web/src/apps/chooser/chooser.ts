// What the file chooser decides without the DOM: which entries a filter
// lists, where a typed path points, and what the accepting button does.
import { dirName, joinPath } from '@/api/fs'
import type { Entry } from '@/api/types'
import type { FileFilter } from '@/stores/fileChooser'

/** mime is one of patterns: exact, or "type/*". */
export function mimeMatches(mime: string, patterns: readonly string[]): boolean {
  const major = mime.split('/')[0]
  return patterns.some((p) => p === mime || p === `${major}/*` || p === '*')
}

/** A shell-style pattern ("*.tar.gz", "photo-??.jpg") as a regular expression. */
function globRegExp(pattern: string): RegExp {
  let src = ''
  for (const c of pattern) {
    if (c === '*') src += '.*'
    else if (c === '?') src += '.'
    else src += c.replace(/[\\^$.|+()[\]{}]/g, '\\$&')
  }
  return new RegExp(`^${src}$`, 'i')
}

/** The filter lists entry: folders always, files by type or name. */
export function filterAccepts(filter: FileFilter | undefined, entry: Pick<Entry, 'name' | 'mime' | 'type'>): boolean {
  if (!filter || entry.type === 'dir') return true
  if (!filter.mimeTypes?.length && !filter.patterns?.length) return true
  if (filter.mimeTypes && mimeMatches(entry.mime, filter.mimeTypes)) return true
  return !!filter.patterns?.some((p) => globRegExp(p).test(entry.name))
}

/** Removes ".", ".." and doubled slashes from an absolute path. */
export function normalizePath(path: string): string {
  const parts: string[] = []
  for (const part of path.split('/')) {
    if (!part || part === '.') continue
    if (part === '..') parts.pop()
    else parts.push(part)
  }
  return `/${parts.join('/')}`
}

/**
 * Where a typed location points: absolute, under the home folder ("~/"),
 * or relative to the folder shown. A trailing slash is kept as a sign that
 * the user means a folder.
 */
export function resolveTyped(input: string, folder: string, home: string): string {
  const text = input.trim()
  let path: string
  if (text === '~' || text.startsWith('~/')) path = home + text.slice(1)
  else if (text.startsWith('/')) path = text
  else path = joinPath(folder, text)
  return normalizePath(path)
}

/** A typed name names a file in another folder ("notes/today.txt"). */
export function splitTyped(input: string, folder: string, home: string): { dir: string; name: string } {
  const text = input.trim()
  if (!text) return { dir: folder, name: '' }
  const path = resolveTyped(text, folder, home)
  if (text.endsWith('/') || text === '~') return { dir: path, name: '' }
  return { dir: dirName(path), name: path.slice(path.lastIndexOf('/') + 1) }
}

/** A file name the worker would refuse, with the reason; '' if fine. */
export function badName(name: string): string {
  if (!name) return 'Type a name for the file.'
  if (name === '.' || name === '..') return `“${name}” cannot be used as a file name.`
  if (name.length > 255) return 'The name is too long.'
  return ''
}
