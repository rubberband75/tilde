<script setup lang="ts">
// File properties, like Nemo's: a Basic tab (type, size or folder contents
// counted live, location, link target, free space, dates) and, for one item,
// a Permissions tab. Changes apply at once; the kernel decides whether they
// are allowed (only the owner may change permissions).
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { baseName, dirName, fsApi, usageStream } from '@/api/fs'
import type { Properties, Usage } from '@/api/types'
import type { AppContext } from '@/apps/types'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { iconsForEntry } from '@/icons/theme'
import { formatDate, formatSize, mimeLabel } from '@/lib/format'
import { accessOf, choices, isExecutable, whos, withAccess, withExecutable, type Who } from './perms'

const props = defineProps<{ ctx: AppContext }>()

const paths = computed(() => {
  const v = props.ctx.args.paths
  return Array.isArray(v) ? v.filter((p): p is string => typeof p === 'string') : []
})
const single = computed(() => paths.value.length === 1)

const info = shallowRef<Properties[]>([])
const usage = ref<Usage | null>(null)
const error = ref('')
const tab = ref<'basic' | 'permissions'>('basic')
const busy = ref(false)
const usageAbort = new AbortController()

const first = computed(() => info.value[0])
const isDir = computed(() => first.value?.entry.type === 'dir')
const anyDir = computed(() => info.value.some((p) => p.entry.type === 'dir'))

const title = computed(() =>
  single.value ? (first.value?.entry.name ?? baseName(paths.value[0] ?? '')) : `${paths.value.length} items`,
)

const sizeText = computed(() => {
  if (!anyDir.value) {
    const total = info.value.reduce((n, p) => n + p.entry.size, 0)
    return `${formatSize(total)} (${total.toLocaleString()} bytes)`
  }
  const u = usage.value
  if (!u) return 'Counting…'
  const items = `${u.items.toLocaleString()} ${u.items === 1 ? 'item' : 'items'}, totalling ${formatSize(u.size)}`
  return `${items}${u.done ? '' : '…'}${u.unreadable ? ' (some contents unreadable)' : ''}`
})

const typeText = computed(() => {
  const p = first.value
  if (!p) return ''
  if (!single.value) return info.value.every((x) => x.entry.mime === p.entry.mime) ? mimeLabel(p.entry.mime) : 'Mixed'
  return `${mimeLabel(p.entry.mime)} (${p.entry.mime})`
})

const location = computed(() => {
  const dirs = new Set(paths.value.map(dirName))
  return dirs.size === 1 ? [...dirs][0]! : 'Several folders'
})

const perm = computed(() => first.value?.entry.perm ?? 0)
const canEdit = computed(() => single.value && !!first.value?.mine && !first.value.entry.symlink)

async function load() {
  try {
    info.value = await Promise.all(paths.value.map((p) => fsApi.properties(p)))
  } catch (e) {
    error.value = (e as Error).message
    return
  }
  if (first.value) {
    props.ctx.setTitle(`${title.value} Properties`)
    props.ctx.setIcon(iconsForEntry(first.value.entry)[0] ?? 'folder')
  }
  if (anyDir.value) {
    usageStream(paths.value, (u) => (usage.value = u), usageAbort.signal).catch(() => {})
  }
}

async function setPerm(next: number) {
  const p = first.value
  if (!p || next === p.entry.perm || busy.value) return
  busy.value = true
  try {
    const entry = await fsApi.chmod(p.entry.path, next)
    info.value = [{ ...p, entry }]
  } catch (e) {
    props.ctx.notify(`Could not change permissions: ${(e as Error).message}`, 'error')
  } finally {
    busy.value = false
  }
}

function onAccess(who: Who, e: Event) {
  void setPerm(withAccess(perm.value, who, isDir.value, (e.target as HTMLSelectElement).value))
}

function onExec(e: Event) {
  void setPerm(withExecutable(perm.value, (e.target as HTMLInputElement).checked))
}

const whoLabel: Record<Who, string> = { owner: 'Owner', group: 'Group', others: 'Others' }

onMounted(load)
onBeforeUnmount(() => usageAbort.abort())
</script>

<template>
  <div class="properties">
    <header>
      <ThemeIcon :name="first ? iconsForEntry(first.entry) : ['folder']" :size="48" />
      <h1 class="name">{{ title }}</h1>
    </header>
    <nav v-if="single && !error" class="tabs" role="tablist">
      <button role="tab" :aria-selected="tab === 'basic'" :class="{ active: tab === 'basic' }" @click="tab = 'basic'">
        Basic
      </button>
      <button
        role="tab"
        :aria-selected="tab === 'permissions'"
        :class="{ active: tab === 'permissions' }"
        @click="tab = 'permissions'"
      >
        Permissions
      </button>
    </nav>

    <p v-if="error" class="error">{{ error }}</p>
    <dl v-else-if="first && tab === 'basic'" class="grid">
      <dt>Type</dt>
      <dd>{{ typeText }}</dd>
      <dt>{{ anyDir ? 'Contents' : 'Size' }}</dt>
      <dd>{{ sizeText }}</dd>
      <dt>Location</dt>
      <dd class="selectable">{{ location }}</dd>
      <template v-if="single && first.entry.symlink">
        <dt>Link target</dt>
        <dd class="selectable">{{ first.entry.target }}{{ first.entry.broken ? ' (broken)' : '' }}</dd>
      </template>
      <template v-if="single">
        <dt>Accessed</dt>
        <dd>{{ formatDate(first.atime) }}</dd>
        <dt>Modified</dt>
        <dd>{{ formatDate(first.entry.mtime) }}</dd>
      </template>
      <dt>Free space</dt>
      <dd>{{ formatSize(first.free) }} of {{ formatSize(first.capacity) }}</dd>
    </dl>

    <div v-else-if="first && tab === 'permissions'" class="perms">
      <dl class="grid">
        <template v-for="who in whos" :key="who">
          <dt>{{ whoLabel[who] }}</dt>
          <dd class="who">
            <span class="who-name">{{ who === 'owner' ? first.owner : who === 'group' ? first.group : '' }}</span>
            <select
              class="entry"
              :aria-label="`${whoLabel[who]} access`"
              :value="accessOf(perm, who, isDir)"
              :disabled="!canEdit || busy"
              @change="onAccess(who, $event)"
            >
              <option v-for="c in choices(isDir)" :key="c.value" :value="c.value">{{ c.label }}</option>
              <option v-if="accessOf(perm, who, isDir) === 'custom'" value="custom" disabled>Custom</option>
            </select>
          </dd>
        </template>
        <template v-if="!isDir">
          <dt>Execute</dt>
          <dd>
            <label class="check">
              <input type="checkbox" :checked="isExecutable(perm)" :disabled="!canEdit || busy" @change="onExec" />
              Allow executing file as program
            </label>
          </dd>
        </template>
      </dl>
      <p class="dim note">
        {{ first.entry.mode }} ({{ perm.toString(8).padStart(3, '0') }})
        <template v-if="!first.mine"> · Only the owner, {{ first.owner }}, can change these permissions.</template>
        <template v-else-if="first.entry.symlink"> · Links have no permissions of their own.</template>
      </p>
    </div>

    <footer>
      <button class="btn" @click="ctx.close()">Close</button>
    </footer>
  </div>
</template>

<style scoped>
.properties {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  padding: 16px;
  gap: 12px;
  background: var(--window-bg);
  overflow-y: auto;
}
header {
  display: flex;
  align-items: center;
  gap: 12px;
}
.name {
  margin: 0;
  font-size: 1.15em;
  overflow-wrap: anywhere;
  user-select: text;
}
.tabs {
  display: flex;
  gap: 2px;
  border-bottom: 1px solid var(--border);
}
.tabs button {
  padding: 6px 14px;
  border: none;
  border-bottom: 2px solid transparent;
  background: transparent;
  color: var(--view-dim-fg);
}
.tabs button.active {
  color: var(--window-fg);
  border-bottom-color: var(--accent);
}
.grid {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 8px 16px;
  margin: 0;
  align-items: center;
}
dt {
  color: var(--view-dim-fg);
  text-align: right;
}
dd {
  margin: 0;
  min-width: 0;
  overflow-wrap: anywhere;
}
.selectable {
  user-select: text;
}
.who {
  display: flex;
  align-items: center;
  gap: 12px;
}
.who-name {
  min-width: 80px;
}
.who select {
  flex: 1;
  max-width: 240px;
}
.check {
  display: flex;
  align-items: center;
  gap: 8px;
}
.check input {
  accent-color: var(--accent);
}
.note {
  margin: 12px 0 0;
  font-size: var(--font-size-small);
}
.error {
  color: var(--danger);
}
footer {
  display: flex;
  justify-content: flex-end;
  margin-top: auto;
}
</style>
