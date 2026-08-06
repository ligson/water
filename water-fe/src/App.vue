<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { message } from 'ant-design-vue'
import {
  CheckCircle,
  Code2,
  Copy,
  Eye,
  FolderPlus,
  GripVertical,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  Pencil,
  Plus,
  RefreshCw,
  Send,
  Square,
  Trash2,
} from '@lucide/vue'
import MarkdownIt from 'markdown-it'
import {
  api,
  taskWebSocketURL,
  type Approval,
  type ExternalPath,
  type Provider,
  type Task,
  type TaskEvent,
  type Workspace,
} from './api'

type ChatBlock = {
  key: string
  role: 'user' | 'assistant' | 'system'
  title: string
  content: string
  sequence: number
  turnId?: string
}

type ChatTimelineItem = {
  key: string
  kind: 'message' | 'execution'
  block?: ChatBlock
  group?: ExecutionGroup
}

type PanelSide = 'left' | 'right'

type ThemeName = 'ruoshui' | 'qingci' | 'xuanzhi' | 'zhusha' | 'xuanmo'

type ThemeOption = {
  name: ThemeName
  label: string
  subtitle: string
  primary: string
  swatches: string[]
}

type ExecutionStep = {
  key: string
  type: string
  title: string
  detail: string
  tone: 'normal' | 'running' | 'success' | 'warning' | 'danger'
  sequence: number
}

type ExecutionGroup = {
  key: string
  title: string
  subtitle: string
  status: 'running' | 'completed' | 'failed' | 'waiting' | 'interrupted' | 'stopped' | 'idle'
  steps: ExecutionStep[]
  lastSequence: number
  startedAt?: string
  endedAt?: string
  contextUsage?: {
    estimatedTokens: number
    tokenBudget: number
    contextWindowTokens: number
    truncated: boolean
  }
}

const providers = ref<Provider[]>([])
const workspaces = ref<Workspace[]>([])
const tasks = ref<Task[]>([])
const events = ref<TaskEvent[]>([])
const approvals = ref<Approval[]>([])
const externalPaths = ref<ExternalPath[]>([])
const selectedWorkspaceId = ref('')
const selectedTaskId = ref('')
const nowTick = ref(Date.now())
const rightTab = ref('approvals')
const loading = ref(false)
const wsConnected = ref(false)
const taskModalOpen = ref(false)
const taskSubmitting = ref(false)
const editingTaskId = ref('')
const providerModalOpen = ref(false)
const providerSubmitting = ref(false)
const editingProviderId = ref('')
const workspaceModalOpen = ref(false)
const workspaceSubmitting = ref(false)
const editingWorkspaceId = ref('')
const leftPanelWidth = ref(272)
const rightPanelWidth = ref(340)
const leftPanelCollapsed = ref(false)
const rightPanelCollapsed = ref(true)
const executionOpenKeys = ref<string[]>([])
const settingsOpenKey = ref('appearance')
const productName = '若水'
const productTagline = '可驾驭的私有 AI 编程助手'
const themeStorageKey = 'water-ui-theme'
const themeOptions: ThemeOption[] = [
  {
    name: 'ruoshui',
    label: '若水',
    subtitle: '清润、克制、可控',
    primary: '#0f766e',
    swatches: ['#0b4f66', '#0f766e', '#e5f4f1'],
  },
  {
    name: 'qingci',
    label: '青瓷',
    subtitle: '温润釉色，静中有骨',
    primary: '#3f7f72',
    swatches: ['#2f5f62', '#86a99b', '#eef6f0'],
  },
  {
    name: 'xuanzhi',
    label: '宣纸',
    subtitle: '纸白墨黑，留白有度',
    primary: '#6f5d2f',
    swatches: ['#2c302b', '#6f5d2f', '#f4efe4'],
  },
  {
    name: 'zhusha',
    label: '朱砂',
    subtitle: '赤印定神，沉稳有力',
    primary: '#a34032',
    swatches: ['#6e2f2b', '#a34032', '#f6ece8'],
  },
  {
    name: 'xuanmo',
    label: '玄墨',
    subtitle: '深墨入夜，专注审慎',
    primary: '#9b8a58',
    swatches: ['#111817', '#9b8a58', '#254c49'],
  },
]
const selectedThemeName = ref<ThemeName>(loadThemeName())
let taskSocket: WebSocket | undefined
let taskSocketReconnectTimer: number | undefined
let taskSocketReconnectAttempts = 0
const taskSocketReconnectBaseMS = 800
const taskSocketReconnectMaxMS = 8000
let activePanelResize:
  | {
      side: PanelSide
      startX: number
      startWidth: number
    }
  | undefined
const assistantRenderedContent = reactive<Record<string, string>>({})
const assistantTargetContent = reactive<Record<string, string>>({})
const assistantRawMode = reactive<Record<string, boolean>>({})
const assistantTypingTimers = new Map<string, number>()
const assistantTypingDelayMS = 18
const markdown = new MarkdownIt({
  html: false,
  linkify: true,
  breaks: true,
})
markdown.validateLink = (url) => {
  const normalized = url.trim().toLowerCase()
  return !normalized.startsWith('javascript:') && !normalized.startsWith('vbscript:')
}
const defaultLinkOpen =
  markdown.renderer.rules.link_open ??
  ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options))
markdown.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  tokens[idx].attrSet('target', '_blank')
  tokens[idx].attrSet('rel', 'noopener noreferrer')
  return defaultLinkOpen(tokens, idx, options, env, self)
}

const providerForm = reactive({
  name: 'Local Model',
  baseUrl: 'http://localhost:11434/v1',
  model: 'qwen2.5-coder:7b',
  apiKey: '',
  isDefault: true,
  enabled: true,
  contextWindowTokens: 8192,
})

const workspaceForm = reactive({
  name: productName,
  rootPath: '',
  permissionMode: 'request_approval',
  defaultProviderId: '',
  trusted: true,
})

const externalPathForm = reactive({
  path: '',
  pathType: 'directory',
  accessMode: 'read',
})

const taskTitle = ref('')
const userInput = ref('')
const chatBodyRef = ref<HTMLElement | null>(null)

const selectedWorkspace = computed(() =>
  workspaces.value.find((item) => item.id === selectedWorkspaceId.value),
)
const selectedTask = computed(() => tasks.value.find((item) => item.id === selectedTaskId.value))
const defaultProvider = computed(() => providers.value.find((item) => item.isDefault))
const activeProvider = computed(() => {
  const workspaceProviderId = selectedWorkspace.value?.defaultProviderId
  return providers.value.find((item) => item.id === workspaceProviderId) ?? defaultProvider.value
})
const activeProviderId = computed(() => activeProvider.value?.id ?? '')
const statusText = computed(() => {
  if (!selectedWorkspace.value) return '未选择工作区'
  if (!selectedTask.value) return '选择或创建一个任务'
  return latestTaskStatusText(events.value)
})
const taskHeaderTone = computed(() => {
  if (statusText.value.includes('失败')) return 'error'
  if (statusText.value.includes('完成')) return 'success'
  if (statusText.value.includes('审批')) return 'warning'
  if (statusText.value.includes('执行中') || statusText.value.includes('思考')) return 'processing'
  return 'default'
})
const latestExecutionGroup = computed(() =>
  [...executionGroups.value].sort((a, b) => b.lastSequence - a.lastSequence)[0],
)
const latestTurnDurationText = computed(() =>
  latestExecutionGroup.value ? executionDurationText(latestExecutionGroup.value) : '',
)

const chatBlocks = computed(() => {
  const blocks: ChatBlock[] = []
  const assistantIndexByTurn = new Map<string, number>()
  for (const item of events.value) {
    const payload = (item.payload ?? {}) as Record<string, unknown>
    const turnKey = item.turnId || `seq-${item.sequence}`
    if (item.type === 'turn.started') {
      blocks.push({
        key: item.eventId,
        role: 'user',
        title: '你',
        content: String(payload.userInput ?? `第 ${payload.sequence ?? item.sequence} 轮输入`),
        sequence: item.sequence,
        turnId: item.turnId,
      })
      continue
    }
    if (item.type === 'agent.message.delta') {
      let index = assistantIndexByTurn.get(turnKey)
      if (index === undefined) {
        index = blocks.length
        assistantIndexByTurn.set(turnKey, index)
        blocks.push({
          key: `assistant-${turnKey}`,
          role: 'assistant',
          title: productName,
          content: '',
          sequence: item.sequence,
          turnId: item.turnId,
        })
      }
      blocks[index].content += String(payload.delta ?? '')
      continue
    }
    if (item.type === 'turn.failed' || item.type === 'turn.interrupted') {
      blocks.push({
        key: item.eventId,
        role: 'system',
        title: item.type === 'turn.interrupted' ? '运行已中断' : '运行失败',
        content: String(payload.message ?? (item.type === 'turn.interrupted' ? 'Agent 执行已中断' : 'Agent 执行失败')),
        sequence: item.sequence,
        turnId: item.turnId,
      })
    }
  }
  return blocks.sort((a, b) => a.sequence - b.sequence)
})

const executionGroups = computed(() => {
  const groups = new Map<string, ExecutionGroup>()
  for (const item of events.value) {
    const key = item.turnId || 'task'
    let group = groups.get(key)
    if (!group) {
      group = {
        key,
        title: key === 'task' ? '任务事件' : '执行过程',
        subtitle: '',
        status: 'idle',
        steps: [],
        lastSequence: item.sequence,
      }
      groups.set(key, group)
    }

    group.lastSequence = Math.max(group.lastSequence, item.sequence)
    updateExecutionTiming(group, item)
    const step = executionStepFromEvent(item)
    if (step) {
      group.steps.push(step)
    }
    updateExecutionGroup(group, item)
  }

  const list = Array.from(groups.values()).filter(
    (group) => group.key !== 'task' && (group.steps.length > 0 || group.status !== 'idle'),
  )
  const latestSequence = Math.max(0, ...list.map((group) => group.lastSequence))
  for (const group of list) {
    if (group.status === 'running' && group.lastSequence < latestSequence) {
      group.status = 'stopped'
    }
  }
  return list
    .sort((a, b) => b.lastSequence - a.lastSequence)
})

const executionGroupByTurn = computed(() => {
  const groupMap = new Map<string, ExecutionGroup>()
  for (const group of executionGroups.value) {
    groupMap.set(group.key, group)
  }
  return groupMap
})

const chatTimeline = computed(() => {
  const items: ChatTimelineItem[] = []
  const renderedExecutionGroups = new Set<string>()
  for (const block of chatBlocks.value) {
    items.push({
      key: `message-${block.key}`,
      kind: 'message',
      block,
    })
    if (block.role !== 'user' || !block.turnId) continue
    const group = executionGroupByTurn.value.get(block.turnId)
    if (!group || renderedExecutionGroups.has(group.key)) continue
    renderedExecutionGroups.add(group.key)
    items.push({
      key: `execution-${group.key}`,
      kind: 'execution',
      group,
    })
  }
  return items
})

const runningExecutionKeys = computed(() =>
  executionGroups.value
    .filter((group) => group.status === 'running' || group.status === 'waiting')
    .map((group) => group.key),
)

const providerModalTitle = computed(() => (editingProviderId.value ? '编辑 Provider' : '新增 Provider'))
const workspaceModalTitle = computed(() => (editingWorkspaceId.value ? '编辑工作区' : '新增工作区'))
const taskModalTitle = computed(() => (editingTaskId.value ? '编辑任务' : '新增任务'))
const activeTheme = computed(
  () => themeOptions.find((item) => item.name === selectedThemeName.value) ?? themeOptions[0],
)
const antdTheme = computed(() => ({
  token: {
    colorPrimary: activeTheme.value.primary,
    borderRadius: 6,
    fontFamily: 'Inter, system-ui, sans-serif',
  },
}))
const shellStyle = computed(() => ({
  '--left-panel-width': `${leftPanelCollapsed.value ? 48 : leftPanelWidth.value}px`,
  '--right-panel-width': `${rightPanelCollapsed.value ? 48 : rightPanelWidth.value}px`,
}))

function loadThemeName(): ThemeName {
  if (typeof window === 'undefined') return 'ruoshui'
  const stored = window.localStorage.getItem(themeStorageKey)
  return themeOptions.some((item) => item.name === stored) ? (stored as ThemeName) : 'ruoshui'
}

function selectTheme(name: ThemeName) {
  selectedThemeName.value = name
}

function clampPanelWidth(value: number, side: PanelSide) {
  const min = side === 'left' ? 220 : 280
  const max = side === 'left' ? 420 : 520
  return Math.min(Math.max(value, min), max)
}

function startPanelResize(side: PanelSide, event: PointerEvent) {
  activePanelResize = {
    side,
    startX: event.clientX,
    startWidth: side === 'left' ? leftPanelWidth.value : rightPanelWidth.value,
  }
  window.addEventListener('pointermove', resizePanel)
  window.addEventListener('pointerup', stopPanelResize)
  window.addEventListener('pointercancel', stopPanelResize)
  document.body.classList.add('resizing-panel')
}

function resizePanel(event: PointerEvent) {
  if (!activePanelResize) return
  const delta = event.clientX - activePanelResize.startX
  if (activePanelResize.side === 'left') {
    leftPanelWidth.value = clampPanelWidth(activePanelResize.startWidth + delta, 'left')
    return
  }
  rightPanelWidth.value = clampPanelWidth(activePanelResize.startWidth - delta, 'right')
}

function stopPanelResize() {
  activePanelResize = undefined
  window.removeEventListener('pointermove', resizePanel)
  window.removeEventListener('pointerup', stopPanelResize)
  window.removeEventListener('pointercancel', stopPanelResize)
  document.body.classList.remove('resizing-panel')
}

function payloadRecord(item: TaskEvent): Record<string, unknown> {
  return ((item.payload ?? {}) as Record<string, unknown>) || {}
}

function payloadNumber(payload: Record<string, unknown>, key: string) {
  const value = payload[key]
  const numberValue = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(numberValue) ? numberValue : 0
}

function updateExecutionTiming(group: ExecutionGroup, item: TaskEvent) {
  if (item.type === 'turn.started') {
    group.startedAt = item.createdAt
  }
  if (item.type === 'turn.completed' || item.type === 'turn.failed' || item.type === 'turn.interrupted') {
    group.endedAt = item.createdAt
  }
}

function updateExecutionGroup(group: ExecutionGroup, item: TaskEvent) {
  const payload = payloadRecord(item)
  if (item.type === 'context.pack.built') {
    group.contextUsage = {
      estimatedTokens: payloadNumber(payload, 'estimatedTokens'),
      tokenBudget: payloadNumber(payload, 'tokenBudget'),
      contextWindowTokens: payloadNumber(payload, 'contextWindowTokens'),
      truncated: payload.truncated === true,
    }
    return
  }
  if (item.type === 'turn.started') {
    group.title = String(payload.userInput ?? '用户输入')
    group.subtitle = `第 ${payload.sequence ?? ''} 轮`
    group.status = 'running'
    return
  }
  if (item.type === 'agent.tool_calls.detected' || item.type === 'tool.completed') {
    group.status = 'running'
    return
  }
  if (item.type === 'approval.requested') {
    group.status = 'waiting'
    return
  }
  if (item.type === 'turn.completed') {
    group.status = 'completed'
    return
  }
  if (item.type === 'turn.failed') {
    group.status = 'failed'
    return
  }
  if (item.type === 'turn.interrupted') {
    group.status = 'interrupted'
  }
}

function executionStepFromEvent(item: TaskEvent): ExecutionStep | undefined {
  const payload = payloadRecord(item)
  switch (item.type) {
    case 'task.started':
      return makeExecutionStep(item, '任务创建', String(payload.title ?? '任务已创建'), 'normal')
    case 'context.pack.built':
    case 'turn.started':
    case 'agent.message.delta':
    case 'agent.message.completed':
    case 'turn.completed':
      return undefined
    case 'agent.tool_calls.detected':
      return makeExecutionStep(item, '准备调用工具', summarizeToolCalls(payload), 'running')
    case 'tool.completed':
      return makeExecutionStep(item, '工具完成', summarizeToolResult(payload), 'success')
    case 'tool.failed':
      return makeExecutionStep(item, '工具失败', String(payload.message ?? '工具执行失败'), 'danger')
    case 'approval.requested':
      return makeExecutionStep(item, '等待审批', summarizeApproval(payload), 'warning')
    case 'approval.resolved':
      return makeExecutionStep(item, '审批处理', summarizeApproval(payload), 'normal')
    case 'turn.failed':
      return makeExecutionStep(item, '运行失败', String(payload.message ?? 'Agent 执行失败'), 'danger')
    case 'turn.interrupted':
      return makeExecutionStep(item, '运行已中断', String(payload.message ?? 'Agent 执行已中断'), 'warning')
    default:
      return undefined
  }
}

function makeExecutionStep(
  item: TaskEvent,
  title: string,
  detail: string,
  tone: ExecutionStep['tone'],
): ExecutionStep {
  return {
    key: item.eventId,
    type: item.type,
    title,
    detail,
    tone,
    sequence: item.sequence,
  }
}

function summarizeToolCalls(payload: Record<string, unknown>) {
  const calls = Array.isArray(payload.toolCalls) ? payload.toolCalls : []
  if (calls.length === 0) return '模型准备调用工具'
  return calls
    .map((raw) => {
      const call = raw as Record<string, unknown>
      const fn = (call.function ?? {}) as Record<string, unknown>
      const name = String(fn.name ?? 'unknown')
      const args = compactText(String(fn.arguments ?? ''))
      return `${name}${args ? ` ${args}` : ''}`
    })
    .join('\n')
}

function summarizeToolResult(payload: Record<string, unknown>) {
  const name = String(payload.name ?? 'tool')
  const output = (payload.output ?? {}) as Record<string, unknown>
  const command = String(output.command ?? '')
  const error = String(output.error ?? '')
  const result = compactText(String(output.output ?? ''))
  const lines = [`${name}${command ? `: ${command}` : ''}`]
  if (error) lines.push(`错误: ${error}`)
  if (result) lines.push(result)
  if (output.truncated === true) lines.push('输出已截断')
  return lines.join('\n')
}

function summarizeApproval(payload: Record<string, unknown>) {
  const approval = (payload.approval ?? payload) as Record<string, unknown>
  const action = String(approval.actionType ?? 'approval')
  const target = String(approval.target ?? '')
  const status = String(approval.status ?? '')
  return compactText([action, target, status].filter(Boolean).join('\n'))
}

function compactText(value: string, limit = 900) {
  const text = value.trim()
  if (text.length <= limit) return text
  return `${text.slice(0, limit)}\n... 已省略 ${text.length - limit} 个字符`
}

function latestTaskStatusText(items: TaskEvent[]) {
  const latestTurnEvent = [...items]
    .filter((item) => item.turnId)
    .sort((a, b) => b.sequence - a.sequence)[0]
  if (!latestTurnEvent) return '等待输入'

  const latestTurnEvents = items
    .filter((item) => item.turnId === latestTurnEvent.turnId)
    .sort((a, b) => a.sequence - b.sequence)
  if (latestTurnEvents.some((item) => item.type === 'turn.failed')) return '最近一轮失败'
  if (latestTurnEvents.some((item) => item.type === 'turn.interrupted')) return '最近一轮已中断'
  if (latestTurnEvents.some((item) => item.type === 'turn.completed')) return '已完成最近一轮'
  if (latestTurnEvents.some((item) => item.type === 'approval.requested')) return '等待审批'
  if (latestTurnEvents.some((item) => item.type === 'agent.message.delta')) return 'Agent 正在回复'
  if (latestTurnEvents.some((item) => item.type === 'agent.tool_calls.detected' || item.type === 'tool.completed')) {
    return '工具执行中'
  }
  return 'Agent 正在思考'
}

function executionDurationText(group: ExecutionGroup) {
  if (!group.startedAt) return ''
  const started = Date.parse(group.startedAt)
  const ended = group.endedAt ? Date.parse(group.endedAt) : nowTick.value
  if (!Number.isFinite(started) || !Number.isFinite(ended)) return ''
  const durationText = formatDuration(Math.max(0, ended - started))
  if (group.status === 'running' || group.status === 'waiting') return `已运行 ${durationText}`
  if (group.status === 'stopped') return `运行过 ${durationText}`
  return `耗时 ${durationText}`
}

function contextUsageText(group: ExecutionGroup) {
  const usage = group.contextUsage
  if (!usage) return ''
  const budget = usage.tokenBudget || usage.contextWindowTokens
  if (!budget) return `上下文约 ${usage.estimatedTokens} tokens`
  const suffix = usage.truncated ? '，已截断' : ''
  return `上下文约 ${usage.estimatedTokens} / ${budget} tokens${suffix}`
}

function formatDuration(ms: number) {
  const totalSeconds = Math.max(0, Math.floor(ms / 1000))
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  if (hours > 0) return `${hours} 小时 ${minutes} 分钟 ${seconds} 秒`
  if (minutes > 0) return `${minutes} 分钟 ${seconds} 秒`
  return `${seconds} 秒`
}

function executionSummaryText(group: ExecutionGroup) {
  const durationText = executionDurationText(group)
  if (group.status === 'waiting') {
    return durationText ? `等待你审批 · ${durationText}` : '等待你审批工具调用'
  }
  if (group.status === 'failed') return '执行失败，展开查看原因'
  if (group.status === 'interrupted') return '执行已中断'
  if (group.status === 'stopped') return '历史轮次未完成，已停止显示运行中'
  if (group.status === 'completed') {
    return group.steps.length > 0 ? '已完成，可展开查看工具记录' : '已完成'
  }
  const lastStep = group.steps[group.steps.length - 1]
  const baseText =
    lastStep?.type === 'agent.tool_calls.detected'
      ? '正在调用工具'
      : lastStep?.type === 'tool.completed'
        ? '正在根据工具结果整理回答'
        : '正在思考'
  return durationText ? `${baseText} · ${durationText}` : baseText
}

function visibleChatContent(block: ChatBlock) {
  if (block.role !== 'assistant') return block.content
  return assistantRenderedContent[block.key] ?? block.content
}

function renderedMarkdown(block: ChatBlock) {
  return markdown.render(visibleChatContent(block))
}

function isAssistantTyping(block: ChatBlock) {
  if (block.role !== 'assistant') return false
  return (assistantRenderedContent[block.key] ?? '').length < (assistantTargetContent[block.key] ?? block.content).length
}

function isAssistantRawMode(block: ChatBlock) {
  return assistantRawMode[block.key] === true
}

function toggleAssistantRawMode(block: ChatBlock) {
  assistantRawMode[block.key] = !assistantRawMode[block.key]
}

async function copyAssistantMarkdown(block: ChatBlock) {
  try {
    await navigator.clipboard.writeText(block.content)
    message.success('Markdown 已复制')
  } catch {
    message.error('复制失败')
  }
}

function syncAssistantTyping(blocks: ChatBlock[]) {
  const activeKeys = new Set<string>()
  for (const block of blocks) {
    if (block.role !== 'assistant') continue
    activeKeys.add(block.key)
    const target = block.content
    const rendered = assistantRenderedContent[block.key]
    assistantTargetContent[block.key] = target

    if (rendered === undefined) {
      const group = block.turnId ? executionGroupByTurn.value.get(block.turnId) : undefined
      assistantRenderedContent[block.key] =
        group?.status === 'running' || group?.status === 'waiting' ? '' : target
    } else if (!target.startsWith(rendered)) {
      assistantRenderedContent[block.key] = target
      clearAssistantTypingTimer(block.key)
    }

    if ((assistantRenderedContent[block.key] ?? '').length < target.length) {
      scheduleAssistantTyping(block.key)
    }
  }

  for (const key of Object.keys(assistantRenderedContent)) {
    if (activeKeys.has(key)) continue
    delete assistantRenderedContent[key]
    delete assistantTargetContent[key]
    delete assistantRawMode[key]
    clearAssistantTypingTimer(key)
  }
}

function scheduleAssistantTyping(key: string) {
  if (assistantTypingTimers.has(key)) return
  const timer = window.setTimeout(() => {
    assistantTypingTimers.delete(key)
    revealNextAssistantChar(key)
  }, assistantTypingDelayMS)
  assistantTypingTimers.set(key, timer)
}

function revealNextAssistantChar(key: string) {
  const target = assistantTargetContent[key] ?? ''
  const rendered = assistantRenderedContent[key] ?? ''
  if (rendered.length >= target.length) return

  const targetChars = Array.from(target)
  const renderedLength = Array.from(rendered).length
  assistantRenderedContent[key] = targetChars.slice(0, renderedLength + 1).join('')
  scrollChatToBottom()

  if (assistantRenderedContent[key].length < target.length) {
    scheduleAssistantTyping(key)
  }
}

function clearAssistantTypingTimer(key: string) {
  const timer = assistantTypingTimers.get(key)
  if (timer === undefined) return
  window.clearTimeout(timer)
  assistantTypingTimers.delete(key)
}

function clearAssistantTypingTimers() {
  for (const timer of assistantTypingTimers.values()) {
    window.clearTimeout(timer)
  }
  assistantTypingTimers.clear()
}

function scrollChatToBottom(behavior: ScrollBehavior = 'auto') {
  void nextTick(() => {
    window.requestAnimationFrame(() => {
      const target = chatBodyRef.value
      if (!target) return
      target.scrollTo({
        top: target.scrollHeight,
        behavior,
      })
    })
  })
}

function executionStatusText(status: ExecutionGroup['status']) {
  switch (status) {
    case 'running':
      return '运行中'
    case 'completed':
      return '已完成'
    case 'failed':
      return '失败'
    case 'interrupted':
      return '已中断'
    case 'waiting':
      return '待审批'
    case 'stopped':
      return '已停止'
    default:
      return '事件'
  }
}

function executionStatusColor(status: ExecutionGroup['status']) {
  switch (status) {
    case 'running':
      return 'processing'
    case 'completed':
      return 'success'
    case 'failed':
      return 'error'
    case 'interrupted':
      return 'warning'
    case 'waiting':
      return 'warning'
    default:
      return 'default'
  }
}

function openTaskModal(item?: Task) {
  editingTaskId.value = item?.id ?? ''
  taskTitle.value = item?.title ?? ''
  taskModalOpen.value = true
}

function resetProviderForm() {
  providerForm.name = 'Local Model'
  providerForm.baseUrl = 'http://localhost:11434/v1'
  providerForm.model = 'qwen2.5-coder:7b'
  providerForm.apiKey = ''
  providerForm.isDefault = providers.value.length === 0
  providerForm.enabled = true
  providerForm.contextWindowTokens = 8192
}

function openProviderModal(item?: Provider) {
  if (item) {
    editingProviderId.value = item.id
    providerForm.name = item.name
    providerForm.baseUrl = item.baseUrl
    providerForm.model = item.model
    providerForm.apiKey = ''
    providerForm.isDefault = item.isDefault
    providerForm.enabled = item.enabled
    providerForm.contextWindowTokens = item.contextWindowTokens || 8192
  } else {
    editingProviderId.value = ''
    resetProviderForm()
  }
  providerModalOpen.value = true
}

function resetWorkspaceForm() {
  workspaceForm.name = productName
  workspaceForm.rootPath = ''
  workspaceForm.permissionMode = 'request_approval'
  workspaceForm.defaultProviderId = defaultProvider.value?.id ?? ''
  workspaceForm.trusted = true
}

function openWorkspaceModal(item?: Workspace) {
  if (item) {
    editingWorkspaceId.value = item.id
    workspaceForm.name = item.name
    workspaceForm.rootPath = item.rootPath
    workspaceForm.permissionMode = item.permissionMode
    workspaceForm.defaultProviderId = item.defaultProviderId ?? ''
    workspaceForm.trusted = item.trusted
  } else {
    editingWorkspaceId.value = ''
    resetWorkspaceForm()
  }
  workspaceModalOpen.value = true
}

function syncSelectedWorkspace() {
  if (workspaces.value.length === 0) {
    selectedWorkspaceId.value = ''
    return
  }
  if (!workspaces.value.some((item) => item.id === selectedWorkspaceId.value)) {
    selectedWorkspaceId.value = workspaces.value[0].id
  }
}

async function refreshAll() {
  loading.value = true
  try {
    const [providerData, workspaceData] = await Promise.all([
      api.listProviders(),
      api.listWorkspaces(),
    ])
    providers.value = providerData.items
    workspaces.value = workspaceData.items
    const previousWorkspaceId = selectedWorkspaceId.value
    syncSelectedWorkspace()
    if (selectedWorkspaceId.value !== previousWorkspaceId) {
      return
    }
    await refreshWorkspaceState()
  } catch (err) {
    showError(err)
  } finally {
    loading.value = false
  }
}

async function refreshWorkspaceState() {
  if (!selectedWorkspaceId.value) {
    tasks.value = []
    approvals.value = []
    externalPaths.value = []
    return
  }
  const previousTaskId = selectedTaskId.value
  const [taskData, approvalData, pathData] = await Promise.all([
    api.listTasks(selectedWorkspaceId.value),
    api.listApprovals(selectedWorkspaceId.value),
    api.listExternalPaths(selectedWorkspaceId.value),
  ])
  tasks.value = taskData.items
  approvals.value = approvalData.items
  externalPaths.value = pathData.items
  if (!selectedTaskId.value && tasks.value.length > 0) {
    selectedTaskId.value = tasks.value[0].id
  }
  if (selectedTaskId.value === previousTaskId) {
    await refreshEvents()
  }
}

async function refreshApprovals() {
  if (!selectedWorkspaceId.value) {
    approvals.value = []
    return
  }
  const approvalData = await api.listApprovals(selectedWorkspaceId.value)
  approvals.value = approvalData.items
}

async function refreshEvents() {
  if (!selectedTaskId.value) {
    events.value = []
    closeTaskSocket()
    return
  }
  const data = await api.listTaskEvents(selectedTaskId.value)
  events.value = data.items.map(normalizeEvent)
  connectTaskSocket()
}

async function saveProvider() {
  if (!providerForm.name.trim() || !providerForm.baseUrl.trim() || !providerForm.model.trim()) return
  providerSubmitting.value = true
  const body: Record<string, unknown> = {
    name: providerForm.name.trim(),
    type: 'openai-compatible',
    baseUrl: providerForm.baseUrl.trim(),
    model: providerForm.model.trim(),
    isDefault: providerForm.isDefault,
    enabled: providerForm.enabled,
    contextWindowTokens: providerForm.contextWindowTokens || 8192,
  }
  if (!editingProviderId.value || providerForm.apiKey.trim()) {
    body.apiKey = providerForm.apiKey
  }
  try {
    if (editingProviderId.value) {
      await api.updateProvider(editingProviderId.value, body)
      message.success('Provider 已更新')
    } else {
      await api.createProvider(body)
      message.success('Provider 已创建')
    }
    providerModalOpen.value = false
    await refreshAll()
  } catch (err) {
    showError(err)
  } finally {
    providerSubmitting.value = false
  }
}

async function testProvider(provider: Provider) {
  try {
    const result = await api.testProvider(provider.id)
    message.success(result.message)
  } catch (err) {
    showError(err)
  }
}

async function deleteProvider(item: Provider) {
  try {
    await api.deleteProvider(item.id)
    message.success('Provider 已删除')
    await refreshAll()
  } catch (err) {
    showError(err)
  }
}

async function saveWorkspace() {
  if (!workspaceForm.name.trim() || !workspaceForm.rootPath.trim()) return
  workspaceSubmitting.value = true
  const body = {
    name: workspaceForm.name.trim(),
    rootPath: workspaceForm.rootPath.trim(),
    defaultProviderId: workspaceForm.defaultProviderId || defaultProvider.value?.id || '',
    permissionMode: workspaceForm.permissionMode,
    trusted: workspaceForm.trusted,
  }
  try {
    const saved = editingWorkspaceId.value
      ? await api.updateWorkspace(editingWorkspaceId.value, body)
      : await api.createWorkspace(body)
    workspaceModalOpen.value = false
    selectedWorkspaceId.value = saved.id
    message.success(editingWorkspaceId.value ? '工作区已更新' : '工作区已创建')
    await refreshAll()
  } catch (err) {
    showError(err)
  } finally {
    workspaceSubmitting.value = false
  }
}

async function deleteWorkspace(item: Workspace) {
  try {
    await api.deleteWorkspace(item.id)
    message.success('工作区已删除')
    await refreshAll()
  } catch (err) {
    showError(err)
  }
}

async function createExternalPath() {
  if (!selectedWorkspaceId.value || !externalPathForm.path.trim()) return
  try {
    await api.createExternalPath(selectedWorkspaceId.value, {
      path: externalPathForm.path,
      pathType: externalPathForm.pathType,
      accessMode: externalPathForm.accessMode,
      sourceTaskId: selectedTaskId.value,
    })
    externalPathForm.path = ''
    message.success('外部路径已授权')
    await refreshWorkspaceState()
  } catch (err) {
    showError(err)
  }
}

async function deleteExternalPath(item: ExternalPath) {
  if (!selectedWorkspaceId.value) return
  try {
    await api.deleteExternalPath(selectedWorkspaceId.value, item.id)
    await refreshWorkspaceState()
  } catch (err) {
    showError(err)
  }
}

async function saveTask() {
  if (!selectedWorkspaceId.value || !taskTitle.value.trim()) return
  taskSubmitting.value = true
  try {
    const saved = editingTaskId.value
      ? await api.updateTask(editingTaskId.value, taskTitle.value.trim())
      : await api.createTask(selectedWorkspaceId.value, taskTitle.value.trim())
    selectedTaskId.value = saved.id
    taskTitle.value = ''
    editingTaskId.value = ''
    taskModalOpen.value = false
    await refreshWorkspaceState()
  } catch (err) {
    showError(err)
  } finally {
    taskSubmitting.value = false
  }
}

async function deleteTask(item: Task) {
  const previousTaskId = selectedTaskId.value
  const currentIndex = tasks.value.findIndex((taskItem) => taskItem.id === item.id)
  const nextTask = tasks.value[currentIndex + 1] ?? tasks.value[currentIndex - 1]
  try {
    if (item.id === selectedTaskId.value) {
      closeTaskSocket()
      selectedTaskId.value = nextTask?.id ?? ''
    }
    await api.deleteTask(item.id)
    message.success('任务已删除')
    await refreshWorkspaceState()
  } catch (err) {
    selectedTaskId.value = previousTaskId
    showError(err)
  }
}

async function sendTurn() {
  if (!selectedTaskId.value || !userInput.value.trim()) return
  const input = userInput.value
  userInput.value = ''
  scrollChatToBottom('smooth')
  try {
    await api.createTurn(selectedTaskId.value, input)
    await refreshEvents()
    scrollChatToBottom('smooth')
  } catch (err) {
    userInput.value = input
    showError(err)
  }
}

function handleComposerKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return
  event.preventDefault()
  void sendTurn()
}

async function cancelTask() {
  if (!selectedTaskId.value) return
  try {
    await api.cancelTask(selectedTaskId.value)
    message.success('已发送打断请求')
    await refreshEvents()
  } catch (err) {
    showError(err)
  }
}

async function resolveApproval(item: Approval, status: 'approved' | 'rejected') {
  try {
    await api.resolveApproval(item.id, status, status === 'approved' ? '同意' : '拒绝')
    await refreshWorkspaceState()
  } catch (err) {
    showError(err)
  }
}

function latestTaskEventSequence() {
  return events.value.reduce((max, item) => Math.max(max, item.sequence ?? 0), 0)
}

function clearTaskSocketReconnectTimer() {
  if (taskSocketReconnectTimer === undefined) return
  window.clearTimeout(taskSocketReconnectTimer)
  taskSocketReconnectTimer = undefined
}

function closeTaskSocket() {
  clearTaskSocketReconnectTimer()
  if (taskSocket) {
    taskSocket.onopen = null
    taskSocket.onclose = null
    taskSocket.onmessage = null
    taskSocket.onerror = null
    taskSocket.close()
    taskSocket = undefined
  }
  wsConnected.value = false
}

function scheduleTaskSocketReconnect(socket: WebSocket) {
  if (taskSocket !== socket || !selectedTaskId.value || taskSocketReconnectTimer !== undefined) return
  const delay = Math.min(
    taskSocketReconnectBaseMS * 2 ** taskSocketReconnectAttempts,
    taskSocketReconnectMaxMS,
  )
  taskSocketReconnectAttempts += 1
  taskSocketReconnectTimer = window.setTimeout(() => {
    taskSocketReconnectTimer = undefined
    if (taskSocket !== socket || !selectedTaskId.value) return
    connectTaskSocket({ reconnect: true })
  }, delay)
}

function connectTaskSocket(options: { reconnect?: boolean } = {}) {
  if (!options.reconnect) {
    clearTaskSocketReconnectTimer()
    taskSocketReconnectAttempts = 0
  }
  if (taskSocket) {
    taskSocket.onopen = null
    taskSocket.onclose = null
    taskSocket.onmessage = null
    taskSocket.onerror = null
    taskSocket.close()
  }
  wsConnected.value = false
  if (!selectedTaskId.value) return
  const socket = new WebSocket(taskWebSocketURL(selectedTaskId.value, latestTaskEventSequence()))
  taskSocket = socket
  socket.onopen = () => {
    if (taskSocket !== socket) return
    wsConnected.value = true
    taskSocketReconnectAttempts = 0
  }
  socket.onclose = () => {
    if (taskSocket !== socket) return
    wsConnected.value = false
    scheduleTaskSocketReconnect(socket)
  }
  socket.onerror = () => {
    if (taskSocket !== socket) return
    wsConnected.value = false
  }
  socket.onmessage = (raw) => {
    if (taskSocket !== socket) return
    const item = normalizeEvent(JSON.parse(raw.data) as TaskEvent)
    const index = events.value.findIndex((event) => event.eventId === item.eventId)
    const isNewEvent = index < 0
    if (index >= 0) {
      events.value[index] = item
    } else {
      events.value.push(item)
      events.value.sort((a, b) => a.sequence - b.sequence)
    }
    if (isNewEvent && item.type.startsWith('approval.')) {
      void refreshApprovals()
    }
  }
}

function normalizeEvent(item: TaskEvent): TaskEvent {
  if (item.payload !== undefined) return item
  if (!item.payloadJson) return { ...item, payload: {} }
  try {
    return { ...item, payload: JSON.parse(item.payloadJson) }
  } catch {
    return { ...item, payload: {} }
  }
}

function showError(err: unknown) {
  message.error(err instanceof Error ? err.message : '操作失败')
}

watch(selectedWorkspaceId, () => {
  selectedTaskId.value = ''
  void refreshWorkspaceState()
})

watch(selectedTaskId, () => {
  void refreshEvents()
})

watch(runningExecutionKeys, (keys) => {
  if (keys.length === 0) return
  executionOpenKeys.value = Array.from(new Set([...keys, ...executionOpenKeys.value]))
})

watch(
  () => chatBlocks.value.map((block) => `${block.key}:${block.role}:${block.content}`).join('\u0001'),
  () => syncAssistantTyping(chatBlocks.value),
  { immediate: true, flush: 'post' },
)

watch(
  () =>
    chatTimeline.value
      .map((item) => {
        if (item.kind === 'message') return `${item.key}:${item.block?.content.length ?? 0}`
        return `${item.key}:${item.group?.lastSequence ?? 0}:${item.group?.status ?? ''}`
      })
      .join('|'),
  () => scrollChatToBottom(),
  { flush: 'post' },
)

watch(selectedThemeName, (name) => {
  window.localStorage.setItem(themeStorageKey, name)
})

onMounted(refreshAll)
const nowTickTimer = window.setInterval(() => {
  nowTick.value = Date.now()
}, 1000)
onBeforeUnmount(() => {
  stopPanelResize()
  clearAssistantTypingTimers()
  window.clearInterval(nowTickTimer)
  closeTaskSocket()
})
</script>

<template>
  <a-config-provider :theme="antdTheme">
    <main
      class="codex-shell"
      :class="{ 'left-collapsed': leftPanelCollapsed, 'right-collapsed': rightPanelCollapsed }"
      :data-theme="selectedThemeName"
      :style="shellStyle"
    >
      <aside class="left-panel" :class="{ collapsed: leftPanelCollapsed }">
        <button
          class="sidebar-toggle"
          :title="leftPanelCollapsed ? '展开左侧栏' : '折叠左侧栏'"
          :aria-label="leftPanelCollapsed ? '展开左侧栏' : '折叠左侧栏'"
          @click="leftPanelCollapsed = !leftPanelCollapsed"
        >
          <PanelLeftOpen v-if="leftPanelCollapsed" :size="17" />
          <PanelLeftClose v-else :size="17" />
        </button>
        <template v-if="!leftPanelCollapsed">
          <section class="brand-row">
            <div class="water-mark"><span>若</span></div>
            <div>
              <h1>{{ productName }}</h1>
              <p>{{ productTagline }}</p>
            </div>
          </section>

          <section class="nav-section">
            <label>工作区</label>
            <a-select v-model:value="selectedWorkspaceId" placeholder="选择工作区" class="full">
              <a-select-option v-for="item in workspaces" :key="item.id" :value="item.id">
                {{ item.name }}
              </a-select-option>
            </a-select>
            <div v-if="selectedWorkspace" class="workspace-meta">
              <span>{{ selectedWorkspace.permissionMode === 'full_access' ? '完全访问' : '请求审批' }}</span>
              <span>{{ defaultProvider?.name ?? '未配置模型' }}</span>
            </div>
          </section>

          <section class="nav-section grow">
            <div class="section-title">
              <a-space>
                <label>任务</label>
                <a-button
                  size="small"
                  type="primary"
                  :disabled="!selectedWorkspaceId"
                  title="新增任务"
                  aria-label="新增任务"
                  @click="openTaskModal()"
                >
                  <template #icon><Plus :size="14" /></template>
                </a-button>
              </a-space>
              <a-tag>{{ tasks.length }}</a-tag>
            </div>
            <div class="task-stack">
              <div
                v-for="item in tasks"
                :key="item.id"
                class="task-item"
                :class="{ active: item.id === selectedTaskId }"
                @click="selectedTaskId = item.id"
              >
                <div class="task-item-main">
                  <span>{{ item.title }}</span>
                  <small>
                    <template v-if="item.id === selectedTaskId">
                      {{ item.status }} · {{ statusText }}
                    </template>
                    <template v-else>
                      {{ item.status }}
                    </template>
                  </small>
                </div>
                <a-space class="task-actions">
                  <a-button size="small" title="编辑任务" aria-label="编辑任务" @click.stop="openTaskModal(item)">
                    <template #icon><Pencil :size="14" /></template>
                  </a-button>
                  <a-popconfirm title="确认删除这个任务？" ok-text="删除" cancel-text="取消" @confirm="deleteTask(item)">
                    <a-button size="small" danger title="删除任务" aria-label="删除任务" @click.stop>
                      <template #icon><Trash2 :size="14" /></template>
                    </a-button>
                  </a-popconfirm>
                </a-space>
              </div>
              <a-empty v-if="tasks.length === 0" description="暂无任务" />
            </div>
          </section>

          <section class="nav-section compact">
            <div class="section-title">
              <label>待审批</label>
              <a-tag color="gold">{{ approvals.length }}</a-tag>
            </div>
            <button v-if="approvals.length > 0" class="plain-link" @click="rightTab = 'approvals'">
              查看审批队列
            </button>
            <span v-else class="muted">无待处理动作</span>
          </section>
        </template>
      </aside>

      <button
        v-if="!leftPanelCollapsed"
        class="resize-handle left-resize"
        title="拖动调整左侧栏宽度"
        aria-label="拖动调整左侧栏宽度"
        @pointerdown.prevent="startPanelResize('left', $event)"
      >
        <GripVertical :size="16" />
      </button>

      <section class="center-panel">
        <header class="chat-header">
          <div class="chat-header-main">
            <span class="eyebrow">{{ selectedWorkspace?.name ?? productName }}</span>
            <h2>{{ selectedTask?.title ?? '选择任务开始' }}</h2>
            <div class="chat-status-line">
              <a-tag :color="taskHeaderTone">{{ statusText }}</a-tag>
              <span v-if="latestTurnDurationText" class="duration-chip">{{ latestTurnDurationText }}</span>
              <span v-if="selectedTask" class="muted">任务 {{ selectedTask.status }}</span>
            </div>
          </div>
          <div class="chat-header-actions">
            <span class="connection-state" :class="{ connected: wsConnected }">
              <span class="connection-dot" />
              <span>{{ wsConnected ? '实时' : '离线' }}</span>
            </span>
            <a-button
              class="header-icon-button stop"
              size="small"
              :disabled="!selectedTaskId"
              title="打断当前任务"
              aria-label="打断当前任务"
              @click="cancelTask"
            >
              <template #icon><Square :size="13" /></template>
            </a-button>
            <a-button
              class="header-icon-button"
              size="small"
              :loading="loading"
              title="刷新"
              aria-label="刷新"
              @click="refreshAll"
            >
              <template #icon><RefreshCw :size="13" /></template>
            </a-button>
          </div>
        </header>

        <div ref="chatBodyRef" class="chat-body">
          <div v-if="chatBlocks.length === 0" class="empty-state">
            <h3>把任务交给{{ productName }}</h3>
            <p>创建任务后直接输入需求。工具调用、审批和执行结果会跟随对话展示，保持可观测、可打断。</p>
          </div>
          <template v-for="item in chatTimeline" :key="item.key">
            <article
              v-if="item.kind === 'message' && item.block"
              class="message-block"
              :class="item.block.role"
            >
              <div class="message-avatar">
                {{ item.block.role === 'user' ? '你' : item.block.role === 'assistant' ? '若' : '!' }}
              </div>
              <div class="message-content">
                <div class="message-title-row">
                  <div class="message-title">{{ item.block.title }}</div>
                  <a-space v-if="item.block.role === 'assistant'" class="message-actions" :size="4">
                    <a-button
                      size="small"
                      :title="isAssistantRawMode(item.block) ? '显示内容' : '显示 Markdown 原文'"
                      :aria-label="isAssistantRawMode(item.block) ? '显示内容' : '显示 Markdown 原文'"
                      @click="toggleAssistantRawMode(item.block)"
                    >
                      <template #icon>
                        <Eye v-if="isAssistantRawMode(item.block)" :size="13" />
                        <Code2 v-else :size="13" />
                      </template>
                    </a-button>
                    <a-button
                      size="small"
                      title="复制 Markdown"
                      aria-label="复制 Markdown"
                      @click="copyAssistantMarkdown(item.block)"
                    >
                      <template #icon><Copy :size="13" /></template>
                    </a-button>
                  </a-space>
                </div>
                <pre v-if="item.block.role === 'assistant' && isAssistantRawMode(item.block)" class="markdown-source">{{ visibleChatContent(item.block) }}</pre>
                <div
                  v-else-if="item.block.role === 'assistant'"
                  class="markdown-body"
                  :class="{ typing: isAssistantTyping(item.block) }"
                >
                  <div v-html="renderedMarkdown(item.block)" />
                  <span v-if="isAssistantTyping(item.block)" class="answer-cursor" aria-hidden="true" />
                </div>
                <p v-else :class="{ typing: isAssistantTyping(item.block) }">
                  <span>{{ visibleChatContent(item.block) }}</span>
                  <span v-if="isAssistantTyping(item.block)" class="answer-cursor" aria-hidden="true" />
                </p>
              </div>
            </article>

            <section
              v-else-if="item.kind === 'execution' && item.group"
              class="message-block execution-message"
            >
              <div class="message-avatar">过</div>
              <div class="message-content execution-panel">
                <div
                  v-if="item.group.steps.length === 0"
                  class="execution-status-card"
                  :class="item.group.status"
                >
                  <span>{{ executionSummaryText(item.group) }}</span>
                  <small
                    v-if="
                      item.group.status !== 'running' &&
                      item.group.status !== 'waiting' &&
                      executionDurationText(item.group)
                    "
                  >
                    {{ executionDurationText(item.group) }}
                  </small>
                  <small v-if="contextUsageText(item.group)">{{ contextUsageText(item.group) }}</small>
                  <span
                    v-if="item.group.status === 'running' || item.group.status === 'waiting'"
                    class="thinking-dots"
                    aria-hidden="true"
                  />
                </div>
                <a-collapse v-else v-model:activeKey="executionOpenKeys" ghost>
                  <a-collapse-panel :key="item.group.key">
                    <template #header>
                      <div class="execution-header">
                        <div class="execution-heading">
                          <strong>{{ executionSummaryText(item.group) }}</strong>
                          <small>
                            {{
                              [
                                item.group.subtitle || `#${item.group.lastSequence}`,
                                contextUsageText(item.group),
                                item.group.status === 'running' || item.group.status === 'waiting'
                                  ? ''
                                  : executionDurationText(item.group),
                              ]
                                .filter(Boolean)
                                .join(' · ')
                            }}
                          </small>
                        </div>
                        <a-tag :color="executionStatusColor(item.group.status)">
                          {{ executionStatusText(item.group.status) }}
                        </a-tag>
                      </div>
                    </template>
                    <div class="execution-steps">
                      <article
                        v-for="step in item.group.steps"
                        :key="step.key"
                        class="execution-step"
                        :class="step.tone"
                      >
                        <div class="execution-dot" />
                        <div class="execution-step-body">
                          <div class="execution-step-title">
                            <strong>{{ step.title }}</strong>
                            <code>#{{ step.sequence }}</code>
                          </div>
                          <pre v-if="step.detail">{{ step.detail }}</pre>
                        </div>
                      </article>
                    </div>
                  </a-collapse-panel>
                </a-collapse>
              </div>
            </section>
          </template>
        </div>

        <footer class="composer">
          <div class="composer-box">
            <a-textarea
              v-model:value="userInput"
              class="composer-input"
              :auto-size="{ minRows: 2, maxRows: 8 }"
              :placeholder="`让${productName}修改、解释或规划你的项目`"
              @keydown="handleComposerKeydown"
            />
            <a-button
              class="composer-send"
              type="primary"
              :disabled="!selectedTaskId || !userInput.trim()"
              title="发送"
              aria-label="发送"
              @click="sendTurn"
            >
              <template #icon><Send :size="15" /></template>
            </a-button>
          </div>
        </footer>
      </section>

      <button
        v-if="!rightPanelCollapsed"
        class="resize-handle right-resize"
        title="拖动调整右侧栏宽度"
        aria-label="拖动调整右侧栏宽度"
        @pointerdown.prevent="startPanelResize('right', $event)"
      >
        <GripVertical :size="16" />
      </button>

      <aside class="right-panel" :class="{ collapsed: rightPanelCollapsed }">
        <button
          class="sidebar-toggle"
          :title="rightPanelCollapsed ? '展开右侧栏' : '折叠右侧栏'"
          :aria-label="rightPanelCollapsed ? '展开右侧栏' : '折叠右侧栏'"
          @click="rightPanelCollapsed = !rightPanelCollapsed"
        >
          <PanelRightOpen v-if="rightPanelCollapsed" :size="17" />
          <PanelRightClose v-else :size="17" />
        </button>
        <a-tabs v-if="!rightPanelCollapsed" v-model:activeKey="rightTab" size="small">
          <a-tab-pane key="approvals" tab="审批">
            <a-list :data-source="approvals" class="dense-list">
              <template #renderItem="{ item }">
                <a-list-item>
                  <a-list-item-meta :title="item.actionType">
                    <template #description>
                      <div class="approval-desc">
                        <code>{{ item.target }}</code>
                        <span>{{ item.riskSummary }}</span>
                        <small>{{ item.expectedImpact }}</small>
                      </div>
                    </template>
                  </a-list-item-meta>
                  <a-space>
                    <a-button size="small" type="primary" @click="resolveApproval(item, 'approved')">
                      <template #icon><CheckCircle :size="14" /></template>
                    </a-button>
                    <a-button size="small" danger @click="resolveApproval(item, 'rejected')">拒绝</a-button>
                  </a-space>
                </a-list-item>
              </template>
            </a-list>
            <a-empty v-if="approvals.length === 0" description="暂无待审批" />
          </a-tab-pane>

          <a-tab-pane key="context" tab="上下文">
            <section class="panel-form">
              <div class="section-title">
                <label>外部路径授权</label>
                <a-tag>{{ externalPaths.length }}</a-tag>
              </div>
              <a-input v-model:value="externalPathForm.path" placeholder="外部绝对路径" />
              <a-input-group compact>
                <a-select v-model:value="externalPathForm.pathType" style="width: 34%">
                  <a-select-option value="directory">目录</a-select-option>
                  <a-select-option value="file">文件</a-select-option>
                </a-select>
                <a-select v-model:value="externalPathForm.accessMode" style="width: 30%">
                  <a-select-option value="read">读</a-select-option>
                  <a-select-option value="write">写</a-select-option>
                </a-select>
                <a-button style="width: 36%" @click="createExternalPath">授权</a-button>
              </a-input-group>
              <a-list size="small" :data-source="externalPaths">
                <template #renderItem="{ item }">
                  <a-list-item>
                    <a-list-item-meta :title="item.path" :description="`${item.pathType} / ${item.accessMode}`" />
                    <a-button size="small" danger @click="deleteExternalPath(item)">撤销</a-button>
                  </a-list-item>
                </template>
              </a-list>
            </section>
          </a-tab-pane>

          <a-tab-pane key="settings" tab="设置">
            <a-collapse v-model:activeKey="settingsOpenKey" class="settings-collapse" accordion ghost>
              <a-collapse-panel key="appearance">
                <template #header>
                  <div class="settings-panel-header">
                    <span>外观</span>
                    <a-tag>{{ activeTheme.label }}</a-tag>
                  </div>
                </template>
                <section class="settings-panel-body">
                  <div class="theme-grid">
                    <button
                      v-for="item in themeOptions"
                      :key="item.name"
                      class="theme-card"
                      :class="{ active: item.name === selectedThemeName }"
                      type="button"
                      @click="selectTheme(item.name)"
                    >
                      <span class="theme-swatch-row">
                        <span
                          v-for="swatch in item.swatches"
                          :key="swatch"
                          class="theme-swatch"
                          :style="{ background: swatch }"
                        />
                      </span>
                      <strong>{{ item.label }}</strong>
                      <small>{{ item.subtitle }}</small>
                    </button>
                  </div>
                </section>
              </a-collapse-panel>

              <a-collapse-panel key="providers">
                <template #header>
                  <div class="settings-panel-header">
                    <span>Provider</span>
                    <a-space>
                      <a-tag>{{ providers.length }}</a-tag>
                      <a-button size="small" type="primary" @click.stop="openProviderModal()">
                        <template #icon><Plus :size="14" /></template>
                        新增
                      </a-button>
                    </a-space>
                  </div>
                </template>
                <section class="settings-panel-body">
                  <a-list v-if="providers.length > 0" :data-source="providers" class="settings-list">
                    <template #renderItem="{ item }">
                      <div
                        class="settings-row provider-row"
                        :class="{
                          active: item.id === activeProviderId,
                          disabled: !item.enabled,
                        }"
                      >
                        <div class="provider-main">
                          <div class="provider-title">
                            <span>{{ item.name }}</span>
                            <a-tag v-if="item.id === activeProviderId" class="provider-status active">
                              <template #icon><CheckCircle :size="12" /></template>
                              激活
                            </a-tag>
                            <a-tag v-else-if="item.isDefault" class="provider-status">默认</a-tag>
                            <a-tag v-else-if="item.enabled" class="provider-status">启用</a-tag>
                            <a-tag v-else class="provider-status disabled">停用</a-tag>
                          </div>
                          <div class="provider-meta">
                            <div class="provider-meta-row">
                              <small>地址</small>
                              <code>{{ item.baseUrl }}</code>
                            </div>
                            <div class="provider-meta-row">
                              <small>模型</small>
                              <span>{{ item.model }}</span>
                            </div>
                            <div class="provider-meta-row">
                              <small>上下文</small>
                              <span>{{ item.contextWindowTokens || 8192 }} tokens</span>
                            </div>
                            <div class="provider-meta-row">
                              <small>密钥</small>
                              <span>{{ item.apiKeyConfigured ? '已配置' : '未配置' }}</span>
                            </div>
                          </div>
                        </div>
                        <div class="provider-actions">
                          <a-button size="small" class="provider-test-button" @click="testProvider(item)">测试</a-button>
                          <a-button size="small" title="编辑 Provider" aria-label="编辑 Provider" @click="openProviderModal(item)">
                            <template #icon><Pencil :size="14" /></template>
                          </a-button>
                          <a-popconfirm title="确认删除这个 Provider？" ok-text="删除" cancel-text="取消" @confirm="deleteProvider(item)">
                            <a-button size="small" danger title="删除 Provider" aria-label="删除 Provider">
                              <template #icon><Trash2 :size="14" /></template>
                            </a-button>
                          </a-popconfirm>
                        </div>
                      </div>
                    </template>
                  </a-list>
                  <a-empty v-else description="暂无 Provider" />
                </section>
              </a-collapse-panel>

              <a-collapse-panel key="workspaces">
                <template #header>
                  <div class="settings-panel-header">
                    <span>工作区</span>
                    <a-space>
                      <a-tag>{{ workspaces.length }}</a-tag>
                      <a-button size="small" type="primary" @click.stop="openWorkspaceModal()">
                        <template #icon><FolderPlus :size="14" /></template>
                        新增
                      </a-button>
                    </a-space>
                  </div>
                </template>
                <section class="settings-panel-body">
                  <a-list v-if="workspaces.length > 0" :data-source="workspaces" class="settings-list">
                    <template #renderItem="{ item }">
                      <a-list-item
                        class="settings-row"
                        :class="{ active: item.id === selectedWorkspaceId }"
                        @click="selectedWorkspaceId = item.id"
                      >
                        <a-list-item-meta :title="item.name">
                          <template #description>
                            <div class="settings-desc">
                              <code>{{ item.rootPath }}</code>
                              <span>
                                {{ item.permissionMode === 'full_access' ? '完全访问' : '请求审批' }}
                              </span>
                            </div>
                          </template>
                        </a-list-item-meta>
                        <a-space>
                          <a-button size="small" @click.stop="openWorkspaceModal(item)">
                            <template #icon><Pencil :size="14" /></template>
                          </a-button>
                          <a-popconfirm title="确认删除这个工作区？" ok-text="删除" cancel-text="取消" @confirm="deleteWorkspace(item)">
                            <a-button size="small" danger @click.stop>
                              <template #icon><Trash2 :size="14" /></template>
                            </a-button>
                          </a-popconfirm>
                        </a-space>
                      </a-list-item>
                    </template>
                  </a-list>
                  <a-empty v-else description="暂无工作区" />
                </section>
              </a-collapse-panel>
            </a-collapse>
          </a-tab-pane>
        </a-tabs>
      </aside>

      <a-modal
        v-model:open="taskModalOpen"
        :title="taskModalTitle"
        :ok-text="editingTaskId ? '保存' : '创建'"
        cancel-text="取消"
        :confirm-loading="taskSubmitting"
        @ok="saveTask"
      >
        <a-input
          v-model:value="taskTitle"
          placeholder="任务标题"
          @keydown.enter.prevent="saveTask"
        />
      </a-modal>

      <a-modal
        v-model:open="workspaceModalOpen"
        :title="workspaceModalTitle"
        :ok-text="editingWorkspaceId ? '保存' : '创建'"
        cancel-text="取消"
        :confirm-loading="workspaceSubmitting"
        @ok="saveWorkspace"
      >
        <a-space class="modal-form" direction="vertical" :size="12">
          <a-input v-model:value="workspaceForm.name" placeholder="工作区名称" />
          <a-input v-model:value="workspaceForm.rootPath" placeholder="工作区根路径" />
          <a-segmented
            v-model:value="workspaceForm.permissionMode"
            block
            :options="[
              { label: '请求审批', value: 'request_approval' },
              { label: '完全访问', value: 'full_access' },
            ]"
          />
        </a-space>
      </a-modal>

      <a-modal
        v-model:open="providerModalOpen"
        :title="providerModalTitle"
        :ok-text="editingProviderId ? '保存' : '创建'"
        cancel-text="取消"
        :confirm-loading="providerSubmitting"
        @ok="saveProvider"
      >
        <a-space class="modal-form" direction="vertical" :size="12">
          <a-input v-model:value="providerForm.name" placeholder="Provider 名称" />
          <a-input v-model:value="providerForm.baseUrl" placeholder="Base URL" />
          <a-input v-model:value="providerForm.model" placeholder="Model" />
          <a-input-number
            v-model:value="providerForm.contextWindowTokens"
            class="full"
            :min="1024"
            :step="1024"
            placeholder="上下文长度，默认 8192"
          />
          <a-input-password
            v-model:value="providerForm.apiKey"
            :placeholder="editingProviderId ? 'API Key（留空不修改）' : 'API Key'"
          />
        </a-space>
      </a-modal>
    </main>
  </a-config-provider>
</template>
