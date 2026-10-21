// Nemo's permission choices, mapped to Unix permission bits. Each class
// (owner, group, others) gets one choice; files have a separate "allow
// executing" switch that sets x for every class that can read.

export type Who = 'owner' | 'group' | 'others'
export const whos: Who[] = ['owner', 'group', 'others']

export interface AccessChoice {
  value: string
  label: string
  bits: number
}

const fileChoices: AccessChoice[] = [
  { value: 'rw', label: 'Read and write', bits: 6 },
  { value: 'r', label: 'Read-only', bits: 4 },
  { value: 'none', label: 'None', bits: 0 },
]

const dirChoices: AccessChoice[] = [
  { value: 'create', label: 'Create and delete files', bits: 7 },
  { value: 'access', label: 'Access files', bits: 5 },
  { value: 'list', label: 'List files only', bits: 4 },
  { value: 'none', label: 'None', bits: 0 },
]

export function choices(isDir: boolean): AccessChoice[] {
  return isDir ? dirChoices : fileChoices
}

const shift: Record<Who, number> = { owner: 6, group: 3, others: 0 }

function bitsOf(perm: number, who: Who): number {
  return (perm >> shift[who]) & 7
}

/** The current choice for who, or "custom" if the bits match none. */
export function accessOf(perm: number, who: Who, isDir: boolean): string {
  const bits = bitsOf(perm, who)
  const relevant = isDir ? bits : bits & 6
  return choices(isDir).find((c) => c.bits === relevant)?.value ?? 'custom'
}

/** perm with who's access set to choice. */
export function withAccess(perm: number, who: Who, isDir: boolean, value: string): number {
  const choice = choices(isDir).find((c) => c.value === value)
  if (!choice) return perm
  let bits = choice.bits
  // A file keeps its execute bit if it stays readable.
  if (!isDir && isExecutable(perm) && bits & 4) bits |= 1
  return (perm & ~(7 << shift[who])) | (bits << shift[who])
}

export function isExecutable(perm: number): boolean {
  return (perm & 0o100) !== 0
}

/** perm with execute allowed (for every class that can read) or removed. */
export function withExecutable(perm: number, on: boolean): number {
  let out = perm & ~0o111
  if (on) {
    for (const who of whos) if (bitsOf(perm, who) & 4) out |= 1 << shift[who]
    out |= 0o100
  }
  return out
}
