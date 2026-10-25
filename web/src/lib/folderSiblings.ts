// The other files of a file's folder that an app steps through (the image
// viewer's images, the media player's playlist): listed in Files' name
// order and kept current through the folder's inotify events.
import { computed, onBeforeUnmount, shallowRef, watch, type Ref } from 'vue'
import { dirName, fsApi } from '@/api/fs'
import type { Entry } from '@/api/types'
import { onFsChanged } from './bus'
import { useWatchedDirs } from './fswatch'

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

/**
 * The entries accept takes, in Files' name order ("img2" before "img10").
 * Hidden files count only when current is hidden itself.
 */
export function folderSiblings(entries: readonly Entry[], current: string, accept: (e: Entry) => boolean): Entry[] {
  const showHidden = entries.some((e) => e.path === current && e.hidden)
  return entries
    .filter((e) => accept(e) && (showHidden || !e.hidden || e.path === current))
    .sort((a, b) => collator.compare(a.name, b.name) || (a.name < b.name ? -1 : a.name > b.name ? 1 : 0))
}

/** The neighbour of current in list; wrap goes round from one end to the other. */
export function neighbour(list: readonly Entry[], current: string, dir: 1 | -1, wrap = true): Entry | undefined {
  if (!list.length) return undefined
  const i = list.findIndex((e) => e.path === current)
  if (i < 0) return list[0]
  const j = i + dir
  if (j < 0 || j >= list.length) return wrap ? list[(j + list.length) % list.length] : undefined
  return list[j]
}

/**
 * Lists path's folder whenever the folder changes or path moves to another
 * one. onReload gets the list from before, so the app can follow its file
 * if it was replaced or removed. Call reload once to start.
 */
export function useFolderSiblings(
  path: Ref<string>,
  accept: (e: Entry) => boolean,
  onReload?: (before: Entry[]) => void,
) {
  const items = shallowRef<Entry[]>([])
  const folder = computed(() => (path.value ? dirName(path.value) : ''))
  let abort: AbortController | undefined

  async function reload() {
    const dir = folder.value
    if (!dir) return
    abort?.abort()
    const ac = new AbortController()
    abort = ac
    const acc: Entry[] = []
    try {
      await fsApi.listStream(dir, (batch) => acc.push(...batch), ac.signal)
    } catch {
      return // The app still has its one file.
    }
    if (ac.signal.aborted || folder.value !== dir) return
    const before = items.value
    items.value = folderSiblings(acc, path.value, accept)
    onReload?.(before)
  }

  watch(folder, () => void reload())
  useWatchedDirs(() => [folder.value])
  const off = onFsChanged((dir) => {
    if (dir === folder.value) void reload()
  })
  onBeforeUnmount(() => {
    abort?.abort()
    off()
  })

  return { items, folder, reload }
}
