import { api } from './client'
import type { LocalApp } from './types'

/** Local web apps (D76): the user's listening ports, opened from the app port. */
export const appsApi = {
  list: () => api.get<{ apps: LocalApp[] }>('/api/v1/apps').then((r) => r.apps),
  /**
   * A one-time code to post to the app port's /_tilde/enter, and that
   * port's origin. The code is spent by its first use, or after a minute.
   */
  handoff: () => api.post<{ code: string; origin: string }>('/api/auth/apps/handoff', {}),
}

/** Where on the app port a local app's page is: its path kept, or the prefix taken off. */
export function appPath(port: number, keepPrefix: boolean, path = '/'): string {
  const rest = path.startsWith('/') ? path.slice(1) : path
  return `/${keepPrefix ? 'absproxy' : 'proxy'}/${port}/${rest}`
}

/**
 * Opens a local app in a frame or a tab (`target`), by posting a handoff
 * code to the app port in a form: the code never goes in a URL (rule 6).
 * Resolves to the app port's origin.
 */
export async function enterApp(target: string, next: string): Promise<string> {
  const { code, origin } = await appsApi.handoff()
  const form = document.createElement('form')
  form.method = 'POST'
  form.action = `${origin}/_tilde/enter`
  form.target = target
  form.hidden = true
  for (const [name, value] of Object.entries({ code, next })) {
    const input = document.createElement('input')
    input.type = 'hidden'
    input.name = name
    input.value = value
    form.append(input)
  }
  document.body.append(form)
  form.submit()
  form.remove()
  return origin
}
