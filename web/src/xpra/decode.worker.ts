// Decodes what a draw packet carries, off the page's thread (D80): raw
// pixels, or an image with the browser's own decoders, so the CSP needs no
// WebAssembly. Same-origin, by Vite's own bundling: script-src stays 'self'.

import { imageType, isRaw, rgbaOf, type Draw } from './draw'

export interface DecodeRequest {
  id: number
  draw: Draw
}

export type DecodeResponse = { id: number; bitmap: ImageBitmap } | { id: number; error: string }

const scope = self as unknown as {
  onmessage: ((e: MessageEvent<DecodeRequest>) => void) | null
  postMessage(message: DecodeResponse, transfer?: Transferable[]): void
}

async function decode(d: Draw): Promise<ImageBitmap> {
  if (isRaw(d.coding)) return createImageBitmap(new ImageData(rgbaOf(d), d.w, d.h))
  const type = imageType(d.coding)
  if (!type) throw new Error(`unsupported encoding ${d.coding}`)
  return createImageBitmap(new Blob([d.data as Uint8Array<ArrayBuffer>], { type }))
}

scope.onmessage = (e) => {
  const { id, draw } = e.data
  decode(draw).then(
    (bitmap) => scope.postMessage({ id, bitmap }, [bitmap]),
    (err: unknown) => scope.postMessage({ id, error: err instanceof Error ? err.message : String(err) }),
  )
}
