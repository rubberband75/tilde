<script setup lang="ts">
// The panel's sound applet (D55, D79), as Cinnamon's: the host's output
// volume and mute, which output plays, and the apps playing. Scrolling
// over it changes the volume. Hidden without a sound server.
import { computed, onMounted, ref } from 'vue'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import { useSound } from '@/stores/sound'

const sound = useSound()
const open = ref(false)
onMounted(() => void sound.refresh())

const out = computed(() => sound.output)
const label = computed(() => {
  const o = out.value
  if (!o) return 'Sound'
  return o.muted ? `Volume: muted (${o.description})` : `Volume: ${o.volume}% (${o.description})`
})

function setVolume(volume: number) {
  if (out.value) sound.set({ output: out.value.name, volume, muted: volume === 0 ? out.value.muted : false })
}

function onWheel(e: WheelEvent) {
  if (!out.value) return
  e.preventDefault()
  setVolume(Math.max(0, Math.min(100, out.value.volume + (e.deltaY < 0 ? 5 : -5))))
}

const value = (e: Event) => Number((e.target as HTMLInputElement).value)
</script>

<template>
  <PopoverRoot v-if="sound.state.available && out" v-model:open="open">
    <PopoverTrigger class="applet sound" :class="{ active: open }" :title="label" :aria-label="label" @wheel="onWheel">
      <SymbolicIcon :name="out.muted || out.volume === 0 ? 'volume-muted' : 'volume'" :size="16" />
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent class="sound-menu" side="top" align="end" :side-offset="0">
        <section aria-label="Output volume">
          <div class="row">
            <button
              class="mute"
              :aria-pressed="out.muted"
              :title="out.muted ? 'Unmute' : 'Mute'"
              :aria-label="out.muted ? 'Unmute' : 'Mute'"
              @click="sound.set({ output: out.name, muted: !out.muted })"
            >
              <SymbolicIcon :name="out.muted ? 'volume-muted' : 'volume'" :size="16" />
            </button>
            <input
              class="slider"
              type="range"
              min="0"
              max="100"
              step="1"
              aria-label="Volume"
              :value="Math.min(out.volume, 100)"
              @input="setVolume(value($event))"
            />
            <span class="percent">{{ out.volume }}%</span>
          </div>
        </section>
        <section v-if="sound.state.outputs.length > 1" aria-label="Output">
          <h3>Output</h3>
          <ul class="list" role="radiogroup" aria-label="Output device">
            <li v-for="o in sound.state.outputs" :key="o.name">
              <button
                class="choice"
                role="radio"
                :aria-checked="o.default"
                @click="o.default || sound.set({ output: o.name, default: true })"
              >
                <SymbolicIcon v-if="o.default" name="check" :size="14" />
                <span v-else class="spacer" />
                <span>{{ o.description }}</span>
              </button>
            </li>
          </ul>
        </section>
        <section v-if="sound.state.apps.length" aria-label="Applications">
          <h3>Applications</h3>
          <div v-for="a in sound.state.apps" :key="a.id" class="row app">
            <button
              class="mute"
              :aria-pressed="a.muted"
              :title="a.muted ? `Unmute ${a.name}` : `Mute ${a.name}`"
              :aria-label="a.muted ? `Unmute ${a.name}` : `Mute ${a.name}`"
              @click="sound.set({ app: a.id, muted: !a.muted })"
            >
              <SymbolicIcon :name="a.muted ? 'volume-muted' : 'volume'" :size="14" />
            </button>
            <span class="app-name">{{ a.name }}</span>
            <input
              class="slider"
              type="range"
              min="0"
              max="100"
              :aria-label="`${a.name} volume`"
              :value="Math.min(a.volume, 100)"
              @input="sound.set({ app: a.id, volume: value($event) })"
            />
          </div>
        </section>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>

<style scoped>
.applet.sound {
  display: grid;
  place-items: center;
  height: 100%;
  padding: 0 8px;
  border: none;
  background: transparent;
  color: var(--panel-item-fg);
}
.applet.sound:hover,
.applet.sound.active {
  background: var(--panel-item-hover-bg);
  color: var(--panel-item-hover-fg);
}
</style>

<style>
/* Portaled content is outside this component's scope. */
.sound-menu {
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: 300px;
  padding: 10px;
  background: var(--menu-bg);
  color: var(--menu-fg);
  border-radius: var(--menu-radius) var(--menu-radius) 0 0;
  box-shadow: var(--menu-shadow);
  z-index: 100000;
  outline: none;
}
.sound-menu h3 {
  margin: 0 4px 4px;
  font-size: 1em;
  color: var(--menu-dim-fg);
}
.sound-menu .row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 2px 4px;
}
.sound-menu .slider {
  flex: 1;
  min-width: 0;
  accent-color: var(--accent);
}
.sound-menu .percent {
  width: 3.2em;
  text-align: right;
  color: var(--menu-dim-fg);
}
.sound-menu .mute,
.sound-menu .choice {
  display: flex;
  align-items: center;
  gap: 8px;
  border: none;
  border-radius: var(--menu-radius);
  background: transparent;
  color: inherit;
}
.sound-menu .mute {
  padding: 4px;
}
.sound-menu .mute:hover,
.sound-menu .choice:hover {
  background: var(--menu-item-hover-bg);
  color: var(--menu-item-hover-fg);
}
.sound-menu .list {
  margin: 0;
  padding: 4px;
  list-style: none;
  border-radius: var(--menu-radius);
  background: var(--menu-box-bg);
}
.sound-menu .choice {
  width: 100%;
  padding: 6px 8px;
  text-align: left;
}
.sound-menu .spacer {
  width: 14px;
}
.sound-menu .app-name {
  width: 7em;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
</style>
