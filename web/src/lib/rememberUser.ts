// "Remember user name" on the login screen (D42). Only the name is kept, in
// this browser's localStorage: never the password, and nothing that lets a
// browser skip it.

const key = 'tilde:login-user'

/** The remembered user name, or '' if none (or storage is unavailable). */
export function rememberedUser(): string {
  try {
    return localStorage.getItem(key) ?? ''
  } catch {
    return ''
  }
}

/** Keeps name for the next login, or forgets it. */
export function rememberUser(name: string, remember: boolean): void {
  try {
    if (remember && name) localStorage.setItem(key, name)
    else localStorage.removeItem(key)
  } catch {
    // Private windows may refuse storage; the login still works.
  }
}
