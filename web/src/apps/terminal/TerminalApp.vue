<script setup lang="ts">
// Terminal: xterm.js attached to a PTY that lives in the worker. The window
// remembers its terminal id, so a page reload reattaches to the same shell
// and the worker replays recent output (decision 4c). Closing the window
// hangs up the shell; reloading the page does not.
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { api, ApiError, wsUrl } from '@/api/client'
import type { TerminalInfo } from '@/api/types'
import type { AppContext } from '@/apps/types'
import { useWindows } from '@/stores/windows'

const props = defineProps<{ ctx: AppContext }>()
const wm = useWindows()

const host = ref<HTMLElement | null>(null)
const status = ref<'connecting' | 'connected' | 'reconnecting' | 'closed'>('connecting')

let term: Terminal | undefined
let fit: FitAddon | undefined
let ws: WebSocket | undefined
let ro: ResizeObserver | undefined
let terminalId: string | undefined
let disposed = false
let retries = 0
let fitTimer: ReturnType<typeof setTimeout> | undefined
const encoder = new TextEncoder()

function cssVar(name: string, fallback: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback
}

async function ensureTerminal(): Promise<string> {
  const saved = props.ctx.args.terminalId
  if (typeof saved === 'string') {
    const { terminals } = await api.get<{ terminals: TerminalInfo[] }>('/api/v1/terminals')
    if (terminals.some((t) => t.id === saved && !t.exited)) return saved
  }
  const cwd = typeof props.ctx.args.cwd === 'string' ? props.ctx.args.cwd : undefined
  const t = await api.post<TerminalInfo>('/api/v1/terminals', { cols: term?.cols ?? 80, rows: term?.rows ?? 24, cwd })
  props.ctx.saveState({ terminalId: t.id })
  return t.id
}

function sendResize() {
  if (ws?.readyState === WebSocket.OPEN && term) {
    ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
  }
}

function fitNow() {
  if (!host.value || !fit || !term) return
  if (host.value.clientWidth === 0 || host.value.clientHeight === 0) return // minimized
  fit.fit()
}

function scheduleFit() {
  clearTimeout(fitTimer)
  fitTimer = setTimeout(fitNow, 30)
}

function connect() {
  if (disposed || !terminalId || !term) return
  const socket = new WebSocket(wsUrl(`/api/v1/terminals/${terminalId}/attach`))
  socket.binaryType = 'arraybuffer'
  ws = socket
  socket.onopen = () => {
    retries = 0
    status.value = 'connected'
    // The worker replays its buffer from the start on every attach.
    term?.reset()
    sendResize()
  }
  socket.onmessage = (e) => {
    if (e.data instanceof ArrayBuffer) term?.write(new Uint8Array(e.data))
  }
  socket.onclose = (e) => {
    if (disposed || ws !== socket) return
    if (e.reason.startsWith('exited')) {
      // The shell ended (exit, Ctrl+D): close the window like a desktop
      // terminal does.
      status.value = 'closed'
      props.ctx.close()
      return
    }
    status.value = 'reconnecting'
    const delay = Math.min(10_000, 500 * 2 ** retries++)
    setTimeout(() => void reconnect(), delay)
  }
}

async function reconnect() {
  if (disposed) return
  try {
    // A 401 here signals the session store, which returns to login.
    const { terminals } = await api.get<{ terminals: TerminalInfo[] }>('/api/v1/terminals')
    if (!terminals.some((t) => t.id === terminalId)) {
      status.value = 'closed'
      term?.write('\r\n\x1b[2m[This terminal has ended]\x1b[0m\r\n')
      return
    }
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) return
  }
  connect()
}

onMounted(async () => {
  if (!host.value) return
  term = new Terminal({
    fontFamily: cssVar('--font-mono', 'monospace'),
    fontSize: 14,
    cursorBlink: true,
    scrollback: 10_000,
    allowProposedApi: false,
    theme: {
      background: cssVar('--terminal-bg', '#222226'),
      foreground: cssVar('--terminal-fg', '#ffffff'),
      cursor: cssVar('--terminal-fg', '#ffffff'),
      selectionBackground: 'rgba(53, 168, 84, 0.45)',
      // Tango, the GNOME Terminal default palette.
      black: '#2e3436',
      red: '#cc0000',
      green: '#4e9a06',
      yellow: '#c4a000',
      blue: '#3465a4',
      magenta: '#75507b',
      cyan: '#06989a',
      white: '#d3d7cf',
      brightBlack: '#555753',
      brightRed: '#ef2929',
      brightGreen: '#8ae234',
      brightYellow: '#fce94f',
      brightBlue: '#729fcf',
      brightMagenta: '#ad7fa8',
      brightCyan: '#34e2e2',
      brightWhite: '#eeeeec',
    },
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(host.value)
  fitNow()
  // A newly opened window is already focused, so the watcher below never
  // fires for it; take keyboard focus now (otherwise keys go to whatever
  // launched us, such as a panel button).
  if (props.ctx.isFocused()) term.focus()

  term.onData((data) => {
    if (ws?.readyState === WebSocket.OPEN) ws.send(encoder.encode(data))
  })
  term.onBinary((data) => {
    if (ws?.readyState !== WebSocket.OPEN) return
    const bytes = new Uint8Array(data.length)
    for (let i = 0; i < data.length; i++) bytes[i] = data.charCodeAt(i) & 0xff
    ws.send(bytes)
  })
  term.onResize(sendResize)
  term.onTitleChange((title) => props.ctx.setTitle(title || 'Terminal'))

  ro = new ResizeObserver(() => {
    if (!wm.interacting) scheduleFit()
  })
  ro.observe(host.value)

  props.ctx.onClose(() => {
    if (terminalId) void api.del(`/api/v1/terminals/${terminalId}`).catch(() => {})
  })

  try {
    terminalId = await ensureTerminal()
    connect()
  } catch (e) {
    status.value = 'closed'
    term.write(`\r\n\x1b[31mCould not start a terminal: ${(e as Error).message}\x1b[0m\r\n`)
  }
})

// Fit once a drag or resize ends rather than on every pointer move.
watch(
  () => wm.interacting,
  (busy) => {
    if (!busy) scheduleFit()
  },
)

// Keyboard focus follows window focus.
watch(
  () => wm.focusedId,
  (id) => {
    if (id === props.ctx.windowId) term?.focus()
  },
)

onBeforeUnmount(() => {
  disposed = true
  clearTimeout(fitTimer)
  ro?.disconnect()
  ws?.close()
  term?.dispose()
})
</script>

<template>
  <div class="terminal-app">
    <div ref="host" class="xterm-host" @pointerdown="term?.focus()" />
    <div v-if="status === 'reconnecting'" class="banner">Reconnecting…</div>
  </div>
</template>

<style scoped>
.terminal-app {
  position: absolute;
  inset: 0;
  background: var(--terminal-bg);
}
.xterm-host {
  position: absolute;
  inset: 4px 0 0 6px;
}
.xterm-host :deep(.xterm) {
  height: 100%;
}
.xterm-host :deep(.xterm-viewport) {
  background-color: var(--terminal-bg) !important;
}
.banner {
  position: absolute;
  top: 8px;
  right: 12px;
  padding: 4px 10px;
  border-radius: var(--radius);
  background: var(--warning);
  color: #000;
  font-size: var(--font-size-small);
}
</style>
