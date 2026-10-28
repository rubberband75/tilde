import { api } from './client'
import type { ProcessList, ProcessSignal } from './types'

/** Processes from the worker's /proc (D59), and signals sent as the user. */
export const processesApi = {
  list: () => api.get<ProcessList>('/api/v1/processes'),
  /** Rejects with `gone` if the process ended (or its PID is another's now), `permission_denied` for others'. */
  signal: (pid: number, start: number, signal: ProcessSignal) =>
    api.post<void>('/api/v1/processes/signal', { pid, start, signal }),
}
