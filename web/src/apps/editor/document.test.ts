import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { draftDelayMs, EditorDocument } from './document'

// A worker with files and drafts, speaking /api/v1/text and /api/v1/drafts.
let files: Map<string, { text: string; version: number }>
let drafts: Map<string, { key: string; text: string; base: string; updated: string }>

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

function fakeWorker(input: string, init?: RequestInit): Response {
  const url = new URL(input, 'http://tilde')
  const method = init?.method ?? 'GET'
  const q = url.searchParams
  if (url.pathname === '/api/v1/text') {
    const path = q.get('path')!
    const f = files.get(path)
    if (method === 'GET') {
      if (!f) return json(404, { error: 'no such file', code: 'not_found' })
      return json(200, { text: f.text, version: `v${f.version}`, writable: true, entry: { path } })
    }
    const base = q.get('base') ?? ''
    if (q.get('force') !== '1') {
      if (!f && base) return json(409, { error: 'gone', code: 'changed' })
      if (f && !base) return json(409, { error: 'exists', code: 'exists' })
      if (f && base !== `v${f.version}`) return json(409, { error: 'changed', code: 'changed' })
    }
    const next = { text: init!.body as string, version: (f?.version ?? 0) + 1 }
    files.set(path, next)
    return json(200, { version: `v${next.version}`, entry: { path } })
  }
  const key = q.get('key')!
  if (method === 'GET') {
    const d = drafts.get(key)
    return d ? json(200, d) : json(404, { error: 'no draft', code: 'not_found' })
  }
  if (method === 'PUT') {
    const body = JSON.parse(init!.body as string) as { text: string; base: string }
    drafts.set(key, { key, ...body, updated: new Date().toISOString() })
  } else {
    drafts.delete(key)
  }
  return new Response(null, { status: 204 })
}

beforeEach(() => {
  files = new Map([['/home/me/notes.txt', { text: 'hello\n', version: 1 }]])
  drafts = new Map()
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string, init?: RequestInit) => Promise.resolve(fakeWorker(input, init))),
  )
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

async function typed(doc: EditorDocument, text: string) {
  vi.useFakeTimers()
  doc.edited(text)
  await vi.advanceTimersByTimeAsync(draftDelayMs)
  vi.useRealTimers()
}

describe('EditorDocument', () => {
  it('keeps unsaved changes in a draft that a new window finds', async () => {
    const doc = new EditorDocument('/home/me/notes.txt', 'id1')
    expect(await doc.open()).toBe('hello\n')
    expect(doc.state.dirty).toBe(false)
    await typed(doc, 'hello world\n')
    expect(drafts.get('file:/home/me/notes.txt')).toMatchObject({ text: 'hello world\n', base: 'v1' })

    // The page reloads (or locks): the window comes back with the draft.
    const again = new EditorDocument('/home/me/notes.txt', 'id2')
    expect(await again.open()).toBe('hello world\n')
    expect(again.state.dirty).toBe(true)
    expect(again.state.changedOnDisk).toBe(false)

    // Typing back to the saved text drops the draft.
    await typed(again, 'hello\n')
    expect(drafts.size).toBe(0)
  })

  it('says when the file changed on disk after the draft began', async () => {
    drafts.set('file:/home/me/notes.txt', { key: 'file:/home/me/notes.txt', text: 'mine\n', base: 'v1', updated: '' })
    files.set('/home/me/notes.txt', { text: 'theirs\n', version: 2 })
    const doc = new EditorDocument('/home/me/notes.txt', 'id')
    expect(await doc.open()).toBe('mine\n')
    expect(doc.state.changedOnDisk).toBe(true)
    expect(await doc.reload()).toBe('theirs\n')
    expect(doc.state.dirty).toBe(false)
    expect(drafts.size).toBe(0)
  })

  it('saves against the version it opened, and asks before overwriting', async () => {
    const doc = new EditorDocument('/home/me/notes.txt', 'id')
    await doc.open()
    await typed(doc, 'v2\n')
    expect(await doc.save('v2\n')).toBe('saved')
    expect(files.get('/home/me/notes.txt')!.text).toBe('v2\n')
    expect(doc.state.dirty).toBe(false)
    expect(drafts.size).toBe(0)

    files.set('/home/me/notes.txt', { text: 'someone else\n', version: 9 })
    doc.edited('mine\n')
    expect(await doc.save('mine\n')).toBe('changed')
    expect(await doc.save('mine\n', true)).toBe('saved')
    expect(files.get('/home/me/notes.txt')!.text).toBe('mine\n')
  })

  it('saves a new document as a file, and its draft moves with it', async () => {
    const doc = new EditorDocument('', 'abc')
    expect(await doc.open()).toBe('')
    expect(doc.name).toBe('Untitled Document')
    await typed(doc, 'draft\n')
    expect(drafts.has('new:abc')).toBe(true)
    expect(await doc.save('draft\n')).toBe('needs-path')
    expect(await doc.saveAs('/home/me/notes.txt', 'draft\n')).toBe('exists')
    expect(await doc.saveAs('/home/me/new.txt', 'draft\n')).toBe('saved')
    expect(doc.state.path).toBe('/home/me/new.txt')
    expect(doc.name).toBe('new.txt')
    expect(drafts.size).toBe(0)
  })

  it('follows changes on disk while there is nothing unsaved', async () => {
    const doc = new EditorDocument('/home/me/notes.txt', 'id')
    await doc.open()
    expect(await doc.checkDisk()).toBeNull()
    files.set('/home/me/notes.txt', { text: 'new\n', version: 2 })
    expect(await doc.checkDisk()).toBe('new\n')
    doc.edited('edited\n')
    files.set('/home/me/notes.txt', { text: 'newer\n', version: 3 })
    expect(await doc.checkDisk()).toBeNull()
    expect(doc.state.changedOnDisk).toBe(true)
    files.delete('/home/me/notes.txt')
    await doc.checkDisk()
    expect(doc.state.missing).toBe(true)
  })

  it('discards the draft when closed without saving', async () => {
    const doc = new EditorDocument('/home/me/notes.txt', 'id')
    await doc.open()
    await typed(doc, 'unwanted\n')
    await doc.discard()
    expect(doc.state.dirty).toBe(false)
    expect(drafts.size).toBe(0)
  })

  it('opens a draft of a file that is gone, and refuses a file that never was', async () => {
    await expect(new EditorDocument('/home/me/missing.txt', 'id').open()).rejects.toMatchObject({ code: 'not_found' })
    drafts.set('file:/home/me/gone.txt', { key: 'file:/home/me/gone.txt', text: 'kept\n', base: 'v4', updated: '' })
    const doc = new EditorDocument('/home/me/gone.txt', 'id')
    expect(await doc.open()).toBe('kept\n')
    expect(doc.state.missing).toBe(true)
    expect(await doc.save('kept\n')).toBe('saved')
  })
})
