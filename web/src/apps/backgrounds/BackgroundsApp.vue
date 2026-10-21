<script setup lang="ts">
// Backgrounds: pick the desktop background from Cinnamon's collections, the
// Pictures folder and folders added in Cinnamon's Backgrounds settings, with
// Cinnamon's picture aspect and color options. The choice is Tilde's own
// (D37); "Use Cinnamon's Background" drops it.
import { computed, onMounted, ref } from 'vue'
import { fsApi } from '@/api/fs'
import type { Background, BackgroundCollection, BackgroundOptions, Entry } from '@/api/types'
import type { AppContext } from '@/apps/types'
import FileIcon from '@/components/FileIcon.vue'
import { iconsForEntry } from '@/icons/theme'
import { useBackground } from '@/stores/background'

const props = defineProps<{ ctx: AppContext }>()

const background = useBackground()
const collections = ref<BackgroundCollection[]>([])
const active = ref(0)
const loading = ref(true)
const error = ref('')

const aspects: { value: BackgroundOptions; label: string }[] = [
  { value: 'wallpaper', label: 'Mosaic' },
  { value: 'centered', label: 'Centered' },
  { value: 'scaled', label: 'Scaled' },
  { value: 'stretched', label: 'Stretched' },
  { value: 'zoom', label: 'Zoom' },
  { value: 'spanned', label: 'Spanned' },
  { value: 'none', label: 'No picture' },
]
const gradients: { value: Background['shading']; label: string }[] = [
  { value: 'solid', label: 'None' },
  { value: 'horizontal', label: 'Horizontal' },
  { value: 'vertical', label: 'Vertical' },
]

const bg = computed(() => background.current)
const images = computed(() => collections.value[active.value]?.images ?? [])

async function change(c: Parameters<typeof background.set>[0]) {
  try {
    await background.set(c)
  } catch (e) {
    props.ctx.notify(`Could not change the background: ${(e as Error).message}`, 'error')
    void background.load()
  }
}

function pick(e: Entry) {
  // Picking a picture while "No picture" is set shows it zoomed, as Cinnamon does.
  void change({ picture: e.path, options: bg.value?.options === 'none' ? 'zoom' : bg.value?.options })
}

async function useCinnamon() {
  try {
    await background.reset()
  } catch (e) {
    props.ctx.notify(`Could not change the background: ${(e as Error).message}`, 'error')
  }
}

function onSelect(e: Event, key: 'options' | 'shading') {
  const v = (e.target as HTMLSelectElement).value
  void change(key === 'options' ? { options: v as BackgroundOptions } : { shading: v as Background['shading'] })
}

function onColor(e: Event, key: 'primary' | 'secondary') {
  void change({ [key]: (e.target as HTMLInputElement).value })
}

onMounted(async () => {
  try {
    const [cols] = await Promise.all([fsApi.backgroundCollections(), background.current ? null : background.load()])
    collections.value = cols
    // Open the collection that holds the current picture.
    const i = cols.findIndex((c) => c.images.some((e) => e.path === background.current?.picture))
    if (i >= 0) active.value = i
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="backgrounds">
    <div class="body">
      <aside class="sidebar" aria-label="Collections">
        <button
          v-for="(c, i) in collections"
          :key="c.name + i"
          class="place"
          :class="{ active: i === active }"
          @click="active = i"
        >
          <span>{{ c.name }}</span>
        </button>
      </aside>
      <div class="grid" role="listbox" aria-label="Pictures">
        <p v-if="error" class="message dim">{{ error }}</p>
        <p v-else-if="!loading && !collections.length" class="message dim">No pictures found.</p>
        <button
          v-for="e in images"
          :key="e.path"
          class="picture"
          role="option"
          :aria-selected="bg?.picture === e.path"
          :class="{ selected: bg?.picture === e.path }"
          :title="e.name"
          @click="pick(e)"
        >
          <FileIcon :name="iconsForEntry(e)" :size="128" :entry="e" />
        </button>
      </div>
    </div>
    <footer class="options">
      <label>
        Picture aspect
        <select :value="bg?.options ?? 'zoom'" @change="onSelect($event, 'options')">
          <option v-for="a in aspects" :key="a.value" :value="a.value">{{ a.label }}</option>
        </select>
      </label>
      <label>
        Gradient
        <select :value="bg?.shading ?? 'solid'" @change="onSelect($event, 'shading')">
          <option v-for="g in gradients" :key="g.value" :value="g.value">{{ g.label }}</option>
        </select>
      </label>
      <label>
        {{ bg?.shading === 'solid' ? 'Color' : 'Start color' }}
        <input type="color" :value="bg?.primary ?? '#000000'" @change="onColor($event, 'primary')" />
      </label>
      <label v-if="bg && bg.shading !== 'solid'">
        End color
        <input type="color" :value="bg.secondary" @change="onColor($event, 'secondary')" />
      </label>
      <span class="spacer" />
      <button
        class="btn"
        :disabled="bg?.source !== 'tilde'"
        title="Follow the background set in Cinnamon"
        @click="useCinnamon"
      >
        Use Cinnamon’s Background
      </button>
    </footer>
  </div>
</template>

<style scoped>
.backgrounds {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  background: var(--window-bg);
  color: var(--window-fg);
}
.body {
  flex: 1;
  min-height: 0;
  display: flex;
}
.sidebar {
  width: 180px;
  flex: none;
  overflow-y: auto;
  padding: 6px 0;
  background: var(--sidebar-bg);
  color: var(--sidebar-fg);
  border-right: 1px solid var(--border);
}
.place {
  display: block;
  width: 100%;
  padding: 6px 12px 6px 16px;
  border: none;
  background: transparent;
  color: inherit;
  text-align: left;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.place:hover {
  background: var(--hover-bg);
}
.place.active {
  background: var(--selection-bg);
  color: var(--selection-fg);
}
.grid {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  display: grid;
  grid-template-columns: repeat(auto-fill, 152px);
  grid-auto-rows: 152px;
  gap: 6px;
  align-content: start;
  padding: 10px;
  background: var(--view-bg);
}
.picture {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 2px solid transparent;
  border-radius: var(--radius);
  background: transparent;
}
.picture:hover {
  background: var(--hover-bg);
}
.picture.selected {
  border-color: var(--selection-bg);
  background: var(--selection-unfocused-bg);
}
.message {
  grid-column: 1 / -1;
  padding: 20px;
  text-align: center;
}
.options {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 16px;
  padding: 8px 12px;
  border-top: 1px solid var(--border);
  background: var(--headerbar-bg);
}
.options label {
  display: flex;
  align-items: center;
  gap: 6px;
}
.options select {
  padding: 3px 6px;
  border: 1px solid var(--entry-border);
  border-radius: var(--radius);
  background: var(--entry-bg);
  color: var(--entry-fg);
}
.options input[type='color'] {
  width: 32px;
  height: 24px;
  padding: 0;
  border: 1px solid var(--entry-border);
  border-radius: var(--radius);
  background: var(--entry-bg);
}
.spacer {
  flex: 1;
}
</style>
