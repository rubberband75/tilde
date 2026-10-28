<script setup lang="ts">
// Cinnamon's calendar applet (D54): today's date on top, then a month with
// buttons (and the mouse wheel) to page through months. Clicking the month
// name comes back to today's. The locale and time zone are the browser's,
// as for the clock.
import { computed, ref, watch } from 'vue'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import { firstDayOfWeek, monthGrid, sameDay, weekdayLabels } from '@/lib/calendar'

const props = defineProps<{ now: Date }>()

const locale = navigator.language
const firstDay = firstDayOfWeek(locale)
const labels = weekdayLabels(locale, firstDay)

/** The month shown, as its first day. */
const shown = ref(new Date(props.now.getFullYear(), props.now.getMonth(), 1))
const isCurrentMonth = computed(
  () => shown.value.getFullYear() === props.now.getFullYear() && shown.value.getMonth() === props.now.getMonth(),
)
// At midnight on the last day the month moves on, if it was today's.
watch(
  () => [props.now.getFullYear(), props.now.getMonth()],
  ([y, m], [py, pm]) => {
    if (shown.value.getFullYear() === py && shown.value.getMonth() === pm) shown.value = new Date(y!, m!, 1)
  },
)

const weeks = computed(() => monthGrid(shown.value.getFullYear(), shown.value.getMonth(), firstDay))
const weekday = computed(() => new Intl.DateTimeFormat(locale, { weekday: 'long' }).format(props.now))
const fullDate = computed(() =>
  new Intl.DateTimeFormat(locale, { year: 'numeric', month: 'long', day: 'numeric' }).format(props.now),
)
const monthLabel = computed(() =>
  new Intl.DateTimeFormat(locale, { year: 'numeric', month: 'long' }).format(shown.value),
)
const dayLabel = new Intl.DateTimeFormat(locale, { dateStyle: 'full' })

function page(delta: number) {
  shown.value = new Date(shown.value.getFullYear(), shown.value.getMonth() + delta, 1)
}

function toToday() {
  shown.value = new Date(props.now.getFullYear(), props.now.getMonth(), 1)
}

function onWheel(e: WheelEvent) {
  if (e.deltaY) page(e.deltaY > 0 ? 1 : -1)
}
</script>

<template>
  <div class="calendar">
    <div class="today">
      <div class="weekday">{{ weekday }}</div>
      <div class="full-date">{{ fullDate }}</div>
    </div>
    <div class="month" @wheel.prevent="onWheel">
      <div class="month-bar">
        <button class="nav" title="Previous month" aria-label="Previous month" @click="page(-1)">
          <SymbolicIcon name="back" :size="14" />
        </button>
        <button
          class="month-label"
          :title="isCurrentMonth ? undefined : 'Back to this month'"
          :disabled="isCurrentMonth"
          @click="toToday"
        >
          {{ monthLabel }}
        </button>
        <button class="nav" title="Next month" aria-label="Next month" @click="page(1)">
          <SymbolicIcon name="forward" :size="14" />
        </button>
      </div>
      <table :aria-label="monthLabel">
        <thead>
          <tr>
            <th v-for="(l, i) in labels" :key="i" scope="col">{{ l }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="week in weeks" :key="week[0]!.getTime()">
            <td
              v-for="d in week"
              :key="d.getTime()"
              :class="{ other: d.getMonth() !== shown.getMonth(), current: sameDay(d, now) }"
              :aria-current="sameDay(d, now) ? 'date' : undefined"
              :aria-label="dayLabel.format(d)"
            >
              {{ d.getDate() }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.calendar {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.today {
  padding: 4px 6px;
}
.weekday {
  font-size: 1.15em;
  font-weight: bold;
}
.full-date {
  color: var(--menu-dim-fg);
}
.month {
  padding: 8px;
  border-radius: var(--menu-radius);
  background: var(--menu-box-bg);
}
.month-bar {
  display: flex;
  align-items: center;
  margin-bottom: 6px;
}
.month-bar button {
  border: none;
  border-radius: var(--menu-radius);
  background: transparent;
  color: inherit;
}
.nav {
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  padding: 0;
}
.month-label {
  flex: 1;
  padding: 4px;
  font-weight: bold;
}
.month-label:disabled {
  opacity: 1;
}
.month-bar button:not(:disabled):hover {
  background: var(--menu-item-hover-bg);
  color: var(--menu-item-hover-fg);
}
table {
  border-collapse: collapse;
  table-layout: fixed;
  width: 100%;
}
th {
  padding: 2px 0 4px;
  color: var(--menu-dim-fg);
  font-size: 0.85em;
  font-weight: normal;
}
td {
  height: 30px;
  padding: 0;
  text-align: center;
  border-radius: 50%;
}
td.other {
  color: var(--menu-dim-fg);
  opacity: 0.6;
}
td.current {
  background: var(--accent);
  color: var(--accent-fg);
  font-weight: bold;
}
</style>
