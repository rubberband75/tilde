<script setup lang="ts">
// A line graph of the last minute (D61): one value a second, the newest on
// the right. Up to two series on one axis, in the categorical slots'
// order; a hover crosshair reads every series out at that second. The
// compact form (the panel applet) has no axis and no hover: its applet's
// tooltip carries the figures.
import { computed, ref } from 'vue'

export interface GraphSeries {
  label: string
  /** Categorical slot: 1 first, 2 second (theme tokens --series-1, --series-2). */
  slot: 1 | 2
  values: number[]
}

const props = withDefaults(
  defineProps<{
    series: GraphSeries[]
    /** How many seconds the width holds. */
    length?: number
    /** The axis top, or auto: the data's peak rounded up. */
    max?: number | 'auto'
    format?: (v: number) => string
    compact?: boolean
    label: string
  }>(),
  { length: 60, max: 'auto', format: (v: number) => String(Math.round(v)), compact: false },
)

const H = 100

/** 1, 2 or 5 times a power of ten, at or above v. */
function niceCeil(v: number): number {
  if (v <= 0) return 1
  const p = 10 ** Math.floor(Math.log10(v))
  for (const m of [1, 2, 5, 10]) if (m * p >= v) return m * p
  return 10 * p
}

const top = computed(() => {
  if (props.max !== 'auto') return props.max
  const peak = Math.max(0, ...props.series.flatMap((s) => s.values))
  return niceCeil(peak)
})

const span = computed(() => Math.max(1, props.length - 1))

/** x of the i-th of n values, the last at the right edge. */
const x = (i: number, n: number) => props.length - n + i
const y = (v: number) => H - (Math.min(Math.max(v, 0), top.value) / top.value) * H

function points(values: number[]) {
  return values.map((v, i) => `${x(i, values.length)},${y(v)}`).join(' ')
}

function area(values: number[]) {
  if (values.length < 2) return ''
  const n = values.length
  return `${x(0, n)},${H} ${points(values)} ${x(n - 1, n)},${H}`
}

/** Seconds back from the newest the pointer is at, while hovering. */
const hover = ref<number | null>(null)
const longest = computed(() => Math.max(0, ...props.series.map((s) => s.values.length)))

function onMove(e: PointerEvent) {
  const el = e.currentTarget as SVGElement
  const box = el.getBoundingClientRect()
  const pos = ((e.clientX - box.left) / Math.max(1, box.width)) * span.value
  const back = Math.round(span.value - pos)
  hover.value = back < longest.value ? Math.max(0, back) : null
}

const readout = computed(() => {
  if (hover.value === null) return null
  const back = hover.value
  return {
    x: ((span.value - back) / span.value) * 100,
    when: back === 0 ? 'now' : `${back} s ago`,
    rows: props.series.map((s) => {
      const v = s.values[s.values.length - 1 - back]
      return {
        label: s.label,
        slot: s.slot,
        value: v === undefined ? '' : props.format(v),
        y: v === undefined ? null : y(v),
      }
    }),
  }
})
</script>

<template>
  <div class="graph" :class="{ compact }">
    <svg
      :viewBox="`0 0 ${span} ${H}`"
      preserveAspectRatio="none"
      role="img"
      :aria-label="label"
      @pointermove="!compact && onMove($event)"
      @pointerleave="hover = null"
    >
      <template v-if="!compact">
        <line v-for="g in [25, 50, 75]" :key="g" class="grid" x1="0" :x2="span" :y1="g" :y2="g" />
      </template>
      <template v-for="s in series" :key="s.label">
        <polygon v-if="series.length === 1" class="area" :class="`slot-${s.slot}`" :points="area(s.values)" />
        <polyline class="line" :class="`slot-${s.slot}`" :points="points(s.values)" />
      </template>
      <line
        v-if="readout"
        class="crosshair"
        :x1="(readout.x / 100) * span"
        :x2="(readout.x / 100) * span"
        y1="0"
        :y2="H"
      />
    </svg>
    <template v-if="readout">
      <span
        v-for="r in readout.rows.filter((r) => r.y !== null)"
        :key="r.label"
        class="dot"
        :class="`slot-${r.slot}`"
        :style="{ left: `${readout.x}%`, top: `${r.y}%` }"
      />
      <div class="tooltip" :class="{ flip: readout.x > 60 }" :style="{ left: `${readout.x}%` }" role="status">
        <div class="when">{{ readout.when }}</div>
        <div v-for="r in readout.rows" :key="r.label" class="row">
          <span class="swatch" :class="`slot-${r.slot}`" />
          <span class="name">{{ r.label }}</span>
          <span class="value">{{ r.value }}</span>
        </div>
      </div>
    </template>
    <span v-if="!compact" class="top">{{ format(top) }}</span>
  </div>
</template>

<style scoped>
.graph {
  position: relative;
  height: 100%;
}
svg {
  display: block;
  width: 100%;
  height: 100%;
  overflow: visible;
}
.grid {
  stroke: var(--graph-grid);
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}
.line {
  fill: none;
  stroke-width: 2;
  stroke-linejoin: round;
  stroke-linecap: round;
  vector-effect: non-scaling-stroke;
}
.compact .line {
  stroke-width: 1.5;
}
.area {
  stroke: none;
  opacity: 0.22;
}
.line.slot-1 {
  stroke: var(--series-1);
}
.line.slot-2 {
  stroke: var(--series-2);
}
.area.slot-1,
.swatch.slot-1,
.dot.slot-1 {
  fill: var(--series-1);
  background: var(--series-1);
}
.area.slot-2,
.swatch.slot-2,
.dot.slot-2 {
  fill: var(--series-2);
  background: var(--series-2);
}
.crosshair {
  stroke: var(--graph-crosshair);
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}
.dot {
  position: absolute;
  width: 8px;
  height: 8px;
  margin: -4px 0 0 -4px;
  border-radius: 50%;
  box-shadow: 0 0 0 2px var(--view-bg);
  pointer-events: none;
}
.tooltip {
  position: absolute;
  top: 4px;
  margin-left: 10px;
  padding: 6px 8px;
  border: 1px solid var(--popup-border);
  border-radius: var(--radius);
  background: var(--popup-bg);
  color: var(--window-fg);
  box-shadow: var(--wm-shadow-unfocused);
  white-space: nowrap;
  pointer-events: none;
  z-index: 1;
}
.tooltip.flip {
  transform: translateX(calc(-100% - 20px));
}
.when {
  margin-bottom: 2px;
  color: var(--view-dim-fg);
}
.row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.swatch {
  width: 10px;
  height: 3px;
  border-radius: 2px;
}
.value {
  margin-left: auto;
  padding-left: 12px;
  font-variant-numeric: tabular-nums;
}
.top {
  position: absolute;
  top: 2px;
  left: 4px;
  color: var(--view-dim-fg);
  font-size: var(--font-size-small);
  pointer-events: none;
}
</style>
