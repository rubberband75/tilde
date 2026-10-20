import { api } from './client'
import type { Job, JobOp, Resolution } from './types'

export const jobsApi = {
  start: (op: JobOp, sources: string[], dest?: string) => api.post<Job>('/api/v1/jobs', { op, sources, dest }),
  list: () => api.get<{ jobs: Job[] }>('/api/v1/jobs').then((r) => r.jobs),
  cancel: (id: string) => api.post<void>(`/api/v1/jobs/${id}/cancel`),
  resolve: (id: string, r: Resolution) => api.post<void>(`/api/v1/jobs/${id}/resolve`, r),
  dismiss: (id: string) => api.del<void>(`/api/v1/jobs/${id}`),
}
