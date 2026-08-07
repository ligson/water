<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { message } from 'ant-design-vue'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal as XTerminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { Copy, KeyRound, Play, RefreshCw, Square, Terminal as TerminalIcon } from '@lucide/vue'
import { api, terminalWebSocketURL, type TerminalSession } from '../../api'

type TerminalState = 'idle' | 'connecting' | 'connected' | 'closed' | 'error'

type TerminalSocketMessage = {
  type: string
  sessionId?: string
  payload?: Record<string, unknown>
}

const props = defineProps<{
  workspaceId: string
  workspaceName?: string
  active?: boolean
}>()

const connectionState = ref<TerminalState>('idle')
const terminalError = ref('')
const sessionCwd = ref('')
const currentSession = ref<TerminalSession | null>(null)
const terminalElement = ref<HTMLElement | null>(null)
const terminalCols = ref(100)
const terminalRows = ref(30)

let terminal: XTerminal | undefined
let fitAddon: FitAddon | undefined
let resizeObserver: ResizeObserver | undefined
let socket: WebSocket | undefined
let dataDisposable: { dispose: () => void } | undefined
let resizeDisposable: { dispose: () => void } | undefined

const workspaceLabel = computed(() => props.workspaceName || '当前工作区')
const sessionLabel = computed(() => currentSession.value?.id || '未创建会话')
const sessionDetails = computed(() => {
  const cwd = currentSession.value?.cwd?.trim() || sessionCwd.value.trim() || '工作区目录'
  return `目录：${cwd}`
})
const connectionText = computed(() => {
  if (!props.workspaceId) return '未选择工作区'
  if (connectionState.value === 'connecting') return '连接中'
  if (connectionState.value === 'connected') return '已连接'
  if (connectionState.value === 'error') return '连接异常'
  if (connectionState.value === 'closed') return '已断开'
  return '未连接'
})
const connectionColor = computed(() => {
  if (connectionState.value === 'connected') return 'success'
  if (connectionState.value === 'connecting') return 'processing'
  if (connectionState.value === 'error') return 'error'
  if (connectionState.value === 'closed') return 'warning'
  return 'default'
})
const canConnect = computed(
  () => Boolean(props.workspaceId) && connectionState.value !== 'connecting' && connectionState.value !== 'connected',
)

function initTerminal() {
  if (terminal || !terminalElement.value) return
  terminal = new XTerminal({
    convertEol: true,
    cursorBlink: true,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
    fontSize: 12,
    lineHeight: 1.24,
    scrollback: 5000,
    theme: {
      background: '#0b1110',
      foreground: '#dbece7',
      cursor: '#8dd8cf',
      selectionBackground: '#284f49',
      black: '#0b1110',
      blue: '#6aa5ff',
      cyan: '#7dd3c7',
      green: '#86efac',
      magenta: '#c084fc',
      red: '#f87171',
      white: '#e5e7eb',
      yellow: '#fde68a',
    },
  })
  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.open(terminalElement.value)
  dataDisposable = terminal.onData((data) => {
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: 'terminal.input', payload: { data } }))
    }
  })
  resizeDisposable = terminal.onResize((size) => {
    terminalCols.value = size.cols
    terminalRows.value = size.rows
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: 'terminal.resize', payload: { cols: size.cols, rows: size.rows } }))
    }
  })
  resizeObserver = new ResizeObserver(() => fitTerminal())
  resizeObserver.observe(terminalElement.value)
  terminal.writeln('后端服务器终端已就绪。')
  terminal.writeln('连接后即可直接操作当前部署机器。')
  fitTerminal()
}

function fitTerminal() {
  void nextTick(() => {
    window.requestAnimationFrame(() => {
      if (!fitAddon || !terminal || !terminalElement.value || terminalElement.value.clientWidth <= 0) return
      try {
        fitAddon.fit()
        terminalCols.value = terminal.cols
        terminalRows.value = terminal.rows
      } catch {
        // tab hidden while fitting
      }
    })
  })
}

function focusTerminal() {
  terminal?.focus()
}

function writeTerminalLine(text: string, ansiColor = '\x1b[90m') {
  terminal?.writeln(`${ansiColor}${text}\x1b[0m`)
}

function closeSocketOnly() {
  if (!socket) return
  socket.onopen = null
  socket.onclose = null
  socket.onerror = null
  socket.onmessage = null
  socket.close()
  socket = undefined
}

async function connectTerminal() {
  if (!canConnect.value) return
  terminalError.value = ''
  connectionState.value = 'connecting'
  terminal?.clear()
  writeTerminalLine('连接后端服务器终端...')
  try {
    const session = await api.createTerminalSession({
      workspaceId: props.workspaceId,
      cwd: sessionCwd.value.trim(),
      cols: terminalCols.value,
      rows: terminalRows.value,
    })
    currentSession.value = session
    openTerminalSocket(session.id)
  } catch (err) {
    connectionState.value = 'error'
    terminalError.value = errorText(err)
    writeTerminalLine(`连接失败: ${terminalError.value}`, '\x1b[31m')
    showError(err)
  }
}

function openTerminalSocket(sessionId: string) {
  closeSocketOnly()
  const nextSocket = new WebSocket(terminalWebSocketURL(sessionId))
  socket = nextSocket
  nextSocket.onopen = () => {
    if (socket !== nextSocket) return
    focusTerminal()
  }
  nextSocket.onmessage = (raw) => {
    if (socket !== nextSocket) return
    handleTerminalSocketMessage(JSON.parse(raw.data) as TerminalSocketMessage)
  }
  nextSocket.onerror = () => {
    if (socket !== nextSocket) return
    connectionState.value = 'error'
    terminalError.value = 'WebSocket 连接异常'
    writeTerminalLine(terminalError.value, '\x1b[31m')
  }
  nextSocket.onclose = () => {
    if (socket !== nextSocket) return
    socket = undefined
    if (connectionState.value === 'connecting' || connectionState.value === 'connected') {
      connectionState.value = 'closed'
      writeTerminalLine('终端连接已断开。')
    }
  }
}

function handleTerminalSocketMessage(item: TerminalSocketMessage) {
  const payload = item.payload ?? {}
  if (item.type === 'terminal.ready') {
    connectionState.value = 'connected'
    const label = String(payload.label ?? '后端服务器')
    const cwd = String(payload.cwd ?? '')
    writeTerminalLine(`已连接 ${label}${cwd ? ` · ${cwd}` : ''}`, '\x1b[32m')
    return
  }
  if (item.type === 'terminal.output' || item.type === 'terminal.replay') {
    terminal?.write(String(payload.chunk ?? payload.output ?? ''))
    return
  }
  if (item.type === 'terminal.error') {
    connectionState.value = 'error'
    terminalError.value = String(payload.message ?? '终端异常')
    writeTerminalLine(terminalError.value, '\x1b[31m')
    return
  }
  if (item.type === 'terminal.exit') {
    writeTerminalLine('远端 shell 已退出。')
    return
  }
  if (item.type === 'terminal.closed') {
    connectionState.value = 'closed'
    currentSession.value = null
    closeSocketOnly()
    writeTerminalLine('终端会话已关闭。')
  }
}

async function disconnectTerminal() {
  const sessionId = currentSession.value?.id
  if (socket?.readyState === WebSocket.OPEN) {
    socket.send(JSON.stringify({ type: 'terminal.close', payload: {} }))
  }
  closeSocketOnly()
  currentSession.value = null
  connectionState.value = 'idle'
  writeTerminalLine('已关闭终端会话。')
  if (!sessionId) return
  try {
    await api.closeTerminalSession(sessionId)
  } catch {
    // websocket handler may already close the session
  }
}

async function copyTerminalSelection() {
  const selection = terminal?.getSelection().trim() ?? ''
  if (!selection) {
    message.info('先选中终端内容')
    return
  }
  try {
    await navigator.clipboard.writeText(selection)
    message.success('终端内容已复制')
  } catch {
    message.error('复制失败')
  }
}

function clearTerminal() {
  terminal?.clear()
  focusTerminal()
}

function errorText(err: unknown) {
  return err instanceof Error ? err.message : '操作失败'
}

function showError(err: unknown) {
  message.error(errorText(err))
}

watch(
  () => props.workspaceId,
  () => {
    void disconnectTerminal()
    sessionCwd.value = ''
    terminalError.value = ''
    currentSession.value = null
    connectionState.value = 'idle'
  },
)

watch(
  () => props.active,
  (active) => {
    if (!active) return
    fitTerminal()
    focusTerminal()
  },
)

onMounted(() => {
  initTerminal()
})

onBeforeUnmount(() => {
  void disconnectTerminal()
  resizeObserver?.disconnect()
  dataDisposable?.dispose()
  resizeDisposable?.dispose()
  terminal?.dispose()
})
</script>

<template>
  <section class="terminal-panel">
    <div class="terminal-panel-header">
      <div class="terminal-panel-title">
        <TerminalIcon :size="16" />
        <div>
          <strong>后端服务器终端</strong>
          <small>{{ workspaceLabel }}</small>
        </div>
      </div>
      <a-space :size="6">
        <a-tag :color="connectionColor">{{ connectionText }}</a-tag>
        <a-tag v-if="currentSession" class="terminal-session-pill">{{ sessionLabel }}</a-tag>
      </a-space>
    </div>

    <div class="terminal-session-meta">
      <span>{{ sessionDetails }}</span>
      <span>留空则默认使用工作区目录</span>
    </div>

    <div class="terminal-session-row">
      <a-input
        size="small"
        v-model:value="sessionCwd"
        :disabled="connectionState === 'connecting' || connectionState === 'connected'"
        placeholder="会话目录，留空则使用工作区目录"
      />
      <a-space :size="6">
        <a-button size="small" type="primary" :disabled="!canConnect" :loading="connectionState === 'connecting'" @click="connectTerminal">
          <template #icon><Play :size="13" /></template>
          连接
        </a-button>
        <a-button
          size="small"
          :disabled="connectionState !== 'connected' && connectionState !== 'connecting'"
          danger
          @click="disconnectTerminal"
        >
          <template #icon><Square :size="13" /></template>
        </a-button>
      </a-space>
    </div>

    <div class="terminal-tools">
      <a-space :size="6">
        <a-button size="small" title="复制选中内容" aria-label="复制选中内容" @click="copyTerminalSelection">
          <template #icon><Copy :size="13" /></template>
        </a-button>
        <a-button size="small" title="清空终端" aria-label="清空终端" @click="clearTerminal">
          清空
        </a-button>
        <a-button size="small" title="重新适配尺寸" aria-label="重新适配尺寸" @click="fitTerminal">
          <template #icon><RefreshCw :size="13" /></template>
        </a-button>
      </a-space>
      <span>{{ terminalCols }} x {{ terminalRows }}</span>
    </div>

    <div class="terminal-screen-shell" :class="{ connected: connectionState === 'connected', error: connectionState === 'error' }" @click="focusTerminal">
      <div ref="terminalElement" class="terminal-screen"></div>
      <div v-if="!currentSession && connectionState !== 'connecting'" class="terminal-overlay">
        <KeyRound :size="14" />
        <span>连接后在这里直接操作后端服务器</span>
      </div>
      <div v-if="terminalError" class="terminal-error">
        <KeyRound :size="13" />
        <span>{{ terminalError }}</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.terminal-panel {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  overflow: hidden;
  padding-top: 2px;
}

.terminal-panel-header,
.terminal-tools,
.terminal-session-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.terminal-panel-title {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 7px;
}

.terminal-panel-title div {
  min-width: 0;
  display: grid;
  gap: 1px;
}

.terminal-panel-title strong {
  min-width: 0;
  overflow: hidden;
  color: var(--ink);
  font-size: 13px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.terminal-panel-title small,
.terminal-session-meta span,
.terminal-tools span {
  color: var(--muted);
  font-size: 11px;
}

.terminal-session-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 0 2px;
  line-height: 1.3;
}

.terminal-session-row {
  align-items: stretch;
  gap: 8px;
}

.terminal-tools {
  min-height: 24px;
}

.terminal-screen-shell {
  position: relative;
  flex: 1 1 auto;
  min-height: 240px;
  overflow: hidden;
  border: 1px solid color-mix(in srgb, var(--line) 84%, #0b1110);
  border-radius: 8px;
  background: #0b1110;
}

.terminal-screen-shell.connected {
  border-color: color-mix(in srgb, var(--brand) 52%, #0b1110);
}

.terminal-screen-shell.error {
  border-color: color-mix(in srgb, var(--status-danger) 64%, #0b1110);
}

.terminal-screen {
  width: 100%;
  height: 100%;
  padding: 8px;
}

.terminal-screen :deep(.xterm) {
  height: 100%;
}

.terminal-overlay {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  gap: 6px;
  color: color-mix(in srgb, var(--muted) 82%, white);
  background: linear-gradient(180deg, rgba(11, 17, 16, 0.12), rgba(11, 17, 16, 0.26));
  font-size: 12px;
  pointer-events: none;
}

.terminal-error {
  position: absolute;
  right: 8px;
  bottom: 8px;
  max-width: calc(100% - 16px);
  display: flex;
  align-items: center;
  gap: 6px;
  border: 1px solid color-mix(in srgb, var(--status-danger) 45%, transparent);
  border-radius: 7px;
  padding: 6px 8px;
  color: var(--status-danger);
  background: color-mix(in srgb, var(--surface) 94%, transparent);
  font-size: 12px;
  box-shadow: 0 8px 18px color-mix(in srgb, #0b1110 18%, transparent);
}

.terminal-error span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.terminal-session-pill {
  max-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
