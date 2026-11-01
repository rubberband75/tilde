// The page's side of the decode worker (decode.worker.ts): a promise of a
// bitmap per draw. Started when first needed, ended with the connection.

import type { DecodeRequest, DecodeResponse } from './decode.worker'
import type { Draw } from './draw'

/** Longer than any decode takes: past it, the worker has hung. */
const JOB_TIMEOUT = 30_000

export class DrawDecoder {
  private worker: Worker | null = null
  private seq = 0
  private waiting = new Map<
    number,
    { resolve: (b: ImageBitmap) => void; reject: (e: Error) => void; timer: ReturnType<typeof setTimeout> }
  >()

  decode(draw: Draw): Promise<ImageBitmap> {
    this.worker ??= this.start()
    const id = ++this.seq
    // Only its own bytes go across: the rest of the packet stays here.
    const data = draw.data.slice()
    const req: DecodeRequest = { id, draw: { ...draw, data } }
    return new Promise((resolve, reject) => {
      // Paints wait their turn, so one that never comes stops its window.
      const timer = setTimeout(() => this.close('a decode took too long'), JOB_TIMEOUT)
      this.waiting.set(id, { resolve, reject, timer })
      this.worker!.postMessage(req, [data.buffer])
    })
  }

  /** Ends the worker and what it was doing; the next decode starts another. */
  close(why = 'the connection ended') {
    this.worker?.terminate()
    this.worker = null
    this.fail(why)
  }

  private start(): Worker {
    const w = new Worker(new URL('./decode.worker.ts', import.meta.url), { type: 'module' })
    w.onmessage = (e: MessageEvent<DecodeResponse>) => {
      const r = e.data
      const job = this.waiting.get(r.id)
      if (!job) {
        if ('bitmap' in r) r.bitmap.close()
        return
      }
      this.waiting.delete(r.id)
      clearTimeout(job.timer)
      if ('bitmap' in r) job.resolve(r.bitmap)
      else job.reject(new Error(r.error))
    }
    w.onerror = () => this.close('the decoder failed')
    return w
  }

  private fail(why: string) {
    for (const job of this.waiting.values()) {
      clearTimeout(job.timer)
      job.reject(new Error(why))
    }
    this.waiting.clear()
  }
}
