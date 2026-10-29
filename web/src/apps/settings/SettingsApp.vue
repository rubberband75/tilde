<script setup lang="ts">
// System Settings (D72), a few of Cinnamon's modules: the screen lock,
// effects, the panel's applets, the background (the Backgrounds app), and
// recent logins. Changes apply at once, as in Cinnamon, and are saved in
// ~/.config/tilde by the worker; a desktop's own settings are read, never
// written (D67).
import { computed, onMounted, ref, watch } from 'vue'
import { SwitchRoot, SwitchThumb } from 'reka-ui'
import { settingsApi } from '@/api/settings'
import type { LoginRecord, Settings } from '@/api/types'
import type { AppContext } from '@/apps/types'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { useSettings } from '@/stores/settings'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx
const store = useSettings()
const s = computed(() => store.settings)

type Section = 'lock' | 'effects' | 'panel' | 'background' | 'logins'
const sections: { id: Section; name: string; icon: string[] }[] = [
  { id: 'lock', name: 'Screen Lock', icon: ['preferences-desktop-screensaver', 'system-lock-screen'] },
  { id: 'effects', name: 'Effects', icon: ['preferences-desktop-effects', 'preferences-desktop'] },
  { id: 'panel', name: 'Panel', icon: ['preferences-desktop-panel', 'preferences-desktop'] },
  { id: 'background', name: 'Backgrounds', icon: ['preferences-desktop-wallpaper'] },
  { id: 'logins', name: 'Recent Logins', icon: ['preferences-system-privacy', 'dialog-password'] },
]
const section = ref<Section>(sections.some((x) => x.id === ctx.args.section) ? (ctx.args.section as Section) : 'lock')
watch(section, (id) => ctx.saveState({ section: id }))

onMounted(() => void store.load())

async function change(fn: (next: Settings) => void) {
  try {
    await store.save(fn)
  } catch (e) {
    ctx.notify(`Could not save the settings: ${(e as Error).message}`, 'error')
  }
}

/** The lock delays offered, in minutes, as Cinnamon's screensaver offers. */
const delays = [1, 2, 3, 5, 10, 15, 20, 30, 45, 60, 90, 120]

const desktopName = computed(() => {
  switch (store.desktopLock?.source) {
    case 'cinnamon':
      return 'Cinnamon'
    case 'gnome':
      return 'GNOME'
  }
  return null
})

/** What following the desktop means here. */
const desktopSays = computed(() => {
  const d = store.desktopLock
  if (!d) return ''
  const when = d.lockAfter > 0 ? `lock after ${Math.round(d.lockAfter / 60)} minutes without input` : 'never lock'
  return desktopName.value
    ? `${desktopName.value}’s setting: ${when}.`
    : `This computer has no desktop settings: ${when} (Tilde’s default).`
})

// Recent logins, read by the server from the audit log.
const logins = ref<LoginRecord[]>([])
const loginsAvailable = ref(true)
const loginsLoaded = ref(false)
watch(
  section,
  async (id) => {
    if (id !== 'logins') return
    try {
      const res = await settingsApi.logins()
      logins.value = res.entries
      loginsAvailable.value = res.available
    } catch {
      loginsAvailable.value = false
    } finally {
      loginsLoaded.value = true
    }
  },
  { immediate: true },
)

const when = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
</script>

<template>
  <div class="settings">
    <nav class="sections" aria-label="Settings">
      <button
        v-for="x in sections"
        :key="x.id"
        class="section"
        :class="{ on: section === x.id }"
        :aria-current="section === x.id ? 'page' : undefined"
        @click="section = x.id"
      >
        <ThemeIcon :name="x.icon" :size="24" />
        <span>{{ x.name }}</span>
      </button>
    </nav>

    <section v-if="section === 'lock'" class="pane" aria-labelledby="h-lock">
      <h2 id="h-lock">Screen Lock</h2>
      <p class="note">
        A browser left without input locks, and asks for your password again. Every browser you are signed in on follows
        this.
      </p>
      <div class="group" role="radiogroup" aria-label="Lock delay from">
        <label class="row">
          <input type="radio" name="lock-source" :checked="!s.lock.own" @change="change((n) => (n.lock.own = false))" />
          <span class="what">
            <strong>Follow {{ desktopName ?? 'the computer' }}</strong>
            <span class="note">{{ desktopSays }}</span>
          </span>
        </label>
        <label class="row">
          <input type="radio" name="lock-source" :checked="s.lock.own" @change="change((n) => (n.lock.own = true))" />
          <span class="what"><strong>Tilde’s own setting</strong></span>
        </label>
      </div>
      <div v-if="s.lock.own" class="group">
        <div class="row">
          <span class="what">Lock when idle</span>
          <SwitchRoot
            class="switch"
            aria-label="Lock when idle"
            :model-value="s.lock.enabled"
            @update:model-value="(on: boolean) => change((n) => (n.lock.enabled = on))"
          >
            <SwitchThumb class="thumb" />
          </SwitchRoot>
        </div>
        <label class="row">
          <span class="what">Lock after</span>
          <select
            class="entry"
            :disabled="!s.lock.enabled"
            :value="s.lock.minutes"
            @change="change((n) => (n.lock.minutes = Number(($event.target as HTMLSelectElement).value)))"
          >
            <option v-for="m in delays" :key="m" :value="m">{{ m === 1 ? '1 minute' : `${m} minutes` }}</option>
          </select>
        </label>
      </div>
      <p class="note">Lock Screen in the menu, or Ctrl+Alt+L, locks every browser at once.</p>
    </section>

    <section v-else-if="section === 'effects'" class="pane" aria-labelledby="h-effects">
      <h2 id="h-effects">Effects</h2>
      <div class="group">
        <div class="row">
          <span class="what">
            <strong>Window animations</strong>
            <span class="note">Windows open, close, minimize and tile with a short animation.</span>
          </span>
          <SwitchRoot
            class="switch"
            aria-label="Window animations"
            :model-value="s.effects.animations"
            @update:model-value="(on: boolean) => change((n) => (n.effects.animations = on))"
          >
            <SwitchThumb class="thumb" />
          </SwitchRoot>
        </div>
      </div>
      <p class="note">A system set to reduce motion turns them off too.</p>
    </section>

    <section v-else-if="section === 'panel'" class="pane" aria-labelledby="h-panel">
      <h2 id="h-panel">Panel</h2>
      <div class="group">
        <div
          v-for="a in [
            { key: 'resources', name: 'System monitor', note: 'CPU and memory graphs; a click opens System Monitor.' },
            { key: 'network', name: 'Network', note: 'Shown only where NetworkManager runs.' },
            {
              key: 'notifications',
              name: 'Notifications',
              note: 'Programs’ notifications, while Tilde shows them; toasts show either way.',
            },
            { key: 'fullscreen', name: 'Fullscreen button', note: 'Fullscreen also stays in the menu.' },
          ] as const"
          :key="a.key"
          class="row"
        >
          <span class="what">
            <strong>{{ a.name }}</strong>
            <span class="note">{{ a.note }}</span>
          </span>
          <SwitchRoot
            class="switch"
            :aria-label="a.name"
            :model-value="s.panel[a.key]"
            @update:model-value="(on: boolean) => change((n) => (n.panel[a.key] = on))"
          >
            <SwitchThumb class="thumb" />
          </SwitchRoot>
        </div>
      </div>
    </section>

    <section v-else-if="section === 'background'" class="pane" aria-labelledby="h-bg">
      <h2 id="h-bg">Backgrounds</h2>
      <p class="note">The desktop background has its own window, with Cinnamon’s collections and your pictures.</p>
      <button class="btn" @click="ctx.launch('backgrounds')">Open Backgrounds</button>
    </section>

    <section v-else class="pane" aria-labelledby="h-logins">
      <h2 id="h-logins">Recent Logins</h2>
      <p class="note">Logins to your account, and attempts that failed, from Tilde’s audit log.</p>
      <p v-if="loginsLoaded && !loginsAvailable" class="note">
        The audit log cannot be read here (Tilde runs without access to the system journal).
      </p>
      <p v-else-if="loginsLoaded && !logins.length" class="note">None yet.</p>
      <ul v-else class="logins" aria-label="Recent logins">
        <li v-for="(l, i) in logins" :key="i" :class="{ failed: l.failed }">
          <time>{{ when.format(l.time) }}</time>
          <span>{{ l.text }}</span>
        </li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.settings {
  display: flex;
  height: 100%;
  background: var(--window-bg);
  color: var(--window-fg);
}
.sections {
  display: flex;
  flex: none;
  flex-direction: column;
  gap: 2px;
  width: 190px;
  padding: 8px;
  overflow: auto;
  background: var(--sidebar-bg);
  border-right: 1px solid var(--border);
}
.section {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 10px;
  border: none;
  border-radius: var(--radius);
  background: transparent;
  color: var(--sidebar-fg);
  text-align: left;
}
.section:hover {
  background: var(--hover-bg);
}
.section.on {
  background: var(--selection-bg);
  color: var(--selection-fg);
}
.pane {
  flex: 1;
  min-width: 0;
  padding: 16px 20px;
  overflow: auto;
}
h2 {
  margin: 0 0 8px;
  font-size: 1.2em;
}
.note {
  margin: 0 0 12px;
  color: var(--view-dim-fg);
}
.what .note {
  margin: 0;
}
.group {
  margin: 0 0 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--view-bg);
}
.row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
}
.row + .row {
  border-top: 1px solid var(--border);
}
.what {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}
.logins {
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--border);
  background: var(--view-bg);
}
.logins li {
  display: flex;
  gap: 14px;
  padding: 6px 10px;
}
.logins li + li {
  border-top: 1px solid var(--border);
}
.logins time {
  flex: none;
  color: var(--view-dim-fg);
}
.logins li.failed span {
  color: var(--danger);
}
</style>
