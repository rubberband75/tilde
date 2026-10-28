<script setup lang="ts">
// The media player, after Celluloid: one <video> element plays sound and
// video files from the worker's fs/content (Range requests let it seek),
// with the folder as the playlist. The position is saved with the window,
// so a lock or a reload (which clear the page, D40) comes back where it
// was, paused. Media keys work through the Media Session API.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { ApiError } from '@/api/client'
import { baseName, fsApi } from '@/api/fs'
import type { Entry } from '@/api/types'
import type { AppContext } from '@/apps/types'
import SymbolicIcon from '@/components/SymbolicIcon.vue'
import ThemeIcon from '@/components/ThemeIcon.vue'
import { iconsForEntry } from '@/icons/theme'
import { neighbour, useFolderSiblings } from '@/lib/folderSiblings'
import { formatTime, isMedia } from './media'

const props = defineProps<{ ctx: AppContext }>()
const ctx = props.ctx

const VOLUME_KEY = 'tilde:media:volume'
/** How often the position is saved while playing, in seconds of playback. */
const SAVE_EVERY = 5
const IDLE_MS = 2500

function storedVolume(): number {
  try {
    const v = Number(localStorage.getItem(VOLUME_KEY))
    return localStorage.getItem(VOLUME_KEY) !== null && v >= 0 && v <= 1 ? v : 1
  } catch {
    return 1
  }
}

const path = ref(typeof ctx.args.path === 'string' ? ctx.args.path : '')
const entry = shallowRef<Entry | null>(null)
const media = ref<HTMLVideoElement | null>(null)
const root = ref<HTMLElement | null>(null)
const playing = ref(false)
const time = ref(0)
const duration = ref(0)
const volume = ref(storedVolume())
const muted = ref(false)
const audioOnly = ref(false)
const waiting = ref(false)
const error = ref('')
const showPlaylist = ref(ctx.args.playlist === true)
const fullscreen = ref(false)
const idle = ref(false)

// A window opened from Files plays at once; one restored after a reload or
// a lock waits, paused where it was.
let autoplay = typeof ctx.args.time !== 'number'
let pendingSeek = typeof ctx.args.time === 'number' ? ctx.args.time : 0
let savedAt = 0

const { items: playlist, folder, reload } = useFolderSiblings(path, isMedia)

const name = computed(() => (path.value ? baseName(path.value) : ''))
watch(name, (n) => ctx.setTitle(n ? `${n} — Media Player` : 'Media Player'), { immediate: true })
const src = computed(() => (entry.value ? fsApi.contentUrl(entry.value.path, false, entry.value.mtime) : ''))
const index = computed(() => playlist.value.findIndex((e) => e.path === path.value))
const hasNext = computed(() => index.value >= 0 && index.value < playlist.value.length - 1)

/** Opens p; play starts it once it can play, at (seconds) seeks first. */
async function open(p: string, play: boolean, at = 0) {
  path.value = p
  ctx.saveState({ path: p, time: at })
  savedAt = at
  error.value = ''
  audioOnly.value = false
  time.value = at
  duration.value = 0
  autoplay = play
  pendingSeek = at
  const listed = playlist.value.find((e) => e.path === p)
  if (listed) {
    entry.value = listed
    return
  }
  try {
    const e = await fsApi.stat(p)
    if (path.value === p) entry.value = e
  } catch (e) {
    if (path.value !== p) return
    entry.value = null
    error.value =
      e instanceof ApiError && e.code === 'not_found'
        ? `“${baseName(p)}” does not exist.`
        : `Could not open “${baseName(p)}”: ${(e as Error).message}`
  }
}

function savePosition() {
  if (!path.value) return
  savedAt = time.value
  ctx.saveState({ time: Math.round(time.value * 10) / 10 })
}

function onLoadedMetadata() {
  const m = media.value!
  duration.value = Number.isFinite(m.duration) ? m.duration : 0
  audioOnly.value = m.videoWidth === 0
  m.volume = volume.value
  m.muted = muted.value
  if (pendingSeek > 0 && duration.value) m.currentTime = Math.min(pendingSeek, Math.max(0, duration.value - 1))
  pendingSeek = 0
  if (autoplay) void play()
}

function onTimeUpdate() {
  time.value = media.value?.currentTime ?? 0
  if (Math.abs(time.value - savedAt) >= SAVE_EVERY) savePosition()
}

function onEnded() {
  playing.value = false
  const next = neighbour(playlist.value, path.value, 1, false)
  if (next) void open(next.path, true)
  else ctx.saveState({ time: 0 })
}

function onError() {
  const code = media.value?.error?.code
  playing.value = false
  waiting.value = false
  if (!entry.value) return
  error.value =
    code === MediaError.MEDIA_ERR_SRC_NOT_SUPPORTED
      ? `This browser cannot play “${name.value}”.`
      : code === MediaError.MEDIA_ERR_DECODE
        ? `“${name.value}” is damaged or uses a format this browser cannot decode.`
        : code === MediaError.MEDIA_ERR_NETWORK
          ? `Playing “${name.value}” stopped: the connection to your session failed.`
          : `Could not play “${name.value}”.`
}

function onVolumeChange() {
  const m = media.value!
  volume.value = m.volume
  muted.value = m.muted
  try {
    localStorage.setItem(VOLUME_KEY, String(m.volume))
  } catch {
    // A preference of this browser only.
  }
}

async function play() {
  try {
    await media.value?.play()
  } catch {
    // Blocked until the user interacts with the page, or no source; the
    // play button stays.
  }
}

function toggle() {
  const m = media.value
  if (!m || !entry.value || error.value) return
  if (m.paused) void play()
  else m.pause()
}

function seek(to: number) {
  const m = media.value
  if (!m || !duration.value) return
  m.currentTime = Math.min(Math.max(0, to), duration.value)
  time.value = m.currentTime
  savePosition()
}

function setVolume(v: number) {
  const m = media.value
  if (!m) return
  m.volume = Math.min(1, Math.max(0, v))
  m.muted = false
}

function toggleMute() {
  if (media.value) media.value.muted = !media.value.muted
}

/** Back to the start, or the previous file when near the start already. */
function previous() {
  const prev = neighbour(playlist.value, path.value, -1, false)
  if (time.value > 3 || !prev) seek(0)
  else void open(prev.path, true)
}

function next() {
  const n = neighbour(playlist.value, path.value, 1, false)
  if (n) void open(n.path, true)
}

function togglePlaylist() {
  showPlaylist.value = !showPlaylist.value
  ctx.saveState({ playlist: showPlaylist.value })
}

async function openFiles() {
  const chosen = await ctx.chooseFile({
    mode: 'open',
    title: 'Open Media',
    folder: folder.value || undefined,
    filters: [{ name: 'Audio and Video', mimeTypes: ['audio/*', 'video/*'] }, { name: 'All Files' }],
  })
  const p = chosen?.paths[0]
  if (p) void open(p, true)
}

async function toggleFullscreen() {
  try {
    // The desktop may be fullscreen underneath (D53): leave only this one.
    if (document.fullscreenElement === root.value) await document.exitFullscreen()
    else await root.value?.requestFullscreen()
  } catch {
    ctx.notify('This browser did not allow fullscreen.', 'error')
  }
}
function onFullscreenChange() {
  fullscreen.value = document.fullscreenElement === root.value
  poke()
  void nextTick(() => root.value?.focus())
}

// In fullscreen the controls and the pointer hide while a video plays.
let idleTimer: ReturnType<typeof setTimeout> | undefined
function poke() {
  idle.value = false
  clearTimeout(idleTimer)
  if (fullscreen.value && playing.value && !audioOnly.value) {
    idleTimer = setTimeout(() => (idle.value = true), IDLE_MS)
  }
}
watch(playing, poke)

function onKey(e: KeyboardEvent) {
  const ctrl = e.ctrlKey || e.metaKey
  const key = e.key
  poke()
  let handled = true
  if (ctrl && key.toLowerCase() === 'o') void openFiles()
  else if (ctrl || e.altKey) handled = false
  else if (key === ' ' || key === 'k') toggle()
  else if (key === 'ArrowLeft') seek(time.value - (e.shiftKey ? 60 : 5))
  else if (key === 'ArrowRight') seek(time.value + (e.shiftKey ? 60 : 5))
  else if (key === 'ArrowUp') setVolume(volume.value + 0.05)
  else if (key === 'ArrowDown') setVolume(volume.value - 0.05)
  else if (key === 'Home') seek(0)
  else if (key === 'm') toggleMute()
  else if (key === 'f' || key === 'F11') void toggleFullscreen()
  else if (key === 'PageUp' || key === 'p') previous()
  else if (key === 'PageDown' || key === 'n') next()
  else handled = false
  if (handled) e.preventDefault()
}

// Media keys and the browser's media controls drive the window that played last.
let ownsSession = false
function claimMediaSession() {
  if (!('mediaSession' in navigator) || !entry.value) return
  const session = navigator.mediaSession
  ownsSession = true
  session.metadata = new MediaMetadata({ title: entry.value.name, album: baseName(folder.value) })
  session.setActionHandler('play', () => void play())
  session.setActionHandler('pause', () => media.value?.pause())
  session.setActionHandler('previoustrack', previous)
  session.setActionHandler('nexttrack', hasNext.value ? next : null)
  session.setActionHandler('seekto', (d) => d.seekTime !== undefined && seek(d.seekTime))
}
function releaseMediaSession() {
  if (!ownsSession || !('mediaSession' in navigator)) return
  const session = navigator.mediaSession
  session.metadata = null
  for (const a of ['play', 'pause', 'previoustrack', 'nexttrack', 'seekto'] as const) session.setActionHandler(a, null)
  ownsSession = false
}

function onPlay() {
  playing.value = true
  error.value = ''
  claimMediaSession()
}

function onPause() {
  playing.value = false
  savePosition()
}

onMounted(() => {
  document.addEventListener('fullscreenchange', onFullscreenChange)
  root.value?.focus()
  if (path.value) {
    void open(path.value, autoplay, pendingSeek)
    void reload()
  }
})

onBeforeUnmount(() => {
  // A reload or a lock: keep the exact position.
  if (media.value && entry.value) savePosition()
  clearTimeout(idleTimer)
  releaseMediaSession()
  document.removeEventListener('fullscreenchange', onFullscreenChange)
  if (document.fullscreenElement === root.value) void document.exitFullscreen().catch(() => {})
})
</script>

<template>
  <div ref="root" class="player" :class="{ fullscreen, idle }" tabindex="-1" @keydown="onKey" @pointermove="poke">
    <div class="main">
      <div class="screen" @click="toggle" @dblclick="toggleFullscreen">
        <video
          v-show="!audioOnly && !error"
          ref="media"
          :src="src || undefined"
          preload="metadata"
          playsinline
          @loadedmetadata="onLoadedMetadata"
          @timeupdate="onTimeUpdate"
          @durationchange="duration = Number.isFinite(media!.duration) ? media!.duration : 0"
          @play="onPlay"
          @pause="onPause"
          @ended="onEnded"
          @error="onError"
          @waiting="waiting = true"
          @playing="waiting = false"
          @canplay="waiting = false"
          @volumechange="onVolumeChange"
        />
        <div v-if="error" class="message" role="status">{{ error }}</div>
        <div v-else-if="audioOnly && entry" class="art">
          <ThemeIcon :name="iconsForEntry(entry)" :size="128" />
          <span class="track">{{ entry.name }}</span>
        </div>
        <div v-else-if="!path" class="message empty">
          <ThemeIcon name="multimedia-video-player" :size="64" />
          <button class="btn suggested" @click.stop="openFiles">Open a File…</button>
        </div>
      </div>

      <aside v-if="showPlaylist" class="playlist" aria-label="Playlist">
        <button
          v-for="(e, i) in playlist"
          :key="e.path"
          class="track-row"
          :class="{ current: i === index }"
          :aria-current="i === index ? 'true' : undefined"
          @click="open(e.path, true)"
        >
          <ThemeIcon :name="iconsForEntry(e)" :size="16" />
          <span>{{ e.name }}</span>
        </button>
        <p v-if="!playlist.length" class="dim">No other files here.</p>
      </aside>
    </div>

    <footer class="controls">
      <button class="btn icon-only flat" title="Open (Ctrl+O)" aria-label="Open" @click="openFiles">
        <ThemeIcon name="folder-open" :size="16" />
      </button>
      <button
        class="btn icon-only flat"
        title="Previous (PageUp)"
        aria-label="Previous"
        :disabled="!entry"
        @click="previous"
      >
        <SymbolicIcon name="previous" />
      </button>
      <button
        class="btn icon-only play"
        :title="playing ? 'Pause (Space)' : 'Play (Space)'"
        :aria-label="playing ? 'Pause' : 'Play'"
        :disabled="!entry || !!error"
        @click="toggle"
      >
        <SymbolicIcon :name="playing ? 'pause' : 'play'" />
      </button>
      <button class="btn icon-only flat" title="Next (PageDown)" aria-label="Next" :disabled="!hasNext" @click="next">
        <SymbolicIcon name="next" />
      </button>
      <span class="time" aria-label="Position">{{ formatTime(time) }}</span>
      <input
        class="seek"
        type="range"
        min="0"
        :max="duration || 1"
        step="0.1"
        :value="time"
        :disabled="!duration"
        aria-label="Seek"
        :aria-valuetext="`${formatTime(time)} of ${formatTime(duration)}`"
        @input="seek(Number(($event.target as HTMLInputElement).value))"
      />
      <span class="time" aria-label="Duration">{{ formatTime(duration) }}</span>
      <button
        class="btn icon-only flat"
        :title="muted ? 'Unmute (M)' : 'Mute (M)'"
        :aria-label="muted ? 'Unmute' : 'Mute'"
        @click="toggleMute"
      >
        <SymbolicIcon :name="muted || volume === 0 ? 'volume-muted' : 'volume'" />
      </button>
      <input
        class="volume"
        type="range"
        min="0"
        max="1"
        step="0.01"
        :value="muted ? 0 : volume"
        aria-label="Volume"
        @input="setVolume(Number(($event.target as HTMLInputElement).value))"
      />
      <button
        class="btn icon-only flat"
        :class="{ on: showPlaylist }"
        title="Playlist"
        aria-label="Playlist"
        :aria-pressed="showPlaylist"
        @click="togglePlaylist"
      >
        <SymbolicIcon name="list" />
      </button>
      <button class="btn icon-only flat" title="Fullscreen (F)" aria-label="Fullscreen" @click="toggleFullscreen">
        <SymbolicIcon name="fullscreen" />
      </button>
    </footer>
    <div v-if="waiting && playing" class="spinner" aria-label="Loading" />
  </div>
</template>

<style scoped>
.player {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--window-bg);
  color: var(--window-fg);
  outline: none;
}
.main {
  flex: 1;
  display: flex;
  min-height: 0;
}
.screen {
  position: relative;
  flex: 1;
  min-width: 0;
  display: grid;
  place-items: center;
  background: var(--media-screen-bg);
  color: var(--media-screen-fg);
  overflow: hidden;
}
.screen video {
  width: 100%;
  height: 100%;
  object-fit: contain;
}
.art {
  position: absolute;
  inset: 0;
  display: grid;
  place-content: center;
  justify-items: center;
  gap: 16px;
  padding: 24px;
  text-align: center;
}
.track {
  font-size: 1.2em;
  font-weight: bold;
  word-break: break-word;
}
.message {
  position: absolute;
  inset: 0;
  display: grid;
  place-content: center;
  padding: 24px;
  text-align: center;
}
.message.empty {
  justify-items: center;
  gap: 16px;
}
.playlist {
  width: 240px;
  flex: none;
  overflow-y: auto;
  padding: 4px 0;
  background: var(--sidebar-bg);
  color: var(--sidebar-fg);
  border-left: 1px solid var(--border);
}
.playlist p {
  padding: 8px 12px;
}
.track-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 5px 12px;
  border: none;
  background: transparent;
  color: inherit;
  text-align: left;
}
.track-row span {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.track-row:hover {
  background: var(--hover-bg);
}
.track-row.current {
  background: var(--selection-bg);
  color: var(--selection-fg);
}
.controls {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 8px;
  background: var(--headerbar-bg);
  border-top: 1px solid var(--border);
}
.controls .btn.on {
  background: var(--button-active-bg);
  box-shadow: inset 0 0 0 1px var(--accent);
}
.time {
  min-width: 44px;
  text-align: center;
  font-variant-numeric: tabular-nums;
  color: var(--view-dim-fg);
}
.seek {
  flex: 1;
  min-width: 60px;
  accent-color: var(--accent);
}
.volume {
  width: 80px;
  accent-color: var(--accent);
}
.spinner {
  position: absolute;
  left: 50%;
  top: 40%;
  width: 40px;
  height: 40px;
  margin: -20px 0 0 -20px;
  border: 3px solid var(--media-screen-fg);
  border-top-color: transparent;
  border-radius: 50%;
  animation: spin 0.9s linear infinite;
  pointer-events: none;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
.player.fullscreen .controls {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  background: var(--media-osd-bg);
  border-top: none;
  transition: opacity 0.3s;
}
.player.fullscreen .playlist {
  display: none;
}
.player.idle .controls {
  opacity: 0;
}
.player.idle,
.player.idle .screen {
  cursor: none;
}
</style>
