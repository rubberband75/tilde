import { api } from './client'
import type { SoundState } from './types'

/** A change: to an output by name, or an app's stream by id. */
export type SoundChange =
  | { output: string; volume?: number; muted?: boolean; default?: boolean }
  | { app: number; volume?: number; muted?: boolean }

/** The host's sound, through its sound server (D79). */
export const soundApi = {
  state: () => api.get<SoundState>('/api/v1/sound'),
  /** Resolves to the state after the change. */
  set: (change: SoundChange) => api.post<SoundState>('/api/v1/sound', change),
}
