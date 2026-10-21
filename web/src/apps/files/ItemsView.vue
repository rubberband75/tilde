<script setup lang="ts">
// The icon grid and list of a Files window. Only the rows in view are
// rendered (cells have a fixed size per zoom level), so a folder with
// 100 000 entries scrolls as smoothly as one with ten. Selection, renaming
// and drops are owned by FilesApp; this component draws and reports events.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { formatDate, formatSize } from '@/lib/format'
import type { Item, SortKey, ViewMode } from './types'

const props = defineProps<{
  items: Item[]
  mode: ViewMode
  /** Icon size in CSS pixels for the current zoom level. */
  iconSize: number
  selected: Set<string>
  cut: Set<string>
  dropTarget: string | null
  renaming: string | null
  sortKey: SortKey
  sortDesc: boolean
  dateLabel: string
  /** Show the Location column (search results). */
  showLocation: boolean
}>()

const renameValue = defineModel<string>('renameValue', { required: true })

const emit = defineEmits<{
  itemClick: [item: Item, e: MouseEvent]
  itemOpen: [item: Item]
  itemContext: [item: Item]
  itemDragstart: [item: Item, e: DragEvent]
  itemDragover: [item: Item, e: DragEvent]
  itemDragleave: [item: Item]
  itemDrop: [item: Item, e: DragEvent]
  sort: [key: SortKey]
  renameCommit: []
  renameCancel: []
}>()

const scroller = ref<HTMLElement | null>(null)
const scrollTop = ref(0)
const viewW = ref(0)
const viewH = ref(0)
let ro: ResizeObserver | undefined

const grid = computed(() => props.mode === 'grid')
const pad = computed(() => (grid.value ? 8 : 0))
const gap = computed(() => (grid.value ? 4 : 0))
const cellW = computed(() => (grid.value ? Math.max(104, props.iconSize + 48) : viewW.value))
/** Icon, padding and two label lines; list rows are a little taller than the icon. */
const cellH = computed(() => (grid.value ? props.iconSize + 52 : Math.max(26, props.iconSize + 10)))
const cols = computed(() =>
  grid.value ? Math.max(1, Math.floor((viewW.value - 2 * pad.value + gap.value) / (cellW.value + gap.value))) : 1,
)
const step = computed(() => cellH.value + gap.value)
const rows = computed(() => Math.ceil(props.items.length / cols.value))
const totalH = computed(() => (rows.value ? rows.value * step.value - gap.value + 2 * pad.value : 0))

const OVERSCAN = 3
const range = computed(() => {
  const first = Math.max(0, Math.floor((scrollTop.value - pad.value) / step.value) - OVERSCAN)
  const last = Math.min(rows.value, Math.ceil((scrollTop.value + viewH.value - pad.value) / step.value) + OVERSCAN)
  return { start: first * cols.value, end: Math.min(props.items.length, last * cols.value) }
})
const visible = computed(() =>
  props.items.slice(range.value.start, range.value.end).map((item, i) => ({ item, index: range.value.start + i })),
)

function position(index: number) {
  const row = Math.floor(index / cols.value)
  const col = index % cols.value
  return {
    top: `${pad.value + row * step.value}px`,
    left: `${pad.value + col * (cellW.value + gap.value)}px`,
    width: grid.value ? `${cellW.value}px` : '100%',
    minHeight: `${cellH.value}px`,
    height: grid.value ? undefined : `${cellH.value}px`,
  }
}

/** Scrolls so the item at index is fully visible. */
function scrollToIndex(index: number) {
  const el = scroller.value
  if (!el || index < 0) return
  const top = pad.value + Math.floor(index / cols.value) * step.value
  const bottom = top + cellH.value
  let next = scrollTop.value
  if (top < next) next = Math.max(0, top - pad.value)
  else if (bottom > next + viewH.value) next = bottom - viewH.value + pad.value
  if (next !== scrollTop.value) {
    el.scrollTop = next
    // Update now, not on the async scroll event, so the item is rendered
    // before the caller's nextTick (renaming focuses its input).
    scrollTop.value = el.scrollTop
  }
}

/** How many items one page (PageUp/PageDown) moves. */
const pageItems = computed(() => Math.max(1, Math.floor(viewH.value / step.value)) * cols.value)

defineExpose({ scrollToIndex, cols, pageItems })

function onScroll() {
  scrollTop.value = scroller.value?.scrollTop ?? 0
}

// A new folder starts at the top.
watch(
  () => props.items.length === 0,
  (empty) => {
    if (empty && scroller.value) scroller.value.scrollTop = scrollTop.value = 0
  },
)

onMounted(() => {
  ro = new ResizeObserver(([e]) => {
    if (!e) return
    viewW.value = e.contentRect.width
    viewH.value = e.contentRect.height
  })
  if (scroller.value) ro.observe(scroller.value)
})
onBeforeUnmount(() => ro?.disconnect())

const columns: { key: SortKey; label: () => string; cls: string }[] = [
  { key: 'name', label: () => 'Name', cls: 'name' },
  { key: 'size', label: () => 'Size', cls: 'size' },
  { key: 'type', label: () => 'Type', cls: 'type' },
  { key: 'mtime', label: () => props.dateLabel, cls: 'date' },
]

function droppable(item: Item) {
  return item.entry?.type === 'dir' && !item.trash
}
</script>

<template>
  <div class="items" :class="[mode, { located: showLocation }]">
    <div v-if="!grid" class="header row">
      <button
        v-for="c in columns"
        :key="c.key"
        class="col"
        :class="[c.cls, { sorted: sortKey === c.key }]"
        @click="emit('sort', c.key)"
      >
        {{ c.label() }}
        <SymbolicIcon v-if="sortKey === c.key" :name="sortDesc ? 'chevron-up' : 'chevron-down'" :size="12" />
      </button>
      <span v-if="showLocation" class="col location">Location</span>
    </div>
    <div ref="scroller" class="scroller" @scroll.passive="onScroll">
      <div class="canvas" :style="{ height: `${totalH}px` }">
        <div
          v-for="{ item, index } in visible"
          :key="item.key"
          data-item
          :class="[
            grid ? 'cell' : 'row',
            {
              selected: selected.has(item.key),
              hidden: item.entry?.hidden,
              cut: cut.has(item.key),
              target: dropTarget === item.key,
            },
          ]"
          :style="position(index)"
          :title="grid ? item.name : undefined"
          :draggable="!!item.entry && !item.trash && renaming !== item.key"
          @click="emit('itemClick', item, $event)"
          @dblclick="emit('itemOpen', item)"
          @contextmenu="emit('itemContext', item)"
          @dragstart="emit('itemDragstart', item, $event)"
          @dragover="droppable(item) && emit('itemDragover', item, $event)"
          @dragleave="emit('itemDragleave', item)"
          @drop="droppable(item) && emit('itemDrop', item, $event)"
        >
          <div class="col name">
            <ThemeIcon :name="item.icon" :size="iconSize" />
            <input
              v-if="renaming === item.key"
              v-model="renameValue"
              class="rename entry"
              @keydown.enter.stop.prevent="emit('renameCommit')"
              @keydown.esc.stop.prevent="emit('renameCancel')"
              @blur="emit('renameCommit')"
              @click.stop
              @dblclick.stop
            />
            <span v-else class="label">{{ item.name }}</span>
          </div>
          <template v-if="!grid">
            <span class="col size">{{ item.size !== undefined ? formatSize(item.size) : '' }}</span>
            <span class="col type">{{ item.type }}</span>
            <span class="col date">{{ item.mtime ? formatDate(item.mtime) : '' }}</span>
            <span v-if="showLocation" class="col location">{{ item.location }}</span>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.items {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
}
.scroller {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  scrollbar-gutter: stable;
}
.canvas {
  position: relative;
}

/* Grid */
.cell {
  position: absolute;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 6px 4px;
  border-radius: 4px;
}
.cell .name {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  width: 100%;
}
.cell .label {
  max-width: 100%;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
  text-align: center;
  line-height: 1.25;
}
/* A selected cell shows its whole name over the cells below, as in Nemo. */
.cell.selected {
  z-index: 1;
}
.cell.selected .label {
  -webkit-line-clamp: 6;
}
.cell .rename {
  width: 100%;
  min-height: 24px;
  padding: 2px 4px;
  text-align: center;
}

/* List */
.row {
  display: grid;
  grid-template-columns: minmax(120px, 1fr) 90px minmax(80px, 160px) 170px;
  align-items: center;
}
.located .row {
  grid-template-columns: minmax(120px, 1fr) 90px minmax(80px, 140px) 170px minmax(80px, 1fr);
}
.list .row:not(.header) {
  position: absolute;
  left: 0;
}
.col {
  padding: 0 8px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.row .name {
  display: flex;
  align-items: center;
  gap: 8px;
}
.row .label {
  overflow: hidden;
  text-overflow: ellipsis;
}
.row .size {
  text-align: right;
}
.row .size,
.row .type,
.row .date,
.row .location {
  color: var(--view-dim-fg);
}
.header {
  flex: none;
  height: 28px;
  background: var(--window-bg);
  border-bottom: 1px solid var(--border);
  overflow-y: hidden;
  scrollbar-gutter: stable;
}
.header .col {
  height: 100%;
  display: flex;
  align-items: center;
  gap: 4px;
  border: none;
  background: transparent;
  color: var(--view-dim-fg);
  font: inherit;
  text-align: left;
}
.header button.col:hover {
  background: var(--hover-bg);
}
.header .col.sorted {
  color: var(--view-fg);
}
.header .col.size {
  justify-content: flex-end;
}
.row .rename {
  flex: 1;
  min-height: 22px;
  padding: 1px 4px;
}

/* States */
.cell:hover,
.row:not(.header):hover {
  background: var(--hover-bg);
}
.cell.selected,
.row.selected {
  background: var(--selection-unfocused-bg);
}
:global(.view:focus) .cell.selected,
:global(.view:focus) .row.selected {
  background: var(--selection-bg);
  color: var(--selection-fg);
}
:global(.view:focus) .row.selected .col {
  color: var(--selection-fg);
}
.hidden,
.cut {
  opacity: 0.65;
}
.target {
  box-shadow: inset 0 0 0 2px var(--accent);
}
</style>
