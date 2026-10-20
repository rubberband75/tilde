// File actions shared by the Files app and the desktop: clipboard, paste,
// trash, permanent delete. Long operations run as worker jobs (D31); the
// jobs panel shows their progress and conflicts.
import { baseName, dirName, fsApi } from '@/api/fs'
import type { JobOp } from '@/api/types'
import type { AppContext } from '@/apps/types'
import { emitFsChanged } from '@/lib/bus'
import { useJobs } from '@/stores/jobs'

/** What the actions need from their caller; an app's ctx provides it. */
export type FileOpsHost = Pick<AppContext, 'confirm' | 'notify' | 'clipboard'>

export async function startJob(host: FileOpsHost, op: JobOp, sources: string[], dest?: string): Promise<void> {
  if (!sources.length) return
  try {
    await useJobs().start(op, sources, dest)
  } catch (e) {
    host.notify(`Could not start the operation: ${(e as Error).message}`, 'error')
  }
}

/** Pastes the clipboard into dest: copies, or moves what was cut. */
export async function paste(host: FileOpsHost, dest: string): Promise<void> {
  const { op, paths } = host.clipboard
  if (!op || !paths.length) return
  await startJob(host, op === 'cut' ? 'move' : 'copy', [...paths], dest)
  if (op === 'cut') host.clipboard.clear()
}

function describe(paths: string[], noun = 'items'): string {
  return paths.length === 1 ? `“${baseName(paths[0]!)}”` : `the ${paths.length} selected ${noun}`
}

/** Asks, then deletes for good (Shift+Delete, or Delete inside the trash). */
export async function deletePermanently(host: FileOpsHost, paths: string[]): Promise<void> {
  if (!paths.length) return
  const ok = await host.confirm({
    title: `Permanently delete ${describe(paths)}?`,
    message: 'If you delete an item, it will be permanently lost.',
    confirm: 'Delete',
    danger: true,
  })
  if (ok) await startJob(host, 'delete', paths)
}

/**
 * Moves paths to the trash. Items on a drive without a trash folder for the
 * user (per-drive trash comes in phase 4) can be deleted instead, as Nemo
 * offers.
 */
export async function moveToTrash(host: FileOpsHost, paths: string[], trashDir: string): Promise<void> {
  if (!paths.length) return
  let results
  try {
    results = await fsApi.trash(paths)
  } catch (e) {
    host.notify(`Could not move to the trash: ${(e as Error).message}`, 'error')
    return
  }
  const trashed = results.filter((r) => !r.error).map((r) => dirName(r.path))
  if (trashed.length) emitFsChanged(...trashed, trashDir)
  const otherDrive = results.filter((r) => r.code === 'other_drive').map((r) => r.path)
  for (const r of results) {
    if (r.error && r.code !== 'other_drive')
      host.notify(`Could not move ${baseName(r.path)} to the trash: ${r.error}`, 'error')
  }
  if (!otherDrive.length) return
  const ok = await host.confirm({
    title: `${otherDrive.length === 1 ? describe(otherDrive) : `${otherDrive.length} items`} cannot be moved to the trash. Delete permanently?`,
    message: 'They are on a drive without a trash folder. If you delete them, they will be permanently lost.',
    confirm: 'Delete',
    danger: true,
  })
  if (ok) await startJob(host, 'delete', otherDrive)
}
