import { api } from './client'
import type { AuthPrompt, PolkitState } from './types'

export const polkitApi = {
  list: () => api.get<PolkitState>('/api/v1/polkit'),
  /**
   * Answers a request: with no question waiting, `answer` is the password of
   * `identity` (default: the first). Resolves to PAM's next question, or
   * undefined once polkit has the authorization; a wrong password rejects
   * with `auth_failed` and the request stays open.
   */
  respond: (id: string, answer: string, identity?: string) =>
    api
      .post<{ prompt: AuthPrompt } | undefined>('/api/v1/polkit/respond', { id, identity, answer })
      .then((r) => r?.prompt),
  cancel: (id: string) => api.post<void>('/api/v1/polkit/cancel', { id }),
}
