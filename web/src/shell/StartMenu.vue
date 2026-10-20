<script setup lang="ts">
// The Cinnamon-style application menu: favorites and session buttons on the
// left, search, categories and apps on the right.
import { computed, nextTick, ref, watch } from 'vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { apps } from '@/apps/registry'
import type { AppManifest } from '@/apps/types'
import ThemeIcon from '@/components/ThemeIcon.vue'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import { useSession } from '@/stores/session'
import { useWindows } from '@/stores/windows'

const wm = useWindows()
const session = useSession()

const open = ref(false)
const query = ref('')
const category = ref<string>('All Applications')
const search = ref<HTMLInputElement | null>(null)

const favorites = ['files', 'terminal', 'system-info']
const favoriteApps = computed(() =>
  favorites.map((id) => apps.find((a) => a.id === id)).filter((a): a is AppManifest => !!a),
)

const visibleApps = computed(() => apps.filter((a) => !a.hidden))
const categories = computed(() => ['All Applications', ...new Set(visibleApps.value.map((a) => a.category).sort())])

const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  let list = visibleApps.value
  if (q) list = list.filter((a) => a.name.toLowerCase().includes(q) || a.comment.toLowerCase().includes(q))
  else if (category.value !== 'All Applications') list = list.filter((a) => a.category === category.value)
  return [...list].sort((a, b) => a.name.localeCompare(b.name))
})

const hovered = ref<AppManifest | null>(null)

function launch(app: AppManifest) {
  wm.open(app.id)
  open.value = false
}

function onEnter() {
  const first = shown.value[0]
  if (first) launch(first)
}

async function logOut() {
  open.value = false
  await session.logout()
}

watch(open, async (v) => {
  if (v) {
    query.value = ''
    category.value = 'All Applications'
    await nextTick()
    search.value?.focus()
  }
})
</script>

<template>
  <PopoverRoot v-model:open="open">
    <PopoverTrigger class="menu-button" :class="{ active: open }" title="Menu">
      <span class="tilde-mark" aria-hidden="true">~</span>
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent class="start-menu" side="top" align="start" :side-offset="0" :avoid-collisions="false">
        <div class="side">
          <div class="favorites">
            <button v-for="a in favoriteApps" :key="a.id" class="fav" :title="a.name" @click="launch(a)">
              <ThemeIcon :name="a.icon" :size="32" />
            </button>
          </div>
          <div class="session">
            <button class="fav" title="Lock Screen (not available yet)" disabled>
              <SymbolicIcon name="lock" :size="18" />
            </button>
            <button class="fav" title="Log Out" @click="logOut">
              <SymbolicIcon name="logout" :size="18" />
            </button>
          </div>
        </div>
        <div class="main">
          <div class="lists">
            <ul class="categories">
              <li
                v-for="c in categories"
                :key="c"
                :class="{ selected: !query && category === c }"
                @pointerenter="category = c"
                @click="category = c"
              >
                {{ c }}
              </li>
            </ul>
            <ul class="apps">
              <li
                v-for="a in shown"
                :key="a.id"
                tabindex="0"
                @click="launch(a)"
                @keydown.enter="launch(a)"
                @pointerenter="hovered = a"
                @pointerleave="hovered = null"
              >
                <ThemeIcon :name="a.icon" :size="24" />
                <span>{{ a.name }}</span>
              </li>
              <li v-if="shown.length === 0" class="empty">No applications found</li>
            </ul>
          </div>
          <div class="footer">
            <div class="desc">
              <template v-if="hovered">
                <strong>{{ hovered.name }}</strong>
                <span>{{ hovered.comment }}</span>
              </template>
              <template v-else>
                <strong>{{ session.info?.user }}</strong>
                <span>{{ session.info?.home }}</span>
              </template>
            </div>
            <input
              ref="search"
              v-model="query"
              class="search"
              type="search"
              placeholder="Search"
              aria-label="Search applications"
              @keydown.enter="onEnter"
            />
          </div>
        </div>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>

<style scoped>
.menu-button {
  display: grid;
  place-items: center;
  width: 44px;
  height: var(--panel-height);
  padding: 0;
  border: none;
  background: transparent;
  color: var(--panel-fg);
}
.menu-button:hover,
.menu-button.active {
  background: var(--panel-item-hover-bg);
}
.tilde-mark {
  font-size: 30px;
  font-weight: bold;
  line-height: 1;
  color: var(--accent);
  transform: translateY(-3px);
}
</style>

<style>
/* Portaled content is outside this component's scope. */
.start-menu {
  display: flex;
  width: 560px;
  height: 480px;
  max-height: calc(100vh - var(--panel-height) - 10px);
  padding: 10px;
  gap: 10px;
  background: var(--menu-bg);
  color: var(--menu-fg);
  border-radius: var(--menu-radius) var(--menu-radius) 0 0;
  box-shadow: var(--menu-shadow);
  z-index: 100000;
  outline: none;
}
.start-menu .side {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  padding: 10px 6px;
  background: var(--menu-box-bg);
  border-radius: var(--menu-radius);
}
.start-menu .favorites,
.start-menu .session {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.start-menu .fav {
  display: grid;
  place-items: center;
  width: 44px;
  height: 44px;
  border: none;
  border-radius: var(--menu-radius);
  background: transparent;
  color: var(--menu-fg);
}
.start-menu .fav:hover:not(:disabled) {
  background: var(--menu-item-hover-bg);
  filter: brightness(1.2);
}
.start-menu .fav:disabled {
  opacity: 0.4;
}
.start-menu .main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.start-menu .lists {
  flex: 1;
  min-height: 0;
  display: flex;
  gap: 6px;
}
.start-menu ul {
  margin: 0;
  padding: 0;
  list-style: none;
  overflow-y: auto;
}
.start-menu .categories {
  width: 170px;
  flex: none;
}
.start-menu .apps {
  flex: 1;
}
.start-menu li {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px;
  border-radius: var(--menu-radius);
  cursor: default;
}
.start-menu .categories li.selected,
.start-menu li:hover,
.start-menu .apps li:focus-visible {
  background: var(--menu-item-hover-bg);
  color: var(--menu-item-hover-fg);
  outline: none;
}
.start-menu li.empty {
  color: var(--menu-dim-fg);
}
.start-menu li.empty:hover {
  background: transparent;
}
.start-menu .footer {
  display: flex;
  align-items: flex-end;
  gap: 10px;
}
.start-menu .desc {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: var(--font-size-small);
}
.start-menu .desc span {
  color: var(--menu-dim-fg);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.start-menu .search {
  width: 200px;
  padding: 7px;
  border: 1px solid var(--menu-entry-bg);
  border-radius: 3px;
  background: var(--menu-entry-bg);
  color: var(--menu-entry-fg);
  outline: none;
}
.start-menu .search:focus {
  border-color: var(--accent);
}
</style>
