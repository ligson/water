<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { message } from 'ant-design-vue'
import {
  ArrowRight,
  ChevronLeft,
  ChevronDown,
  CheckCircle,
  Code2,
  Copy,
  Download,
  Eye,
  FileText,
  Folder,
  FolderPlus,
  GripVertical,
  KeyRound,
  LockKeyhole,
  LogOut,
  Link2,
  PackageOpen,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  Paperclip,
  Pencil,
  Plus,
  RefreshCw,
  Send,
  Settings2,
  Square,
  Terminal,
  Trash2,
  UploadCloud,
  X,
} from '@lucide/vue'
import MarkdownIt from 'markdown-it'
import WaterAuthScene from './components/auth/WaterAuthScene.vue'
import ServerTerminalPanel from './components/terminal/ServerTerminalPanel.vue'
import {
  api,
  clearAccessToken,
  setAccessToken,
  taskAttachmentURL,
  taskWebSocketURL,
  type AuthStatus,
  type Approval,
  type ExternalPath,
  type Provider,
  type ProviderModelOption,
  type Skill,
  type Task,
  type TaskEvent,
  type TurnAttachmentInput,
  type Workspace,
  type WorkspaceFileContent,
  type WorkspaceFileItem,
} from './api'

type ChatBlock = {
  key: string
  role: 'user' | 'assistant' | 'system'
  title: string
  content: string
  sequence: number
  turnId?: string
  attachments?: ChatAttachment[]
  continuation?: {
    canContinue: boolean
    prompt: string
    reason: string
  }
}

type ChatAttachment = {
  id: string
  name: string
  mimeType: string
  kind: 'image' | 'file'
  size: number
}

type ChatTimelineItem = {
  key: string
  kind: 'message' | 'execution' | 'summary'
  block?: ChatBlock
  group?: ExecutionGroup
  summary?: TurnSummary
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
  status: 'running' | 'completed' | 'failed' | 'waiting' | 'blocked' | 'paused' | 'interrupted' | 'stopped' | 'idle'
  steps: ExecutionStep[]
  lastSequence: number
  startedAt?: string
  endedAt?: string
  contextUsage?: {
    estimatedTokens: number
    tokenBudget: number
    contextWindowTokens: number
    messageCount?: number
    toolCount?: number
    round?: number
    phase?: number
    source?: string
    truncated: boolean
    hasTaskSummary: boolean
    selectedFilePaths: string[]
  }
  contextSummary?: {
    summary: string
    contentHash: string
    chars: number
  }
  contract?: TaskContract
  ledger?: HypothesisLedger
  plan?: TaskPlan
  replay?: ReplayAssessment
}

type TaskContract = {
  goal: string
  taskType: string
  stage: string
  doneWhen: string[]
  missingInputs: string[]
}

type HypothesisItem = {
  id: string
  claim: string
  status: string
  missingEvidence: string[]
}

type EvidenceItem = {
  id: string
  hypothesisId: string
  kind: string
  operation: string
  source: string
  resource: string
  summary: string
  outcome: string
}

type HypothesisLedger = {
  hypotheses: HypothesisItem[]
  evidence: EvidenceItem[]
}

type TaskPlanStep = {
  id: string
  position: number
  title: string
  description: string
  status: string
  gateType: string
  acceptance: string[]
}

type TaskPlan = {
  id: string
  status: string
  version: number
  steps: TaskPlanStep[]
}

type ReplayAssessment = {
  score: number
  turns: number
  repeatedReads: number
  validations: number
  endToEndVerified: boolean
  findings: string[]
}

type ContextSnapshot = {
  usage?: ExecutionGroup['contextUsage']
  summary?: ExecutionGroup['contextSummary']
  turnLabel: string
}

type SummaryFile = {
  path: string
  displayPath: string
  action: string
  bytes: number
  additions: number
  deletions: number
}

type SummaryCommand = {
  command: string
  status: 'passed' | 'failed' | string
  summary?: string
  truncated?: boolean
}

type TurnSummary = {
  key: string
  turnId: string
  sequence: number
  changedFiles: SummaryFile[]
  validations: SummaryCommand[]
  commands: SummaryCommand[]
  raw: Record<string, unknown>
}

type TurnOutcome = {
  status: 'completed' | 'blocked' | 'failed' | 'interrupted'
  label: string
  tone: 'success' | 'warning' | 'error'
}

type ComposerAttachment = TurnAttachmentInput & {
  id: string
  size: number
  kind: 'image' | 'file'
}

const providers = ref<Provider[]>([])
const skills = ref<Skill[]>([])
const workspaces = ref<Workspace[]>([])
const tasks = ref<Task[]>([])
const events = ref<TaskEvent[]>([])
const approvals = ref<Approval[]>([])
const externalPaths = ref<ExternalPath[]>([])
const workspaceFiles = ref<WorkspaceFileItem[]>([])
const workspaceFilePath = ref('')
const selectedWorkspaceFilePath = ref('')
const workspaceFileContent = ref<WorkspaceFileContent | null>(null)
const fileBrowserLoading = ref(false)
const fileContentLoading = ref(false)
const filePreviewOpen = ref(false)
const selectedWorkspaceId = ref('')
const selectedTaskId = ref('')
const nowTick = ref(Date.now())
const rightTab = ref('files')
const loading = ref(false)
const authReady = ref(false)
const authUnlocked = ref(false)
const authSubmitting = ref(false)
const authPIN = ref('')
const authError = ref('')
const authLockedUntil = ref(0)
const authStatus = ref<AuthStatus | null>(null)
const wsConnected = ref(false)
const taskModalOpen = ref(false)
const taskSubmitting = ref(false)
const editingTaskId = ref('')
const providerModalOpen = ref(false)
const providerSubmitting = ref(false)
const editingProviderId = ref('')
const providerModelOptions = ref<ProviderModelOption[]>([])
const providerModelsLoading = ref(false)
const providerModelsError = ref('')
const skillInstalling = ref(false)
const skillInstallURL = ref('')
const skillUploadInput = ref<HTMLInputElement>()
const workspaceModalOpen = ref(false)
const workspaceSubmitting = ref(false)
const editingWorkspaceId = ref('')
const leftPanelWidth = ref(272)
const rightPanelWidth = ref(420)
const leftPanelCollapsed = ref(false)
const rightPanelCollapsed = ref(true)
const executionOpenKeys = ref<string[]>([])
const settingsOpenKey = ref('appearance')
const waterPetEnabled = ref(loadWaterPetEnabled())
const productName = '若水'
const productTagline = '可驾驭的私有 AI 编程助手'
const brandMarkSrc = '/favicon.svg'
const themeStorageKey = 'water-ui-theme'
const waterPetEnabledStorageKey = 'water-ui-water-pet-enabled'
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
let taskEventFrame: number | undefined
const pendingTaskEvents = new Map<string, TaskEvent>()
const taskSocketReconnectBaseMS = 800
const taskSocketReconnectMaxMS = 8000
const terminalPanelMinWidth = 560
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
const summaryRawMode = reactive<Record<string, boolean>>({})
const summaryExpanded = reactive<Record<string, boolean>>({})
const assistantTypingTimers = new Map<string, number>()
const assistantMarkdownCache = new Map<string, { content: string; html: string }>()
const assistantTypingDelayMS = 24
const assistantTypingMaxBatch = 16
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
  timeoutSeconds: 30,
  streamIdleTimeoutSeconds: 60,
  maxRetries: 2,
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

const authPinForm = reactive({
  currentPin: '',
  newPin: '',
})

const taskTitle = ref('')
const userInput = ref('')
const composerAttachments = ref<ComposerAttachment[]>([])
const attachmentInputRef = ref<HTMLInputElement | null>(null)
const composerDragging = ref(false)
const maxComposerAttachments = 6
const maxComposerAttachmentBytes = 8 * 1024 * 1024
const maxComposerAttachmentsBytes = 20 * 1024 * 1024
const chatBodyRef = ref<HTMLElement | null>(null)
const customCursorRef = ref<HTMLElement | null>(null)
const customCursorSupported = ref(false)
const customCursorVisible = ref(false)
const customCursorPressed = ref(false)
const customCursorInteractive = ref(false)
const waterPetPoked = ref(false)
const waterPetMoodIndex = ref(0)
const waterPetDragging = ref(false)
const waterPetSuppressClick = ref(false)
const waterPetWalking = ref(false)
const waterPetTurning = ref(false)
const waterPetGaitPhase = ref(0)
const waterPetFacing = ref(1)
const waterPetLean = ref(0)
const waterPetX = ref(0)
const waterPetY = ref(0)
let customCursorMediaQuery: MediaQueryList | undefined
let customCursorFrame: number | undefined
let customCursorPendingX = 0
let customCursorPendingY = 0
let chatScrollFrame: number | undefined
let chatScrollQueued = false
let chatScrollBehavior: ScrollBehavior = 'auto'
let waterPetTimer: number | undefined
let waterPetMoveTimer: number | undefined
let waterPetWalkTimer: number | undefined
const waterPetStorageKey = 'water-ui-pet-position'
let waterPetDrag:
  | {
      pointerId: number
      offsetX: number
      offsetY: number
      startX: number
      startY: number
      moved: boolean
    }
  | undefined

const selectedWorkspace = computed(() =>
  workspaces.value.find((item) => item.id === selectedWorkspaceId.value),
)
const authLockRemainingSeconds = computed(() => {
  if (!authLockedUntil.value) return 0
  return Math.max(0, Math.ceil((authLockedUntil.value - nowTick.value) / 1000))
})
const authLockRemainingText = computed(() => {
  const seconds = authLockRemainingSeconds.value
  if (seconds >= 60) return `${Math.ceil(seconds / 60)} 分钟`
  return `${seconds} 秒`
})
const selectedTask = computed(() => tasks.value.find((item) => item.id === selectedTaskId.value))
const defaultProvider = computed(() => providers.value.find((item) => item.isDefault))
const activeProvider = computed(() => {
  const workspaceProviderId = selectedWorkspace.value?.defaultProviderId
  return providers.value.find((item) => item.id === workspaceProviderId) ?? defaultProvider.value
})
const activeProviderId = computed(() => activeProvider.value?.id ?? '')
const activeProviderScopeLabel = computed(() => {
  if (!activeProvider.value) return '未配置'
  if (selectedWorkspace.value?.defaultProviderId === activeProvider.value.id) {
    return '当前工作区默认'
  }
  if (defaultProvider.value?.id === activeProvider.value.id) {
    return '全局默认'
  }
  return '回退生效'
})
const statusText = computed(() => {
  if (!selectedWorkspace.value) return '未选择工作区'
  if (!selectedTask.value) return '选择或创建一个任务'
  return latestTaskStatusText(events.value)
})
const selectedTaskIsLive = computed(() =>
  ['执行中', '思考', '审批', '回复'].some((label) => statusText.value.includes(label)),
)
const selectedTaskIsThinking = computed(() =>
  statusText.value.includes('思考') || statusText.value.includes('执行中'),
)
const selectedTaskIsCompleted = computed(() => statusText.value.includes('完成'))
const composerPlaceholder = computed(() =>
  selectedTaskIsLive.value
    ? '可以先输入下一步，当前任务完成后再发送'
    : `让${productName}修改、解释或规划你的项目`,
)
const canSubmitTurn = computed(() =>
  Boolean(
    selectedTaskId.value &&
      (userInput.value.trim() || composerAttachments.value.length > 0) &&
      !selectedTaskIsLive.value,
  ),
)
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
const workspaceFilePathLabel = computed(() => workspaceFilePath.value || '根目录')
const workspaceFileParentPath = computed(() => parentWorkspacePath(workspaceFilePath.value))
const workspaceFileModalTitle = computed(() => {
  const path = workspaceFileContent.value?.path || selectedWorkspaceFilePath.value
  return path || '文件预览'
})
const workspaceFileLanguage = computed(() => detectFileLanguage(workspaceFileModalTitle.value))
const workspaceFileDisplayContent = computed(() => {
  return workspaceFileContent.value?.content ?? ''
})
const workspaceFilePreviewHint = computed(() =>
  workspaceFileContent.value?.truncated ? '文件过大，当前仅显示前 512 KiB 预览' : '',
)
const workspaceFilePreviewLines = computed(() => {
  const lines = workspaceFileDisplayContent.value.split('\n')
  return (lines.length > 0 ? lines : ['']).map((line, index) => ({
    number: index + 1,
    html: highlightCode(line || ' ', workspaceFileLanguage.value),
  }))
})
const latestContextUsage = computed(() => latestExecutionGroup.value?.contextUsage)
const latestTaskContract = computed(() => latestExecutionGroup.value?.contract)
const latestHypothesisLedger = computed(() => latestExecutionGroup.value?.ledger)
const latestTaskPlan = computed(() => latestExecutionGroup.value?.plan)
const latestReplayAssessment = computed(() => latestExecutionGroup.value?.replay)
const latestContextSnapshot = computed<ContextSnapshot>(() => ({
  usage: latestExecutionGroup.value?.contextUsage,
  summary: latestExecutionGroup.value?.contextSummary,
  turnLabel: latestExecutionGroup.value?.subtitle || '最近一轮',
}))
const contextBudgetRatio = 0.8
const contextHeaderBudget = computed(() => {
  const usage = latestContextUsage.value
  if (usage?.tokenBudget) return usage.tokenBudget
  const contextWindow = activeProvider.value?.contextWindowTokens || 8192
  return Math.floor(contextWindow * contextBudgetRatio)
})
const contextHeaderText = computed(() => {
  const usage = latestContextUsage.value
  const budget = contextHeaderBudget.value
  if (usage) {
    return `上下文估算 ${formatTokenCount(usage.estimatedTokens)} / ${formatTokenCount(budget)}`
  }
  return `上下文估算 -- / ${formatTokenCount(budget)}`
})
const contextHeaderTitle = computed(() =>
  latestExecutionGroup.value && latestContextUsage.value
    ? `本轮 ${contextUsageText(latestExecutionGroup.value)}\n按当前实际请求的消息和工具定义估算；不同模型 tokenizer 可能略有差异。`
    : '当前 Provider 的本轮上下文预算；发送请求后按消息和工具定义估算',
)
const contextHeaderPercent = computed(() => {
  const usage = latestContextUsage.value
  const budget = contextHeaderBudget.value
  if (!usage || budget <= 0) return 0
  return Math.min(100, Math.round((usage.estimatedTokens / budget) * 100))
})
const contextHeaderStyle = computed(() => ({
  '--context-used': `${contextHeaderPercent.value}%`,
}))

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
        attachments: parseChatAttachments(payload.attachments),
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
		if (item.type === 'turn.failed' || item.type === 'turn.interrupted' || item.type === 'turn.blocked' || item.type === 'turn.paused') {
      const continuationPrompt = String(payload.continuationPrompt ?? '').trim()
      const isContinuableInterrupted =
			(item.type === 'turn.paused' || item.type === 'turn.interrupted') &&
        (payload.canContinue === true || String(payload.message ?? '').includes('工具调用轮次达到上限'))
      blocks.push({
        key: item.eventId,
        role: 'system',
        title:
			item.type === 'turn.blocked'
				? '等待补充信息'
				: item.type === 'turn.paused'
					? String(payload.reason ?? '').startsWith('semantic_')
						? '连续无进展，任务未完成'
						: '本轮预算结束，任务未完成'
            : isContinuableInterrupted
            ? '本轮已停止，可继续'
            : item.type === 'turn.interrupted'
              ? '运行已中断'
              : '运行失败',
        content:
			item.type === 'turn.blocked'
				? blockedMessage(payload)
				: item.type === 'turn.paused'
					? String(payload.message ?? '本轮预算结束，任务计划仍未完成。')
            : String(payload.message ?? (item.type === 'turn.interrupted' ? 'Agent 执行已中断' : 'Agent 执行失败')),
        sequence: item.sequence,
        turnId: item.turnId,
        continuation:
          isContinuableInterrupted
            ? {
                canContinue: true,
                prompt: continuationPrompt || '继续上一轮任务，沿用已有结果继续推进，不要重复已经完成的工作。',
                reason: String(payload.reason ?? ''),
              }
            : undefined,
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

const summaryByTurn = computed(() => {
  const summaries = new Map<string, TurnSummary>()
  for (const item of events.value) {
    if (item.type !== 'turn.summary' || !item.turnId) continue
    const payload = payloadRecord(item)
    summaries.set(item.turnId, {
      key: item.eventId,
      turnId: item.turnId,
      sequence: item.sequence,
      changedFiles: parseSummaryFiles(payload.changedFiles),
      validations: parseSummaryCommands(payload.validations),
      commands: parseSummaryCommands(payload.commands),
      raw: payload,
    })
  }
  return summaries
})

const turnOutcomeByTurn = computed(() => {
  const outcomes = new Map<string, TurnOutcome>()
  for (const item of events.value) {
    if (!item.turnId) continue
    if (item.type === 'turn.completed') {
      outcomes.set(item.turnId, {
        status: 'completed',
        label: '已完成',
        tone: 'success',
      })
      continue
    }
    if (item.type === 'turn.failed') {
      outcomes.set(item.turnId, {
        status: 'failed',
        label: '已失败',
        tone: 'error',
      })
      continue
    }
    if (item.type === 'turn.blocked') {
      outcomes.set(item.turnId, {
        status: 'blocked',
        label: '等待你补充信息',
        tone: 'warning',
      })
      continue
    }
    if (item.type === 'turn.interrupted') {
      const payload = payloadRecord(item)
      outcomes.set(item.turnId, {
        status: 'interrupted',
        label: payload.canContinue === true ? '已中断，可继续' : '已中断',
        tone: 'warning',
      })
    }
  }
  return outcomes
})

const chatTimeline = computed(() => {
  const items: ChatTimelineItem[] = []
  const renderedExecutionGroups = new Set<string>()
  const renderedSummaries = new Set<string>()
  for (const block of chatBlocks.value) {
    items.push({
      key: `message-${block.key}`,
      kind: 'message',
      block,
    })
    if (block.turnId && block.role === 'user') {
      const group = executionGroupByTurn.value.get(block.turnId)
      if (group && !renderedExecutionGroups.has(group.key)) {
        renderedExecutionGroups.add(group.key)
        items.push({
          key: `execution-${group.key}`,
          kind: 'execution',
          group,
        })
      }
    }
    if (block.turnId && block.role === 'assistant') {
      const summary = summaryByTurn.value.get(block.turnId)
      if (summary && !renderedSummaries.has(summary.key)) {
        renderedSummaries.add(summary.key)
        items.push({
          key: `summary-${summary.key}`,
          kind: 'summary',
          summary,
        })
      }
    }
  }
  for (const summary of [...summaryByTurn.value.values()].sort((a, b) => a.sequence - b.sequence)) {
    if (renderedSummaries.has(summary.key)) continue
    items.push({
      key: `summary-${summary.key}`,
      kind: 'summary',
      summary,
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
const effectiveRightPanelWidth = computed(() =>
  rightTab.value === 'terminal' && !rightPanelCollapsed.value
    ? Math.max(rightPanelWidth.value, terminalPanelMinWidth)
    : rightPanelWidth.value,
)
const shellStyle = computed(() => ({
  '--left-panel-width': `${leftPanelCollapsed.value ? 48 : leftPanelWidth.value}px`,
  '--right-panel-width': `${rightPanelCollapsed.value ? 48 : effectiveRightPanelWidth.value}px`,
}))
const waterPetMessages = ['上善若水', '缓而不怠', '清流已就位', '稳住上下文']
const waterPetMessage = computed(() => waterPetMessages[waterPetMoodIndex.value % waterPetMessages.length])
const waterPetStyle = computed(() => ({
  left: `${waterPetX.value}px`,
  top: `${waterPetY.value}px`,
  '--water-pet-face': `${waterPetFacing.value}`,
  '--water-pet-lean': `${waterPetLean.value}deg`,
  '--water-pet-tilt': `${waterPetFacing.value * 7}deg`,
}))
const waterPetGaitClass = computed(() => (waterPetGaitPhase.value % 2 === 0 ? 'gait-a' : 'gait-b'))
const authStatusText = computed(() =>
  authStatus.value?.authenticated ? '已解锁' : authStatus.value?.configured === false ? '未启用访问锁' : '已上锁',
)
const canChangePIN = computed(() => Boolean(authPinForm.currentPin.trim() && authPinForm.newPin.trim()))

function loadThemeName(): ThemeName {
  if (typeof window === 'undefined') return 'ruoshui'
  const stored = window.localStorage.getItem(themeStorageKey)
  return themeOptions.some((item) => item.name === stored) ? (stored as ThemeName) : 'ruoshui'
}

function loadWaterPetEnabled() {
  if (typeof window === 'undefined') return true
  const stored = window.localStorage.getItem(waterPetEnabledStorageKey)
  if (stored === null) return true
  return stored !== 'false'
}

function selectTheme(name: ThemeName) {
  selectedThemeName.value = name
}

function clampPanelWidth(value: number, side: PanelSide) {
  const min = side === 'left' ? 220 : 280
  const max = side === 'left' ? 420 : 760
  return Math.min(Math.max(value, min), max)
}

function canUseCustomCursor() {
  return typeof window !== 'undefined' && window.matchMedia('(pointer: fine)').matches
}

function flushCustomCursorFrame() {
  customCursorFrame = undefined
  const cursor = customCursorRef.value
  if (!cursor) return
  cursor.style.transform = `translate3d(${customCursorPendingX}px, ${customCursorPendingY}px, 0) translate(-2px, -1px)`
}

function scheduleCustomCursorUpdate(x: number, y: number) {
  customCursorPendingX = x
  customCursorPendingY = y
  if (customCursorFrame !== undefined) return
  customCursorFrame = window.requestAnimationFrame(flushCustomCursorFrame)
}

function setCustomCursorState(event: PointerEvent) {
  if (!customCursorSupported.value || (event.pointerType !== 'mouse' && event.pointerType !== 'pen')) {
    customCursorVisible.value = false
    customCursorInteractive.value = false
    customCursorPressed.value = false
    return
  }
  customCursorVisible.value = true
  scheduleCustomCursorUpdate(event.clientX, event.clientY)
  const target = event.target instanceof Element ? event.target : null
  customCursorInteractive.value = Boolean(
    target?.closest(
      'button, a, input, textarea, select, [role="button"], [contenteditable="true"], .ant-btn, .ant-select, .ant-input, .ant-input-affix-wrapper, .ant-switch, .ant-checkbox, .sidebar-toggle, .resize-handle, .task-item, .provider-card, .workspace-row, .file-row, .approval-row, .water-pet',
    ),
  )
}

function handleCustomCursorDown(event: PointerEvent) {
  setCustomCursorState(event)
  if (event.pointerType === 'mouse' || event.pointerType === 'pen') {
    customCursorPressed.value = true
  }
}

function handleCustomCursorMove(event: PointerEvent) {
  setCustomCursorState(event)
}

function handleCustomCursorUp() {
  customCursorPressed.value = false
}

function hideCustomCursor() {
  customCursorVisible.value = false
  customCursorInteractive.value = false
  customCursorPressed.value = false
  if (customCursorFrame !== undefined) {
    window.cancelAnimationFrame(customCursorFrame)
    customCursorFrame = undefined
  }
}

function syncCustomCursorSupport() {
  customCursorSupported.value = canUseCustomCursor() && authUnlocked.value
  if (typeof document !== 'undefined') {
    document.body.classList.toggle('water-cursor-mode', customCursorSupported.value)
  }
  if (!customCursorSupported.value) {
    hideCustomCursor()
  }
}

function handleCustomCursorOut(event: MouseEvent) {
  if (event.relatedTarget) return
  hideCustomCursor()
}

function stopWaterPetTimers() {
  if (waterPetTimer !== undefined) {
    window.clearTimeout(waterPetTimer)
    waterPetTimer = undefined
  }
  if (waterPetMoveTimer !== undefined) {
    window.clearTimeout(waterPetMoveTimer)
    waterPetMoveTimer = undefined
  }
  if (waterPetWalkTimer !== undefined) {
    window.clearTimeout(waterPetWalkTimer)
    waterPetWalkTimer = undefined
  }
}

function pokeWaterPet() {
  if (waterPetSuppressClick.value) return
  waterPetMoodIndex.value += 1
  waterPetPoked.value = true
  if (waterPetTimer !== undefined) {
    window.clearTimeout(waterPetTimer)
  }
  waterPetTimer = window.setTimeout(() => {
    waterPetPoked.value = false
    waterPetTimer = undefined
  }, 900)
}

function clampWaterPetPosition(x: number, y: number) {
  const width = 78
  const height = 92
  const margin = 12
  const maxX = Math.max(margin, window.innerWidth - width - margin)
  const maxY = Math.max(margin, window.innerHeight - height - margin)
  return {
    x: Math.min(Math.max(x, margin), maxX),
    y: Math.min(Math.max(y, margin), maxY),
  }
}

function moveWaterPetTo(x: number, y: number) {
  const next = clampWaterPetPosition(x, y)
  waterPetX.value = next.x
  waterPetY.value = next.y
  if (typeof window !== 'undefined') {
    window.localStorage.setItem(waterPetStorageKey, JSON.stringify(next))
  }
}

function stopWaterPetWalk() {
  waterPetWalking.value = false
  waterPetTurning.value = false
  waterPetLean.value = 0
  if (waterPetWalkTimer !== undefined) {
    window.clearTimeout(waterPetWalkTimer)
    waterPetWalkTimer = undefined
  }
}

function nextWaterPetWalkTarget() {
  const width = 78
  const nearLeft = waterPetX.value < 120
  const nearRight = waterPetX.value > window.innerWidth - width - 120
  const direction = nearLeft ? 1 : nearRight ? -1 : Math.random() > 0.5 ? 1 : -1
  const stepX = direction * (82 + Math.round(Math.random() * 88))
  const stepY = Math.round((Math.random() - 0.5) * 32)
  return clampWaterPetPosition(waterPetX.value + stepX, waterPetY.value + stepY)
}

function setDefaultWaterPetPosition() {
  const stored = loadWaterPetPosition()
  if (stored) {
    moveWaterPetTo(stored.x, stored.y)
    return
  }
  moveWaterPetTo(
    window.innerWidth - (rightPanelCollapsed.value ? 48 : effectiveRightPanelWidth.value) - 116,
    window.innerHeight - 212,
  )
}

function loadWaterPetPosition() {
  if (typeof window === 'undefined') return undefined
  const raw = window.localStorage.getItem(waterPetStorageKey)
  if (!raw) return undefined
  try {
    const parsed = JSON.parse(raw) as { x?: number; y?: number }
    if (typeof parsed.x !== 'number' || typeof parsed.y !== 'number') return undefined
    return clampWaterPetPosition(parsed.x, parsed.y)
  } catch {
    return undefined
  }
}

function scheduleWaterPetMove(delay = 900 + Math.round(Math.random() * 700)) {
  if (!waterPetEnabled.value) return
  if (waterPetMoveTimer !== undefined) {
    window.clearTimeout(waterPetMoveTimer)
  }
  waterPetMoveTimer = window.setTimeout(() => {
    waterPetMoveTimer = undefined
    if (!waterPetDragging.value && !selectedTaskIsLive.value) {
      const target = nextWaterPetWalkTarget()
      walkWaterPetTo(target.x, target.y)
    }
    scheduleWaterPetMove(3000 + Math.round(Math.random() * 2600))
  }, delay)
}

function walkWaterPetTo(x: number, y: number) {
  if (!waterPetEnabled.value || waterPetDragging.value || selectedTaskIsLive.value) return
  stopWaterPetWalk()
  const startX = waterPetX.value
  const startY = waterPetY.value
  const target = clampWaterPetPosition(x, y)
  const dx = target.x - startX
  const dy = target.y - startY
  const distance = Math.hypot(dx, dy)
  if (distance < 24) return
  waterPetFacing.value = dx >= 0 ? 1 : -1
  waterPetLean.value = waterPetFacing.value * -4
  const steps = Math.max(6, Math.min(14, Math.round(distance / 16)))
  const stepDuration = distance < 100 ? 132 : 112
  waterPetTurning.value = true
  waterPetGaitPhase.value = 0
  let step = 0
  const advance = () => {
    if (waterPetDragging.value || selectedTaskIsLive.value) {
      stopWaterPetWalk()
      return
    }
    step += 1
    waterPetGaitPhase.value += 1
    const t = step / steps
    const eased = t * t * (3 - 2 * t)
    const wave = Math.sin(t * Math.PI * steps)
    const bob = Math.abs(wave) * (distance < 100 ? 2.4 : 3.6)
    const sway = wave * waterPetFacing.value * (distance < 100 ? 1.4 : 2.2)
    const leanEase = Math.sin(Math.PI * t)
    waterPetLean.value = waterPetFacing.value * (3 + leanEase * 5)
    moveWaterPetTo(startX + dx * eased + sway, startY + dy * eased - bob)
    if (step < steps) {
      waterPetWalkTimer = window.setTimeout(advance, stepDuration)
      return
    }
    moveWaterPetTo(target.x, target.y)
    stopWaterPetWalk()
  }
  waterPetWalkTimer = window.setTimeout(() => {
    waterPetTurning.value = false
    waterPetWalking.value = true
    advance()
  }, 140)
}

function startWaterPetDrag(event: PointerEvent) {
  stopWaterPetWalk()
  const target = event.currentTarget as HTMLElement
  waterPetDrag = {
    pointerId: event.pointerId,
    offsetX: event.clientX - waterPetX.value,
    offsetY: event.clientY - waterPetY.value,
    startX: event.clientX,
    startY: event.clientY,
    moved: false,
  }
  waterPetDragging.value = true
  target.setPointerCapture(event.pointerId)
  window.addEventListener('pointermove', dragWaterPet)
  window.addEventListener('pointerup', stopWaterPetDrag)
  window.addEventListener('pointercancel', stopWaterPetDrag)
}

function dragWaterPet(event: PointerEvent) {
  if (!waterPetDrag || event.pointerId !== waterPetDrag.pointerId) return
  if (Math.hypot(event.clientX - waterPetDrag.startX, event.clientY - waterPetDrag.startY) > 3) {
    waterPetDrag.moved = true
  }
  moveWaterPetTo(event.clientX - waterPetDrag.offsetX, event.clientY - waterPetDrag.offsetY)
}

function stopWaterPetDrag(event: PointerEvent) {
  if (!waterPetDrag || event.pointerId !== waterPetDrag.pointerId) return
  const moved = waterPetDrag.moved
  window.removeEventListener('pointermove', dragWaterPet)
  window.removeEventListener('pointerup', stopWaterPetDrag)
  window.removeEventListener('pointercancel', stopWaterPetDrag)
  waterPetDragging.value = false
  waterPetDrag = undefined
  if (moved) {
    waterPetSuppressClick.value = true
    window.setTimeout(() => {
      waterPetSuppressClick.value = false
    }, 0)
  }
  if (!selectedTaskIsLive.value) {
    scheduleWaterPetMove()
  }
}

function keepWaterPetInBounds() {
  moveWaterPetTo(waterPetX.value, waterPetY.value)
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

function payloadStringArray(payload: Record<string, unknown>, key: string) {
  const value = payload[key]
  if (!Array.isArray(value)) return []
  return value.map((item) => String(item)).filter(Boolean)
}

function parseSummaryFiles(value: unknown): SummaryFile[] {
  if (!Array.isArray(value)) return []
  return value.map((raw) => {
    const item = (raw ?? {}) as Record<string, unknown>
    return {
      path: String(item.path ?? ''),
      displayPath: String(item.displayPath ?? item.path ?? ''),
      action: String(item.action ?? 'modified'),
      bytes: numberValue(item.bytes),
      additions: numberValue(item.additions),
      deletions: numberValue(item.deletions),
    }
  }).filter((item) => item.path || item.displayPath)
}

function parseChatAttachments(value: unknown): ChatAttachment[] {
  if (!Array.isArray(value)) return []
  return value
    .map((raw) => {
      const item = (raw ?? {}) as Record<string, unknown>
      return {
        id: String(item.id ?? ''),
        name: String(item.name ?? '附件'),
        mimeType: String(item.mimeType ?? 'application/octet-stream'),
        kind: item.kind === 'image' ? 'image' as const : 'file' as const,
        size: numberValue(item.size),
      }
    })
    .filter((item) => item.id)
}

function chatAttachmentURL(block: ChatBlock, attachment: ChatAttachment) {
  if (!block.turnId || !selectedTaskId.value) return ''
  return taskAttachmentURL(selectedTaskId.value, attachment.id)
}

function parseSummaryCommands(value: unknown): SummaryCommand[] {
  if (!Array.isArray(value)) return []
  return value.map((raw) => {
    const item = (raw ?? {}) as Record<string, unknown>
    return {
      command: String(item.command ?? ''),
      status: String(item.status ?? 'passed'),
      summary: String(item.summary ?? ''),
      truncated: item.truncated === true,
    }
  }).filter((item) => item.command)
}

function numberValue(value: unknown) {
  const number = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(number) ? number : 0
}

function updateExecutionTiming(group: ExecutionGroup, item: TaskEvent) {
  if (item.type === 'turn.started') {
    group.startedAt = item.createdAt
  }
	if (item.type === 'turn.completed' || item.type === 'turn.blocked' || item.type === 'turn.paused' || item.type === 'turn.failed' || item.type === 'turn.interrupted') {
    group.endedAt = item.createdAt
  }
}

function updateExecutionGroup(group: ExecutionGroup, item: TaskEvent) {
  const payload = payloadRecord(item)
  if (item.type === 'context.pack.built' || item.type === 'context.request.estimated') {
    const previous = group.contextUsage
    group.contextUsage = {
      estimatedTokens: payloadNumber(payload, 'estimatedTokens'),
      tokenBudget: payloadNumber(payload, 'tokenBudget'),
      contextWindowTokens: payloadNumber(payload, 'contextWindowTokens'),
      messageCount: payloadNumber(payload, 'messageCount'),
      toolCount: payloadNumber(payload, 'toolCount'),
      round: payloadNumber(payload, 'round'),
      phase: payloadNumber(payload, 'phase'),
      source: String(payload.source ?? item.type),
      truncated: payload.truncated === true || previous?.truncated === true,
      hasTaskSummary: payload.hasTaskSummary === true || previous?.hasTaskSummary === true,
      selectedFilePaths:
        payloadStringArray(payload, 'selectedFilePaths').length > 0
          ? payloadStringArray(payload, 'selectedFilePaths')
          : previous?.selectedFilePaths ?? [],
    }
    return
  }
  if (item.type === 'context.summary.updated') {
    group.contextSummary = {
      summary: String(payload.summary ?? ''),
      contentHash: String(payload.contentHash ?? ''),
      chars: payloadNumber(payload, 'chars'),
    }
    return
  }
  if (item.type === 'agent.contract.updated') {
    group.contract = {
      goal: String(payload.goal ?? ''),
      taskType: String(payload.taskType ?? ''),
      stage: String(payload.stage ?? ''),
      doneWhen: payloadStringArray(payload, 'doneWhen'),
      missingInputs: payloadStringArray(payload, 'missingInputs'),
    }
    return
  }
  if (item.type === 'agent.ledger.snapshot') {
    group.ledger = parseHypothesisLedger(payload)
    return
  }
  if (item.type === 'agent.plan.snapshot' || item.type === 'agent.plan.created') {
    group.plan = parseTaskPlan(payload)
    return
  }
  if (item.type === 'agent.replay.assessed') {
    group.replay = parseReplayAssessment(payload)
    return
  }
  if (item.type === 'turn.started') {
    group.title = String(payload.userInput ?? '用户输入')
    group.subtitle = `第 ${payload.sequence ?? ''} 轮`
    group.status = 'running'
    return
  }
  if (
    item.type === 'context.turn.compacted' ||
    item.type === 'agent.loop.guard.triggered' ||
    item.type === 'tool.call.corrected' ||
    item.type === 'tool.call.cached' ||
    item.type === 'agent.hypothesis.updated' ||
    item.type === 'agent.evidence.recorded' ||
    item.type === 'agent.progress.assessed' ||
		item.type === 'agent.replan.requested' ||
		item.type === 'agent.execution.phase.continued' ||
		item.type === 'agent.final_tool_calls.deferred' ||
		item.type === 'agent.deferred_tool_calls.executing' ||
    item.type === 'agent.ledger.snapshot' ||
    item.type === 'agent.plan.created' ||
		item.type === 'agent.plan.history.recovered' ||
    item.type === 'agent.plan.snapshot' ||
    item.type === 'agent.plan.step.completed' ||
    item.type === 'agent.replay.assessed' ||
    item.type === 'agent.tool_calls.detected' ||
    item.type === 'approval.continuation.started' ||
    item.type === 'tool.call.started' ||
    item.type === 'tool.completed'
  ) {
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
	if (item.type === 'turn.blocked') {
		group.status = 'blocked'
		return
	}
	if (item.type === 'turn.paused') {
		group.status = 'paused'
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
      return makeExecutionStep(item, '上下文已组装', summarizeContextPack(payload), 'normal')
    case 'context.request.estimated':
      return undefined
    case 'context.turn.compacted':
      return makeExecutionStep(item, '单轮上下文已压缩', summarizeTurnContextCompaction(payload), 'normal')
    case 'agent.loop.guard.triggered':
      return makeExecutionStep(item, '循环保护已触发，正在收敛', String(payload.message ?? '工具循环已停止，正在整理最终答复'), 'warning')
    case 'context.summary.updated':
      return makeExecutionStep(item, '上下文摘要已更新', summarizeContextSummary(payload), 'success')
    case 'agent.contract.updated':
      return makeExecutionStep(item, '任务契约已更新', summarizeContract(payload), 'normal')
    case 'agent.ledger.snapshot':
      return undefined
    case 'agent.plan.created':
      return makeExecutionStep(item, '执行计划已建立', summarizePlan(payload), 'normal')
		case 'agent.plan.history.recovered':
			return makeExecutionStep(item, '已恢复历史验收进度', String(payload.message ?? ''), 'success')
    case 'agent.plan.snapshot':
      return undefined
    case 'agent.plan.step.completed':
      return makeExecutionStep(item, '计划步骤已验收', summarizePlanProgress(payload), 'success')
    case 'agent.replay.assessed':
      return makeExecutionStep(item, '历史任务已回放', summarizeReplayAssessment(payload), replayTone(payload))
    case 'agent.hypothesis.updated':
      return makeExecutionStep(item, '假设状态已更新', summarizeHypothesis(payload), hypothesisTone(payload))
    case 'agent.evidence.recorded':
      return makeExecutionStep(item, '证据已记录', summarizeEvidenceRecord(payload), evidenceTone(payload))
    case 'agent.progress.assessed':
      if (payload.newInformation === true) return undefined
      return makeExecutionStep(item, '信息增益检查', summarizeProgressAssessment(payload), 'warning')
		case 'agent.replan.requested':
			return makeExecutionStep(item, '已要求重新规划', String(payload.message ?? '当前路径没有新增证据'), 'warning')
		case 'agent.execution.phase.continued':
			return makeExecutionStep(
				item,
				'已自动继续执行',
				`进入第 ${payloadNumber(payload, 'nextPhase')} 个执行阶段\n${String(payload.message ?? '')}`,
				'normal',
			)
		case 'agent.final_tool_calls.deferred':
			return makeExecutionStep(
				item,
				'已保留下一工具动作',
				String(payload.message ?? '若水将在下一执行阶段优先执行该工具动作'),
				'normal',
			)
		case 'agent.deferred_tool_calls.executing':
			return makeExecutionStep(
				item,
				'正在承接上一阶段动作',
				String(payload.message ?? '正在执行上一阶段保留的工具动作'),
				'normal',
			)
    case 'tool.call.corrected':
      return makeExecutionStep(
        item,
        '工具调用已纠正',
        `${String(payload.from ?? 'tool')} -> ${String(payload.to ?? 'tool')}`,
        'warning',
      )
    case 'tool.call.cached':
      return makeExecutionStep(
        item,
        '复用已有工具结果',
        String(payload.message ?? '资源没有已知变化，已复用本轮结果'),
        'normal',
      )
    case 'turn.started':
    case 'agent.message.delta':
    case 'agent.message.completed':
    case 'turn.completed':
      return undefined
    case 'agent.tool_calls.detected':
      return makeExecutionStep(item, '准备调用工具', summarizeToolCalls(payload), 'running')
    case 'approval.continuation.started':
      return makeExecutionStep(item, '审批通过，继续执行', summarizeApprovalContinuation(payload), 'running')
    case 'tool.call.started':
      return makeExecutionStep(item, '工具开始', summarizeToolStart(payload), 'running')
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
		case 'turn.blocked':
			return makeExecutionStep(item, '等待补充信息', blockedMessage(payload), 'warning')
		case 'turn.paused':
			return makeExecutionStep(
				item,
				String(payload.reason ?? '').startsWith('semantic_') ? '连续无进展，任务未完成' : '本轮预算结束，任务未完成',
				String(payload.message ?? ''),
				'warning',
			)
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

function summarizeContextPack(payload: Record<string, unknown>) {
  const estimated = payloadNumber(payload, 'estimatedTokens')
  const budget = payloadNumber(payload, 'tokenBudget')
  const files = payloadStringArray(payload, 'selectedFilePaths')
  const index = (payload.indexStats ?? {}) as Record<string, unknown>
  const indexed = payloadNumber(index, 'filesIndexed')
  const changed = payloadNumber(index, 'filesChanged')
  const lines = [
    budget > 0 ? `预算 ${estimated} / ${budget} tokens` : `估算 ${estimated} tokens`,
    payload.hasTaskSummary === true ? '已带入任务滚动摘要' : '暂无任务滚动摘要',
    files.length > 0 ? `选中文件摘要 ${files.length} 个` : '未选中文件摘要',
  ]
  if (indexed > 0) lines.push(`代码索引 ${indexed} 个文件${changed > 0 ? `，更新 ${changed} 个` : ''}`)
  if (payload.truncated === true) lines.push('已按预算截断低优先级上下文')
  return lines.join('\n')
}

function summarizeContextSummary(payload: Record<string, unknown>) {
  const chars = payloadNumber(payload, 'chars')
  const summary = compactText(String(payload.summary ?? ''), 360)
  return [`任务滚动摘要 ${chars} 字`, summary].filter(Boolean).join('\n')
}

function summarizeTurnContextCompaction(payload: Record<string, unknown>) {
  const original = payloadNumber(payload, 'originalEstimatedTokens')
  const compacted = payloadNumber(payload, 'compactedEstimatedTokens')
  const dropped = payloadNumber(payload, 'droppedMessages')
  const round = payloadNumber(payload, 'round')
  return [
    `第 ${round} 回合，估算 ${formatTokenCount(original)} -> ${formatTokenCount(compacted)}`,
    `已把 ${dropped} 条较早消息压缩为执行摘要，完整事件仍保留`,
  ].join('\n')
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

function summarizeToolStart(payload: Record<string, unknown>) {
  const name = String(payload.name ?? 'tool')
  const toolCallId = String(payload.toolCallId ?? '')
  return compactText([name, toolCallId].filter(Boolean).join('\n'))
}

function summarizeApprovalContinuation(payload: Record<string, unknown>) {
  const toolName = String(payload.toolName ?? 'tool')
  const approvalId = String(payload.approvalId ?? '')
  return compactText([toolName, approvalId].filter(Boolean).join('\n'))
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

function blockedMessage(payload: Record<string, unknown>) {
  const missingInputs = payloadStringArray(payload, 'missingInputs')
  if (missingInputs.length === 0) return String(payload.message ?? '继续任务需要补充关键信息。')
  return `继续任务需要补充：\n${missingInputs.map((item) => `- ${item}`).join('\n')}`
}

function summarizeContract(payload: Record<string, unknown>) {
  const stage = contractStageText(String(payload.stage ?? ''))
  const goal = String(payload.goal ?? '')
  return compactText([stage, goal].filter(Boolean).join('\n'))
}

function parseHypothesisLedger(payload: Record<string, unknown>): HypothesisLedger {
  const hypotheses = (Array.isArray(payload.hypotheses) ? payload.hypotheses : []).map((raw) => {
    const item = raw as Record<string, unknown>
    return {
      id: String(item.id ?? ''),
      claim: String(item.claim ?? ''),
      status: String(item.status ?? 'open'),
      missingEvidence: payloadStringArray(item, 'missingEvidence'),
    }
  })
  const evidence = (Array.isArray(payload.evidence) ? payload.evidence : []).map((raw) => {
    const item = raw as Record<string, unknown>
    return {
      id: String(item.id ?? ''),
      hypothesisId: String(item.hypothesisId ?? ''),
      kind: String(item.kind ?? ''),
      operation: String(item.operation ?? ''),
      source: String(item.source ?? ''),
      resource: String(item.resource ?? ''),
      summary: String(item.summary ?? ''),
      outcome: String(item.outcome ?? 'neutral'),
    }
  })
  return { hypotheses, evidence }
}

function parseTaskPlan(payload: Record<string, unknown>): TaskPlan {
  const steps = (Array.isArray(payload.steps) ? payload.steps : []).map((raw) => {
    const item = raw as Record<string, unknown>
    return {
      id: String(item.id ?? ''),
      position: payloadNumber(item, 'position'),
      title: String(item.title ?? ''),
      description: String(item.description ?? ''),
      status: String(item.status ?? 'pending'),
      gateType: String(item.gateType ?? ''),
      acceptance: payloadStringArray(item, 'acceptance'),
    }
  })
  return {
    id: String(payload.id ?? ''),
    status: String(payload.status ?? 'in_progress'),
    version: payloadNumber(payload, 'version'),
    steps,
  }
}

function parseReplayAssessment(payload: Record<string, unknown>): ReplayAssessment {
  return {
    score: payloadNumber(payload, 'score'),
    turns: payloadNumber(payload, 'turns'),
    repeatedReads: payloadNumber(payload, 'repeatedReads'),
    validations: payloadNumber(payload, 'validations'),
    endToEndVerified: payload.endToEndVerified === true,
    findings: payloadStringArray(payload, 'findings'),
  }
}

function summarizePlan(payload: Record<string, unknown>) {
  const plan = parseTaskPlan(payload)
  const current = plan.steps.find((item) => item.status === 'in_progress')
  return current ? `${plan.steps.length} 个步骤\n当前：${current.title}` : `${plan.steps.length} 个步骤`
}

function summarizePlanProgress(payload: Record<string, unknown>) {
  const next = (payload.nextStep ?? {}) as Record<string, unknown>
  if (payload.planComplete === true) return '所有计划步骤已通过验收'
  return `下一步：${String(next.title ?? '继续执行计划')}`
}

function summarizeReplayAssessment(payload: Record<string, unknown>) {
  const findings = payloadStringArray(payload, 'findings')
  return [
    `执行质量 ${payloadNumber(payload, 'score')} 分 · ${payloadNumber(payload, 'turns')} 轮`,
    `重复读取 ${payloadNumber(payload, 'repeatedReads')} 次 · 验证 ${payloadNumber(payload, 'validations')} 次`,
    ...findings,
  ].join('\n')
}

function replayTone(payload: Record<string, unknown>): ExecutionStep['tone'] {
  const score = payloadNumber(payload, 'score')
  if (score >= 80) return 'success'
  if (score >= 50) return 'warning'
  return 'danger'
}

function planStepStatusText(status: string) {
  const labels: Record<string, string> = {
    pending: '待执行',
    in_progress: '进行中',
    completed: '已验收',
    blocked: '已阻塞',
  }
  return labels[status] ?? status
}

function summarizeHypothesis(payload: Record<string, unknown>) {
  return compactText(
    [hypothesisStatusText(String(payload.status ?? 'open')), String(payload.claim ?? '')]
      .filter(Boolean)
      .join('\n'),
  )
}

function hypothesisTone(payload: Record<string, unknown>): ExecutionStep['tone'] {
  const status = String(payload.status ?? '')
  if (status === 'resolved' || status === 'supported') return 'success'
  if (status === 'blocked' || status === 'contradicted') return 'warning'
  return 'normal'
}

function summarizeEvidenceRecord(payload: Record<string, unknown>) {
  const purpose = String(payload.purpose ?? '')
  const resource = String(payload.resource ?? '')
  const summary = String(payload.summary ?? '')
  return compactText([purpose, resource, summary].filter(Boolean).join('\n'))
}

function evidenceTone(payload: Record<string, unknown>): ExecutionStep['tone'] {
  const outcome = String(payload.outcome ?? '')
  if (outcome === 'supports') return 'success'
  if (outcome === 'contradicts') return 'warning'
  return 'normal'
}

function summarizeProgressAssessment(payload: Record<string, unknown>) {
  const resource = String(payload.resource ?? '')
  const repeatCount = payloadNumber(payload, 'repeatCount')
  const action = String(payload.action ?? '')
  const actionText = action === 'stop_no_progress' ? '停止重复路径' : '要求更换假设或验证方式'
  return `${resource || '当前资源'} 已出现 ${repeatCount} 次相同证据，${actionText}`
}

function hypothesisStatusText(status: string) {
  const labels: Record<string, string> = {
    open: '待验证',
    supported: '已有支持',
    contradicted: '出现反证',
    blocked: '等待输入',
    resolved: '已解决',
  }
  return labels[status] ?? status
}

function evidenceOutcomeText(outcome: string) {
  const labels: Record<string, string> = {
    supports: '支持',
    contradicts: '反证',
    neutral: '记录',
  }
  return labels[outcome] ?? outcome
}

function contractStageText(stage: string) {
  const labels: Record<string, string> = {
    understanding: '理解目标',
    collecting_evidence: '收集证据',
    implementing: '执行修改',
    verifying: '验证结果',
    finalizing: '整理结果',
    blocked: '等待输入',
    completed: '已完成',
  }
  return labels[stage] ?? (stage || '准备中')
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
	if (latestTurnEvents.some((item) => item.type === 'turn.paused')) return '任务未完成，可从当前计划继续'
  if (latestTurnEvents.some((item) => item.type === 'turn.blocked')) return '等待你补充信息'
  if (latestTurnEvents.some((item) => item.type === 'turn.interrupted')) return '最近一轮已中断'
  if (latestTurnEvents.some((item) => item.type === 'turn.completed')) return '已完成最近一轮'
  const hasPendingApproval =
    latestTurnEvents.some((item) => item.type === 'approval.requested') &&
    !latestTurnEvents.some((item) => item.type === 'approval.resolved')
  if (hasPendingApproval) return '等待审批'
  if (latestTurnEvents.some((item) => item.type === 'agent.message.delta')) return 'Agent 正在回复'
  if (
    latestTurnEvents.some(
      (item) =>
        item.type === 'agent.tool_calls.detected' ||
        item.type === 'approval.continuation.started' ||
        item.type === 'tool.call.started' ||
        item.type === 'tool.completed',
    )
  ) {
    return '工具执行中'
  }
  return 'Agent 正在思考'
}

function taskLifecycleText(status: string) {
  if (status === 'created') return '未开始'
  if (status === 'active') return '已开始'
  if (status === 'archived') return '已归档'
  return status
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

function formatTokenCount(value: number) {
  if (value >= 10000) return `${Math.round(value / 1000)}k`
  if (value >= 1000) return `${(value / 1000).toFixed(2)}k`
  return String(value)
}

function parentWorkspacePath(path: string) {
  if (!path) return ''
  const parts = path.split('/').filter(Boolean)
  parts.pop()
  return parts.join('/')
}

function formatFileSize(size: number) {
  if (size >= 1024 * 1024) return `${(size / 1024 / 1024).toFixed(1)} MB`
  if (size >= 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${size} B`
}

function detectFileLanguage(path: string) {
  const name = path.toLowerCase()
  const ext = name.split('.').pop() ?? ''
  if (name.endsWith('.vue')) return 'vue'
  if (['ts', 'tsx'].includes(ext)) return 'typescript'
  if (['js', 'jsx', 'mjs', 'cjs'].includes(ext)) return 'javascript'
  if (['java', 'kt', 'kts'].includes(ext)) return 'java'
  if (['go'].includes(ext)) return 'go'
  if (['json'].includes(ext)) return 'json'
  if (['yml', 'yaml'].includes(ext)) return 'yaml'
  if (['css', 'scss', 'less'].includes(ext)) return 'css'
  if (['html', 'xml', 'svg'].includes(ext)) return 'markup'
  if (['md', 'markdown'].includes(ext)) return 'markdown'
  if (['sql'].includes(ext)) return 'sql'
  if (['sh', 'bash', 'zsh'].includes(ext)) return 'shell'
  return ext || 'text'
}

function escapeHTML(value: string) {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;')
}

function highlightCode(content: string, language: string) {
  if (!content) return ''
  if (language === 'markup' || language === 'vue') return highlightMarkup(content)
  if (language === 'json') return highlightJSON(content)
  if (language === 'yaml') return highlightYAML(content)
  return highlightCommonCode(content, language)
}

function highlightToken(raw: string, tone: string) {
  return `<span class="code-token ${tone}">${escapeHTML(raw)}</span>`
}

function highlightCommonCode(content: string, language: string) {
  const keywordsByLanguage: Record<string, string[]> = {
    java: ['class', 'interface', 'enum', 'public', 'private', 'protected', 'static', 'final', 'void', 'new', 'return', 'if', 'else', 'for', 'while', 'switch', 'case', 'try', 'catch', 'throw', 'throws', 'extends', 'implements', 'package', 'import', 'true', 'false', 'null'],
    typescript: ['const', 'let', 'var', 'function', 'return', 'if', 'else', 'for', 'while', 'switch', 'case', 'type', 'interface', 'class', 'extends', 'implements', 'import', 'from', 'export', 'default', 'async', 'await', 'true', 'false', 'null', 'undefined'],
    javascript: ['const', 'let', 'var', 'function', 'return', 'if', 'else', 'for', 'while', 'switch', 'case', 'class', 'extends', 'import', 'from', 'export', 'default', 'async', 'await', 'true', 'false', 'null', 'undefined'],
    go: ['package', 'import', 'func', 'type', 'struct', 'interface', 'return', 'if', 'else', 'for', 'range', 'switch', 'case', 'defer', 'go', 'chan', 'select', 'true', 'false', 'nil'],
    css: ['display', 'position', 'grid', 'flex', 'color', 'background', 'border', 'padding', 'margin', 'font', 'width', 'height'],
    shell: ['if', 'then', 'else', 'fi', 'for', 'do', 'done', 'case', 'esac', 'export', 'local', 'function'],
    sql: ['select', 'from', 'where', 'insert', 'into', 'update', 'delete', 'create', 'table', 'alter', 'join', 'left', 'right', 'inner', 'values', 'set', 'group', 'order', 'by', 'limit'],
  }
  const keywords = new Set([...(keywordsByLanguage[language] ?? []), ...(keywordsByLanguage.javascript ?? [])])
  const keywordPattern = [...keywords].sort((a, b) => b.length - a.length).join('|')
  const tokenPattern = new RegExp(
    `("(?:\\\\.|[^"\\\\])*"|'(?:\\\\.|[^'\\\\])*'|\\\`(?:\\\\.|[^\\\`\\\\])*\\\`|\\/\\/.*|\\/\\*[\\s\\S]*?\\*\\/|#.*$|\\b(?:${keywordPattern})\\b|\\b\\d+(?:\\.\\d+)?\\b)`,
    'gim',
  )
  let lastIndex = 0
  let output = ''
  for (const match of content.matchAll(tokenPattern)) {
    const token = match[0]
    const index = match.index ?? 0
    output += escapeHTML(content.slice(lastIndex, index))
    if (token.startsWith('//') || token.startsWith('/*') || token.startsWith('#')) {
      output += highlightToken(token, 'comment')
    } else if (token.startsWith('"') || token.startsWith("'") || token.startsWith('`')) {
      output += highlightToken(token, 'string')
    } else if (/^\d/.test(token)) {
      output += highlightToken(token, 'number')
    } else {
      output += highlightToken(token, 'keyword')
    }
    lastIndex = index + token.length
  }
  output += escapeHTML(content.slice(lastIndex))
  return output
}

function highlightJSON(content: string) {
  const formatted = (() => {
    try {
      return JSON.stringify(JSON.parse(content), null, 2)
    } catch {
      return content
    }
  })()
  const escaped = escapeHTML(formatted)
  return escaped.replace(
    /(&quot;(?:\\.|[^"\\])*&quot;)(\s*:)?|\b(true|false|null)\b|-?\b\d+(?:\.\d+)?\b/g,
    (token, stringToken: string, colon: string, literal: string) => {
      if (stringToken) {
        return colon
          ? `<span class="code-token property">${stringToken}</span>${colon}`
          : `<span class="code-token string">${stringToken}</span>`
      }
      if (literal) return `<span class="code-token keyword">${literal}</span>`
      return `<span class="code-token number">${token}</span>`
    },
  )
}

function highlightYAML(content: string) {
  return escapeHTML(content)
    .replace(/^(\s*[\w.-]+)(:)/gm, '<span class="code-token property">$1</span>$2')
    .replace(/(#.*)$/gm, '<span class="code-token comment">$1</span>')
    .replace(/\b(true|false|null)\b/g, '<span class="code-token keyword">$1</span>')
}

function highlightMarkup(content: string) {
  return escapeHTML(content)
    .replace(/(&lt;!--[\s\S]*?--&gt;)/g, '<span class="code-token comment">$1</span>')
    .replace(/(&lt;\/?[\w:-]+)([\s\S]*?)(&gt;)/g, (_match, open: string, attrs: string, close: string) => {
      const highlightedAttrs = attrs.replace(
        /([\w:-]+)(=)(&quot;.*?&quot;|&#39;.*?&#39;)/g,
        '<span class="code-token property">$1</span>$2<span class="code-token string">$3</span>',
      )
      return `<span class="code-token keyword">${open}</span>${highlightedAttrs}<span class="code-token keyword">${close}</span>`
    })
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
	if (group.status === 'blocked') return '需要补充信息后继续'
	if (group.status === 'paused') return '本轮预算结束，任务尚未完成'
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
  const content = visibleChatContent(block)
  const cached = assistantMarkdownCache.get(block.key)
  if (cached?.content === content) return cached.html
  const html = markdown.render(content)
  assistantMarkdownCache.set(block.key, { content, html })
  return html
}

function isAssistantTyping(block: ChatBlock) {
  if (block.role !== 'assistant') return false
  return (assistantRenderedContent[block.key] ?? '').length < (assistantTargetContent[block.key] ?? block.content).length
}

function isAssistantStreaming(block: ChatBlock) {
  if (block.role !== 'assistant') return false
  const group = block.turnId ? executionGroupByTurn.value.get(block.turnId) : undefined
  return group?.status === 'running' || group?.status === 'waiting' || isAssistantTyping(block)
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

function isSummaryRawMode(summary: TurnSummary) {
  return summaryRawMode[summary.key] === true
}

function isSummaryExpanded(summary: TurnSummary) {
  return summaryExpanded[summary.key] === true
}

function toggleSummaryExpanded(summary: TurnSummary) {
  summaryExpanded[summary.key] = !isSummaryExpanded(summary)
}

function toggleSummaryRawMode(summary: TurnSummary) {
  summaryRawMode[summary.key] = !summaryRawMode[summary.key]
}

function summaryCommands(summary: TurnSummary) {
  return summary.validations.length > 0 ? summary.validations : summary.commands
}

function summaryCommandTitle(summary: TurnSummary) {
  return summary.validations.length > 0 ? '验证结果' : '命令结果'
}

function summaryFileTotals(summary: TurnSummary) {
  return summary.changedFiles.reduce(
    (totals, file) => ({
      additions: totals.additions + file.additions,
      deletions: totals.deletions + file.deletions,
    }),
    { additions: 0, deletions: 0 },
  )
}

function commandStatusText(status: string) {
  return status === 'failed' ? '失败' : '通过'
}

function commandStatusTone(status: string) {
  return status === 'failed' ? 'error' : 'success'
}

function fileActionText(action: string) {
  return action === 'created' ? '新增' : '修改'
}

function summaryMarkdown(summary: TurnSummary) {
  const lines: string[] = []
  const totals = summaryFileTotals(summary)
  if (summary.changedFiles.length > 0) {
    lines.push(`已编辑 ${summary.changedFiles.length} 个文件`)
    lines.push(`+${totals.additions} -${totals.deletions}`)
    for (const file of summary.changedFiles) {
      lines.push(`- ${file.displayPath || file.path} +${file.additions} -${file.deletions}`)
    }
  }
  const commands = summaryCommands(summary)
  if (commands.length > 0) {
    if (lines.length > 0) lines.push('')
    lines.push(`${summaryCommandTitle(summary)}：`)
    for (const command of commands) {
      lines.push(`- \`${command.command}\` ${commandStatusText(command.status)}`)
      if (command.summary) lines.push(`  ${command.summary}`)
    }
  }
  return lines.join('\n')
}

async function copySummaryMarkdown(summary: TurnSummary) {
  try {
    await navigator.clipboard.writeText(summaryMarkdown(summary))
    message.success('任务产物 Markdown 已复制')
  } catch {
    message.error('复制失败')
  }
}

async function copyWorkspaceFileContent() {
  if (!workspaceFileDisplayContent.value) return
  try {
    await navigator.clipboard.writeText(workspaceFileDisplayContent.value)
    message.success('文件内容已复制')
  } catch {
    message.error('复制失败')
  }
}

function triggerDownload(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

async function downloadWorkspaceFile() {
  if (!selectedWorkspaceId.value || !workspaceFileContent.value?.path) return
  try {
    const path = workspaceFileContent.value.path
    const blob = await api.downloadWorkspaceFile(selectedWorkspaceId.value, path)
    triggerDownload(blob, path.split('/').pop() || 'workspace-file')
  } catch (err) {
    showError(err)
  }
}

async function downloadWorkspaceArchive(item: Workspace) {
  try {
    const blob = await api.downloadWorkspaceArchive(item.id)
    triggerDownload(blob, `${item.name || 'workspace'}.zip`)
  } catch (err) {
    showError(err)
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
    assistantMarkdownCache.delete(key)
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

  const remaining = target.length - rendered.length
  const batchSize = Math.min(assistantTypingMaxBatch, Math.max(1, Math.ceil(remaining / 8)))
  let nextLength = Math.min(target.length, rendered.length + batchSize)
  const finalCodeUnit = target.charCodeAt(nextLength - 1)
  if (nextLength < target.length && finalCodeUnit >= 0xd800 && finalCodeUnit <= 0xdbff) {
    nextLength += 1
  }
  assistantRenderedContent[key] = target.slice(0, nextLength)
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
  chatScrollBehavior = behavior
  if (chatScrollQueued) return
  chatScrollQueued = true
  void nextTick(() => {
    if (!chatScrollQueued) return
    chatScrollFrame = window.requestAnimationFrame(() => {
      chatScrollFrame = undefined
      chatScrollQueued = false
      const target = chatBodyRef.value
      if (!target) return
      target.scrollTo({
        top: target.scrollHeight,
        behavior: chatScrollBehavior,
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
		case 'blocked':
			return '待补充'
		case 'paused':
			return '未完成'
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
		case 'blocked':
			return 'warning'
		case 'paused':
			return 'warning'
    case 'interrupted':
      return 'warning'
    case 'waiting':
      return 'warning'
    default:
      return 'default'
  }
}

function turnOutcomeLabel(turnId?: string) {
  if (!turnId) return ''
  return turnOutcomeByTurn.value.get(turnId)?.label ?? ''
}

function turnOutcomeTone(turnId?: string) {
  if (!turnId) return 'default'
  return turnOutcomeByTurn.value.get(turnId)?.tone ?? 'default'
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
  providerForm.timeoutSeconds = 30
  providerForm.streamIdleTimeoutSeconds = 60
  providerForm.maxRetries = 2
}

function resetProviderModelOptions() {
  providerModelOptions.value = []
  providerModelsError.value = ''
  providerModelsLoading.value = false
}

function openProviderModal(item?: Provider) {
  resetProviderModelOptions()
  if (item) {
    editingProviderId.value = item.id
    providerForm.name = item.name
    providerForm.baseUrl = item.baseUrl
    providerForm.model = item.model
    providerForm.apiKey = ''
    providerForm.isDefault = item.isDefault
    providerForm.enabled = item.enabled
    providerForm.contextWindowTokens = item.contextWindowTokens || 8192
    providerForm.timeoutSeconds = Math.max(1, Math.round((item.timeoutMs || 30000) / 1000))
    providerForm.streamIdleTimeoutSeconds = Math.max(1, Math.round((item.streamIdleTimeoutMs || 60000) / 1000))
    providerForm.maxRetries = item.maxRetries ?? 2
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

function authLockedStatus(): AuthStatus {
  return {
    configured: true,
    authenticated: false,
    locked: true,
    sessionExpiresAt: '',
    lastUnlockedAt: '',
    pinLockedUntil: '',
    pinRetryAfterSeconds: 0,
  }
}

function syncAuthLockout(status: AuthStatus) {
  const lockedUntil = Date.parse(status.pinLockedUntil || '')
  if (Number.isFinite(lockedUntil) && lockedUntil > Date.now()) {
    authLockedUntil.value = lockedUntil
    return
  }
  if (status.pinRetryAfterSeconds > 0) {
    authLockedUntil.value = Date.now() + status.pinRetryAfterSeconds * 1000
    return
  }
  authLockedUntil.value = 0
}

function clearWorkspaceState() {
  closeTaskSocket()
  providers.value = []
  workspaces.value = []
  tasks.value = []
  events.value = []
  approvals.value = []
  externalPaths.value = []
  workspaceFiles.value = []
  workspaceFilePath.value = ''
  selectedWorkspaceFilePath.value = ''
  workspaceFileContent.value = null
  selectedWorkspaceId.value = ''
  selectedTaskId.value = ''
}

function handleAuthExpired() {
  clearAccessToken()
  authUnlocked.value = false
  authReady.value = true
  authStatus.value = authLockedStatus()
  clearWorkspaceState()
}

async function refreshAuthStatus() {
  try {
    const status = await api.authStatus()
    authStatus.value = status
    syncAuthLockout(status)
    authUnlocked.value = status.authenticated || status.configured === false || !status.locked
    if (!authUnlocked.value) {
      clearAccessToken()
      clearWorkspaceState()
    }
  } catch (err) {
    clearAccessToken()
    authStatus.value = authLockedStatus()
    authLockedUntil.value = 0
    authUnlocked.value = false
    authError.value = err instanceof Error ? err.message : '检查访问状态失败'
  } finally {
    authReady.value = true
  }
}

async function unlockAccess() {
  const pin = authPIN.value.trim()
  if (!pin || authLockRemainingSeconds.value > 0) return
  authSubmitting.value = true
  authError.value = ''
  try {
    const session = await api.unlockAuth(pin)
    setAccessToken(session.accessToken)
    authPIN.value = ''
    await refreshAuthStatus()
    if (authUnlocked.value) {
      message.success('若水已解锁')
      await refreshAll()
    }
  } catch (err) {
    const retryAfterSeconds = errorDataNumber(err, 'retryAfterSeconds')
    if (errorStatus(err) === 429 && retryAfterSeconds > 0) {
      authLockedUntil.value = Date.now() + retryAfterSeconds * 1000
      authPIN.value = ''
      authError.value = ''
      return
    }
    if (errorStatus(err) === 401) {
      authPIN.value = ''
      authError.value = 'PIN 不正确'
      return
    }
    authError.value = err instanceof Error ? err.message : '解锁失败'
  } finally {
    authSubmitting.value = false
  }
}

async function lockAccess() {
  try {
    await api.lockAuth()
  } catch {
    // Local locking should still happen even if the server-side session already expired.
  }
  handleAuthExpired()
  message.success('若水已上锁')
}

async function changeAuthPIN() {
  if (!canChangePIN.value) return
  try {
    const session = await api.changeAuthPin(authPinForm.currentPin.trim(), authPinForm.newPin.trim())
    setAccessToken(session.accessToken)
    authPinForm.currentPin = ''
    authPinForm.newPin = ''
    await refreshAuthStatus()
    message.success('访问 PIN 已更新')
  } catch (err) {
    if (errorStatus(err) === 401) {
      message.error('当前 PIN 不正确')
      return
    }
    showError(err)
  }
}

async function refreshAll() {
  if (!authUnlocked.value) return
  loading.value = true
  try {
    const [providerData, workspaceData, skillData] = await Promise.all([
      api.listProviders(),
      api.listWorkspaces(),
      api.listSkills(),
    ])
    providers.value = providerData.items
    workspaces.value = workspaceData.items
    skills.value = skillData.items
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
    workspaceFiles.value = []
    workspaceFilePath.value = ''
    selectedWorkspaceFilePath.value = ''
    workspaceFileContent.value = null
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
  void refreshWorkspaceFiles(workspaceFilePath.value)
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

function selectProviderModel(item: ProviderModelOption) {
  providerForm.model = item.id
}

function providerRowStatusLabel(item: Provider) {
  if (selectedWorkspace.value?.defaultProviderId === item.id) {
    return '当前工作区使用'
  }
  if (defaultProvider.value?.id === item.id) {
    return '全局默认'
  }
  if (item.enabled) {
    return '启用'
  }
  return '停用'
}

async function loadProviderModels() {
  if (!providerForm.baseUrl.trim() && !editingProviderId.value) {
    providerModelsError.value = '请先填写 Base URL'
    return
  }

  providerModelsLoading.value = true
  providerModelsError.value = ''
  try {
    const body: Record<string, unknown> = {}
    if (editingProviderId.value) {
      body.providerId = editingProviderId.value
    }
    if (providerForm.baseUrl.trim()) {
      body.baseUrl = providerForm.baseUrl.trim()
    }
    if (providerForm.apiKey.trim()) {
      body.apiKey = providerForm.apiKey.trim()
    }

    const data = await api.listProviderModels(body)
    const seen = new Set<string>()
    providerModelOptions.value = data.items
      .filter((item) => item.id.trim())
      .filter((item) => {
        if (seen.has(item.id)) return false
        seen.add(item.id)
        return true
      })
    if (!providerForm.model.trim() && providerModelOptions.value.length > 0) {
      providerForm.model = providerModelOptions.value[0].id
    }
    if (providerModelOptions.value.length === 0) {
      providerModelsError.value = '接口未返回模型列表，可以继续手动填写'
      return
    }
    message.success(`已获取 ${providerModelOptions.value.length} 个模型`)
  } catch (err) {
    providerModelsError.value = err instanceof Error ? err.message : '获取模型失败'
    if (errorStatus(err) === 401) {
      showError(err)
    }
  } finally {
    providerModelsLoading.value = false
  }
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
    timeoutMs: Math.max(1, providerForm.timeoutSeconds || 30) * 1000,
    streamIdleTimeoutMs: Math.max(1, providerForm.streamIdleTimeoutSeconds || 60) * 1000,
    maxRetries: Math.max(0, providerForm.maxRetries || 0),
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

async function setGlobalDefaultProvider(item: Provider) {
  try {
    await api.setDefaultProvider(item.id)
    message.success(`已设为全局默认：${item.name}`)
    await refreshAll()
  } catch (err) {
    showError(err)
  }
}

async function setWorkspaceDefaultProvider(item: Provider) {
  if (!selectedWorkspace.value) {
    message.warning('请先选择工作区')
    return
  }
  try {
    await api.updateWorkspace(selectedWorkspace.value.id, {
      name: selectedWorkspace.value.name,
      rootPath: selectedWorkspace.value.rootPath,
      defaultProviderId: item.id,
      permissionMode: selectedWorkspace.value.permissionMode,
      trusted: selectedWorkspace.value.trusted,
    })
    message.success(`当前工作区已切换到：${item.name}`)
    await refreshAll()
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

async function refreshSkills() {
  const data = await api.listSkills()
  skills.value = data.items
}

async function installSkillArchive(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (!file.name.toLowerCase().endsWith('.zip')) {
    message.warning('请选择 ZIP 格式的 Skill 包')
    return
  }
  skillInstalling.value = true
  try {
    const installed = await api.installSkillArchive(file)
    message.success(`${installed.name} 已安装，默认保持停用`)
    await refreshSkills()
  } catch (err) {
    showError(err)
  } finally {
    skillInstalling.value = false
  }
}

async function installSkillFromURL() {
  const url = skillInstallURL.value.trim()
  if (!url) return
  skillInstalling.value = true
  try {
    const installed = await api.installSkillURL(url)
    skillInstallURL.value = ''
    message.success(`${installed.name} 已安装，默认保持停用`)
    await refreshSkills()
  } catch (err) {
    showError(err)
  } finally {
    skillInstalling.value = false
  }
}

async function toggleSkill(item: Skill) {
  try {
    await api.setSkillEnabled(item.id, !item.enabled)
    await refreshSkills()
    message.success(`${item.name} 已${item.enabled ? '停用' : '启用'}`)
  } catch (err) {
    showError(err)
  }
}

async function deleteSkill(item: Skill) {
  try {
    await api.deleteSkill(item.id)
    await refreshSkills()
    message.success('Skill 已删除')
  } catch (err) {
    showError(err)
  }
}

function skillSourceLabel(item: Skill) {
  return item.source === 'url' ? 'URL' : item.source === 'upload' ? '上传' : item.source
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

async function refreshWorkspaceFiles(path = '') {
  if (!selectedWorkspaceId.value) return
  fileBrowserLoading.value = true
  try {
    const data = await api.listWorkspaceFiles(selectedWorkspaceId.value, path)
    workspaceFilePath.value = data.path === '.' ? '' : data.path
    workspaceFiles.value = data.items
  } catch (err) {
    workspaceFiles.value = []
    showError(err)
  } finally {
    fileBrowserLoading.value = false
  }
}

async function openWorkspaceFile(item: WorkspaceFileItem) {
  if (item.isDir) {
    selectedWorkspaceFilePath.value = ''
    workspaceFileContent.value = null
    filePreviewOpen.value = false
    await refreshWorkspaceFiles(item.path)
    return
  }
  if (!selectedWorkspaceId.value) return
  selectedWorkspaceFilePath.value = item.path
  workspaceFileContent.value = null
  filePreviewOpen.value = true
  fileContentLoading.value = true
  try {
    workspaceFileContent.value = await api.readWorkspaceFile(selectedWorkspaceId.value, item.path)
  } catch (err) {
    workspaceFileContent.value = null
    filePreviewOpen.value = false
    showError(err)
  } finally {
    fileContentLoading.value = false
  }
}

async function openWorkspaceFileParent() {
  selectedWorkspaceFilePath.value = ''
  workspaceFileContent.value = null
  filePreviewOpen.value = false
  await refreshWorkspaceFiles(workspaceFileParentPath.value)
}

function closeWorkspaceFilePreview() {
  filePreviewOpen.value = false
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
  if (!canSubmitTurn.value) return
  const input = userInput.value
  const attachments = [...composerAttachments.value]
  userInput.value = ''
  composerAttachments.value = []
  await submitTurn(input, attachments, () => {
    userInput.value = input
    composerAttachments.value = attachments
  })
}

async function continueTurn(block: ChatBlock) {
  const input = block.continuation?.prompt.trim()
  if (!selectedTaskId.value || !input) return
  await submitTurn(input, [], () => {
    userInput.value = input
  })
}

async function submitTurn(
  input: string,
  attachments: ComposerAttachment[],
  restoreInput: () => void,
) {
  if (selectedTaskIsLive.value) return
  scrollChatToBottom('smooth')
  try {
    await api.createTurn(
      selectedTaskId.value,
      input,
      attachments.map(({ name, mimeType, dataUrl }) => ({ name, mimeType, dataUrl })),
    )
    await refreshEvents()
    scrollChatToBottom('smooth')
  } catch (err) {
    restoreInput()
    showError(err)
  }
}

function openAttachmentPicker() {
  if (!selectedTaskId.value) {
    message.info('请先选择或创建任务')
    return
  }
  attachmentInputRef.value?.click()
}

function handleAttachmentInput(event: Event) {
  const input = event.target as HTMLInputElement
  void addComposerFiles(Array.from(input.files ?? []))
  input.value = ''
}

async function addComposerFiles(files: File[]) {
  if (files.length === 0) return
  const existing = composerAttachments.value
  const additions: ComposerAttachment[] = []
  let totalBytes = existing.reduce((sum, item) => sum + item.size, 0)
  for (const file of files) {
    if (existing.length + additions.length >= maxComposerAttachments) {
      message.warning(`一次最多添加 ${maxComposerAttachments} 个附件`)
      break
    }
    if (file.size > maxComposerAttachmentBytes) {
      message.warning(`${file.name} 超过 8 MiB`)
      continue
    }
    if (totalBytes + file.size > maxComposerAttachmentsBytes) {
      message.warning('附件总大小不能超过 20 MiB')
      break
    }
    const duplicate = [...existing, ...additions].some(
      (item) => item.name === file.name && item.size === file.size,
    )
    if (duplicate) continue
    try {
      const dataUrl = await readFileAsDataURL(file)
      additions.push({
        id: composerAttachmentID(),
        name: file.name,
        mimeType: file.type || attachmentMimeFromName(file.name),
        dataUrl,
        size: file.size,
        kind: file.type.startsWith('image/') ? 'image' : 'file',
      })
      totalBytes += file.size
    } catch {
      message.error(`${file.name} 读取失败`)
    }
  }
  composerAttachments.value = [...existing, ...additions]
}

function readFileAsDataURL(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

function composerAttachmentID() {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID()
  return `attachment-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function attachmentMimeFromName(name: string) {
  const extension = name.split('.').pop()?.toLowerCase() ?? ''
  const types: Record<string, string> = {
    css: 'text/css',
    csv: 'text/csv',
    doc: 'application/msword',
    docx: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
    go: 'text/plain',
    html: 'text/html',
    java: 'text/plain',
    js: 'text/javascript',
    json: 'application/json',
    md: 'text/markdown',
    pdf: 'application/pdf',
    ppt: 'application/vnd.ms-powerpoint',
    pptx: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
    py: 'text/x-python',
    ts: 'text/typescript',
    tsx: 'text/typescript',
    txt: 'text/plain',
    vue: 'text/plain',
    xls: 'application/vnd.ms-excel',
    xlsx: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
    xml: 'application/xml',
    yaml: 'application/yaml',
    yml: 'application/yaml',
  }
  return types[extension] ?? 'application/octet-stream'
}

function attachmentFormatLabel(name: string) {
  const extension = name.split('.').pop()?.toLowerCase() ?? ''
  return ['pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx'].includes(extension)
    ? extension.toUpperCase()
    : ''
}

function removeComposerAttachment(id: string) {
  composerAttachments.value = composerAttachments.value.filter((item) => item.id !== id)
}

function handleComposerDrop(event: DragEvent) {
  composerDragging.value = false
  const files = Array.from(event.dataTransfer?.files ?? [])
  if (files.length > 0) void addComposerFiles(files)
}

function handleComposerPaste(event: ClipboardEvent) {
  const files = Array.from(event.clipboardData?.files ?? [])
  if (files.length === 0) return
  event.preventDefault()
  void addComposerFiles(files)
}

function formatAttachmentSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} KiB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`
}

function handleComposerKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return
  event.preventDefault()
  if (!canSubmitTurn.value) return
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
  let latest = events.value.reduce((max, item) => Math.max(max, item.sequence ?? 0), 0)
  for (const item of pendingTaskEvents.values()) {
    latest = Math.max(latest, item.sequence ?? 0)
  }
  return latest
}

function flushTaskEvents() {
  if (taskEventFrame !== undefined) {
    window.cancelAnimationFrame(taskEventFrame)
    taskEventFrame = undefined
  }
  if (pendingTaskEvents.size === 0) return

  const incoming = Array.from(pendingTaskEvents.values())
  pendingTaskEvents.clear()
  const nextEvents = events.value.slice()
  const indexByID = new Map(nextEvents.map((item, index) => [item.eventId, index]))
  let approvalsChanged = false

  for (const item of incoming) {
    const index = indexByID.get(item.eventId)
    if (index !== undefined) {
      nextEvents[index] = item
    } else {
      indexByID.set(item.eventId, nextEvents.length)
      nextEvents.push(item)
      approvalsChanged ||= item.type.startsWith('approval.')
    }
  }

  nextEvents.sort((a, b) => a.sequence - b.sequence)
  events.value = nextEvents
  if (approvalsChanged) void refreshApprovals()
}

function queueTaskEvent(item: TaskEvent) {
  pendingTaskEvents.set(item.eventId, item)
  if (taskEventFrame !== undefined) return
  taskEventFrame = window.requestAnimationFrame(() => {
    taskEventFrame = undefined
    flushTaskEvents()
  })
}

function discardPendingTaskEvents() {
  pendingTaskEvents.clear()
  if (taskEventFrame === undefined) return
  window.cancelAnimationFrame(taskEventFrame)
  taskEventFrame = undefined
}

function clearTaskSocketReconnectTimer() {
  if (taskSocketReconnectTimer === undefined) return
  window.clearTimeout(taskSocketReconnectTimer)
  taskSocketReconnectTimer = undefined
}

function closeTaskSocket() {
  clearTaskSocketReconnectTimer()
  discardPendingTaskEvents()
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
    discardPendingTaskEvents()
  } else {
    flushTaskEvents()
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
    queueTaskEvent(item)
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

function errorStatus(err: unknown) {
  if (!err || typeof err !== 'object') return 0
  return Number((err as { status?: number }).status ?? 0)
}

function errorDataNumber(err: unknown, key: string) {
  if (!err || typeof err !== 'object') return 0
  const data = (err as { data?: unknown }).data
  if (!data || typeof data !== 'object') return 0
  return Number((data as Record<string, unknown>)[key] ?? 0)
}

function showError(err: unknown) {
  if (errorStatus(err) === 401) {
    handleAuthExpired()
    message.warning('访问会话已失效，请重新输入 PIN')
    return
  }
  message.error(err instanceof Error ? err.message : '操作失败')
}

watch(selectedWorkspaceId, () => {
  selectedTaskId.value = ''
  workspaceFilePath.value = ''
  selectedWorkspaceFilePath.value = ''
  workspaceFileContent.value = null
  filePreviewOpen.value = false
  workspaceFiles.value = []
  void refreshWorkspaceState()
})

watch(selectedTaskId, () => {
  composerAttachments.value = []
  composerDragging.value = false
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

watch(authUnlocked, () => {
  syncCustomCursorSupport()
})

watch(waterPetEnabled, (enabled) => {
  window.localStorage.setItem(waterPetEnabledStorageKey, String(enabled))
  if (!enabled) {
    stopWaterPetWalk()
    stopWaterPetTimers()
    return
  }
  if (!selectedTaskIsLive.value) {
    scheduleWaterPetMove(800)
  }
})

watch(selectedTaskIsLive, (isLive) => {
  if (isLive) {
    stopWaterPetWalk()
    if (waterPetMoveTimer !== undefined) {
      window.clearTimeout(waterPetMoveTimer)
      waterPetMoveTimer = undefined
    }
    return
  }
  if (waterPetEnabled.value) {
    scheduleWaterPetMove(800)
  }
})

watch(effectiveRightPanelWidth, () => {
  if (waterPetEnabled.value) {
    keepWaterPetInBounds()
  }
})

onMounted(() => {
  void (async () => {
    await refreshAuthStatus()
    if (authUnlocked.value) {
      await refreshAll()
    }
  })()
  setDefaultWaterPetPosition()
  if (waterPetEnabled.value) {
    scheduleWaterPetMove(800)
  }
  syncCustomCursorSupport()
  window.addEventListener('pointermove', handleCustomCursorMove, { passive: true })
  window.addEventListener('pointerdown', handleCustomCursorDown, { passive: true })
  window.addEventListener('pointerup', handleCustomCursorUp, { passive: true })
  window.addEventListener('pointercancel', handleCustomCursorUp, { passive: true })
  window.addEventListener('blur', hideCustomCursor)
  window.addEventListener('mouseout', handleCustomCursorOut)
  window.addEventListener('resize', keepWaterPetInBounds, { passive: true })
  customCursorMediaQuery = window.matchMedia('(pointer: fine)')
  customCursorMediaQuery.addEventListener('change', syncCustomCursorSupport)
})
const nowTickTimer = window.setInterval(() => {
  nowTick.value = Date.now()
}, 1000)
onBeforeUnmount(() => {
  stopPanelResize()
  discardPendingTaskEvents()
  clearAssistantTypingTimers()
  assistantMarkdownCache.clear()
  chatScrollQueued = false
  if (chatScrollFrame !== undefined) {
    window.cancelAnimationFrame(chatScrollFrame)
    chatScrollFrame = undefined
  }
  if (customCursorFrame !== undefined) {
    window.cancelAnimationFrame(customCursorFrame)
    customCursorFrame = undefined
  }
  if (waterPetTimer !== undefined) {
    window.clearTimeout(waterPetTimer)
  }
  stopWaterPetTimers()
  stopWaterPetWalk()
  window.removeEventListener('pointermove', dragWaterPet)
  window.removeEventListener('pointerup', stopWaterPetDrag)
  window.removeEventListener('pointercancel', stopWaterPetDrag)
  if (typeof document !== 'undefined') {
    document.body.classList.remove('water-cursor-mode')
  }
  window.removeEventListener('pointermove', handleCustomCursorMove)
  window.removeEventListener('pointerdown', handleCustomCursorDown)
  window.removeEventListener('pointerup', handleCustomCursorUp)
  window.removeEventListener('pointercancel', handleCustomCursorUp)
  window.removeEventListener('blur', hideCustomCursor)
  window.removeEventListener('mouseout', handleCustomCursorOut)
  window.removeEventListener('resize', keepWaterPetInBounds)
  customCursorMediaQuery?.removeEventListener('change', syncCustomCursorSupport)
  window.clearInterval(nowTickTimer)
  closeTaskSocket()
})
</script>

<template>
  <a-config-provider :theme="antdTheme">
    <main v-if="!authReady" class="auth-shell" :data-theme="selectedThemeName">
      <WaterAuthScene :theme="selectedThemeName" />
      <section class="auth-card">
        <div class="auth-brand">
          <span class="auth-brand-mark" aria-hidden="true">
            <img :src="brandMarkSrc" alt="" />
          </span>
          <div>
            <h1>{{ productName }}</h1>
            <p>{{ productTagline }}</p>
          </div>
        </div>
        <a-skeleton active :paragraph="{ rows: 2 }" />
      </section>
    </main>

    <main v-else-if="!authUnlocked" class="auth-shell" :data-theme="selectedThemeName">
      <WaterAuthScene :theme="selectedThemeName" />
      <section class="auth-card">
        <div class="auth-brand">
          <span class="auth-brand-mark" aria-hidden="true">
            <img :src="brandMarkSrc" alt="" />
          </span>
          <div>
            <h1>{{ productName }}</h1>
            <p>{{ productTagline }}</p>
          </div>
        </div>
        <div class="auth-lock-title">
          <LockKeyhole :size="18" />
          <strong>访问已上锁</strong>
        </div>
        <a-alert
          v-if="authStatus?.configured === false"
          class="auth-lock-alert"
          type="info"
          show-icon
          message="访问锁未启用"
          description="当前运行未初始化访问门禁，工作台会直接开放。"
        />
        <form class="auth-form" @submit.prevent="unlockAccess">
          <a-input-password
            v-model:value="authPIN"
            id="auth-pin"
            name="auth-pin"
            size="large"
            placeholder="输入访问 PIN"
            autocomplete="one-time-code"
            :disabled="authLockRemainingSeconds > 0"
          />
          <p v-if="authLockRemainingSeconds > 0" class="auth-error auth-lockout">
            尝试次数过多，请 {{ authLockRemainingText }} 后再试
          </p>
          <p v-else-if="authError" class="auth-error">{{ authError }}</p>
          <a-button
            html-type="submit"
            type="primary"
            size="large"
            block
            :loading="authSubmitting"
            :disabled="authLockRemainingSeconds > 0"
          >
            <template #icon><KeyRound :size="16" /></template>
            解锁
          </a-button>
        </form>
      </section>
    </main>

    <main
      v-else
      class="codex-shell"
      :class="{ 'left-collapsed': leftPanelCollapsed, 'right-collapsed': rightPanelCollapsed }"
      :data-theme="selectedThemeName"
      :style="shellStyle"
    >
      <div
        ref="customCursorRef"
        v-show="customCursorSupported && customCursorVisible"
        class="water-cursor"
        :class="{ 'is-interactive': customCursorInteractive, 'is-pressed': customCursorPressed }"
        aria-hidden="true"
      >
        <svg viewBox="0 0 24 32" class="water-cursor-pointer" aria-hidden="true">
          <path
            d="M4 2L17.4 15.2H10.2L13.8 28.8L11.5 29.4L7.7 18.7L4 22.3V2Z"
            class="water-cursor-body"
          />
          <path
            d="M7.6 8.3L14.7 15.4"
            class="water-cursor-sheen"
          />
        </svg>
      </div>
      <button
        v-show="waterPetEnabled"
        class="water-pet"
        :class="{
          'is-live': selectedTaskIsLive,
          'is-poked': waterPetPoked,
          'is-dragging': waterPetDragging,
          'is-walking': waterPetWalking,
          'is-turning': waterPetTurning,
          'is-facing-left': waterPetFacing < 0,
          'is-facing-right': waterPetFacing > 0,
          [waterPetGaitClass]: true,
        }"
        :style="waterPetStyle"
        type="button"
        :title="waterPetMessage"
        aria-label="若水水灵"
        @pointerdown.prevent="startWaterPetDrag"
        @click="pokeWaterPet"
      >
        <span class="water-pet-bubble">{{ waterPetMessage }}</span>
        <span class="water-pet-trail one" aria-hidden="true"></span>
        <span class="water-pet-trail two" aria-hidden="true"></span>
        <span class="water-pet-ripple" aria-hidden="true"></span>
        <svg class="water-pet-figure" viewBox="0 0 88 96" aria-hidden="true">
          <defs>
            <radialGradient id="water-pet-body-front-gradient" cx="30%" cy="22%" r="78%">
              <stop offset="0%" style="stop-color: #ffffff; stop-opacity: 0.9" />
              <stop offset="24%" style="stop-color: var(--brand-soft); stop-opacity: 0.42" />
              <stop offset="52%" style="stop-color: var(--brand); stop-opacity: 0.98" />
              <stop offset="100%" style="stop-color: var(--brand-deep); stop-opacity: 0.95" />
            </radialGradient>
            <radialGradient id="water-pet-body-back-gradient" cx="50%" cy="42%" r="68%">
              <stop offset="0%" style="stop-color: var(--brand-deep); stop-opacity: 0.34" />
              <stop offset="75%" style="stop-color: var(--brand-deep); stop-opacity: 0.18" />
              <stop offset="100%" style="stop-color: var(--brand-deep); stop-opacity: 0" />
            </radialGradient>
            <linearGradient id="water-pet-body-rim-gradient" x1="20%" x2="85%" y1="18%" y2="88%">
              <stop offset="0%" style="stop-color: #ffffff; stop-opacity: 0.62" />
              <stop offset="36%" style="stop-color: #ffffff; stop-opacity: 0" />
              <stop offset="100%" style="stop-color: var(--brand-deep); stop-opacity: 0.42" />
            </linearGradient>
          </defs>
          <ellipse class="water-pet-volume" cx="44" cy="48" rx="25" ry="34" />
          <path
            class="water-pet-body-back"
            d="M44 8C28 25 17 39 17 57C17 76 30 87 44 87C58 87 71 76 71 57C71 39 60 25 44 8Z"
          />
          <path
            class="water-pet-shadow"
            d="M23 78C31 72 55 72 65 78C58 83 31 84 23 78Z"
          />
          <path
            class="water-pet-body"
            fill="url(#water-pet-body-front-gradient)"
            d="M44 8C28 25 17 39 17 57C17 76 30 87 44 87C58 87 71 76 71 57C71 39 60 25 44 8Z"
          />
          <path
            class="water-pet-body-rim"
            d="M27 26C34 18 40 13 46 10C39 23 33 32 28 38"
          />
          <path
            class="water-pet-body-shine"
            d="M31 36C35 27 40 21 45 16"
          />
          <path class="water-pet-arm left" d="M19 57C11 58 9 64 13 68" />
          <path class="water-pet-arm right" d="M69 57C77 58 79 64 75 68" />
          <g class="water-pet-face">
            <circle class="water-pet-eye left" cx="35" cy="53" r="3.2" />
            <circle class="water-pet-eye right" cx="53" cy="53" r="3.2" />
            <circle class="water-pet-eye-spark left" cx="33.9" cy="51.8" r="1" />
            <circle class="water-pet-eye-spark right" cx="51.9" cy="51.8" r="1" />
            <path class="water-pet-mouth" d="M36 64C40 69 48 69 52 64" />
          </g>
          <path class="water-pet-foot left" d="M31 85C34 89 39 89 42 86" />
          <path class="water-pet-foot right" d="M46 86C50 89 55 89 58 85" />
          <path
            class="water-pet-wave"
            d="M18 69C25 64 31 64 38 69C45 74 52 74 60 68C64 65 68 64 72 65"
          />
          <circle class="water-pet-spark one" cx="22" cy="31" r="2" />
          <circle class="water-pet-spark two" cx="69" cy="42" r="1.8" />
        </svg>
      </button>
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
            <div class="water-mark">
              <img :src="brandMarkSrc" alt="" aria-hidden="true" />
            </div>
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
                :class="{
                  active: item.id === selectedTaskId,
                  'is-live': item.id === selectedTaskId && selectedTaskIsLive,
                  'is-complete': item.id === selectedTaskId && selectedTaskIsCompleted,
                }"
                @click="selectedTaskId = item.id"
              >
                <div class="task-item-main">
                  <span>{{ item.title }}</span>
                  <small>
                    <template v-if="item.id === selectedTaskId">
                      {{ taskLifecycleText(item.status) }} · {{ statusText }}
                    </template>
                    <template v-else>
                      {{ taskLifecycleText(item.status) }}
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
              <a-tag class="task-status-tag" :color="taskHeaderTone">
                {{ statusText }}
              </a-tag>
              <span v-if="selectedTaskIsThinking" class="status-thought-wave" aria-hidden="true">
                <i></i>
                <i></i>
                <i></i>
              </span>
              <span v-else-if="selectedTaskIsCompleted" class="status-complete-drop" aria-hidden="true"></span>
              <span v-if="latestTurnDurationText" class="duration-chip">{{ latestTurnDurationText }}</span>
              <span v-if="selectedTask" class="muted">任务{{ taskLifecycleText(selectedTask.status) }}</span>
            </div>
          </div>
          <div class="chat-header-actions">
            <span
              class="context-state"
              :class="{ active: latestContextUsage, warning: contextHeaderPercent >= 80 }"
              :style="contextHeaderStyle"
              :title="contextHeaderTitle"
            >
              <span class="context-meter" />
              <span>{{ contextHeaderText }}</span>
            </span>
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
              :disabled="authStatus?.configured === false"
              title="锁定访问"
              aria-label="锁定访问"
              @click="lockAccess"
            >
              <template #icon><LogOut :size="13" /></template>
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

        <details v-if="latestTaskContract" class="task-contract-strip" :open="latestTaskContract.missingInputs.length > 0">
          <summary>
            <span class="contract-stage">{{ contractStageText(latestTaskContract.stage) }}</span>
            <strong>{{ latestTaskContract.goal }}</strong>
            <span class="contract-count">{{ latestTaskContract.doneWhen.length }} 项完成条件</span>
          </summary>
          <div class="task-contract-detail">
            <section v-if="latestTaskPlan" class="contract-plan">
              <span>执行计划</span>
              <ol class="plan-step-list">
                <li v-for="item in latestTaskPlan.steps" :key="item.id" :data-status="item.status">
                  <span class="plan-step-index">{{ item.position }}</span>
                  <span class="plan-step-copy">
                    <strong>{{ item.title }}</strong>
                    <small>{{ planStepStatusText(item.status) }}</small>
                  </span>
                </li>
              </ol>
            </section>
            <section v-if="latestReplayAssessment" class="contract-replay">
              <span>历史执行质量</span>
              <div class="replay-score-line">
                <strong>{{ latestReplayAssessment.score }}</strong>
                <span>
                  {{ latestReplayAssessment.turns }} 轮 · 重复读取 {{ latestReplayAssessment.repeatedReads }} 次 ·
                  验证 {{ latestReplayAssessment.validations }} 次
                </span>
              </div>
              <ul v-if="latestReplayAssessment.findings.length">
                <li v-for="item in latestReplayAssessment.findings" :key="item">{{ item }}</li>
              </ul>
            </section>
            <section>
              <span>完成条件</span>
              <ul>
                <li v-for="item in latestTaskContract.doneWhen" :key="item">{{ item }}</li>
              </ul>
            </section>
            <section v-if="latestTaskContract.missingInputs.length" class="contract-missing">
              <span>需要你补充</span>
              <ul>
                <li v-for="item in latestTaskContract.missingInputs" :key="item">{{ item }}</li>
              </ul>
            </section>
            <section
              v-if="latestHypothesisLedger && latestHypothesisLedger.hypotheses.length"
              class="contract-ledger"
            >
              <span>当前判断</span>
              <ul class="hypothesis-list">
                <li v-for="item in latestHypothesisLedger.hypotheses" :key="item.id">
                  <span class="ledger-state" :data-status="item.status">{{ hypothesisStatusText(item.status) }}</span>
                  <span>{{ item.claim }}</span>
                </li>
              </ul>
            </section>
            <section
              v-if="latestHypothesisLedger && latestHypothesisLedger.evidence.length"
              class="contract-ledger"
            >
              <span>最近证据</span>
              <ul class="evidence-list">
                <li v-for="item in latestHypothesisLedger.evidence.slice(0, 5)" :key="item.id">
                  <span class="ledger-state" :data-outcome="item.outcome">{{ evidenceOutcomeText(item.outcome) }}</span>
                  <span class="evidence-copy">
                    <code>{{ item.resource }}</code>
                    <small>{{ item.summary }}</small>
                  </span>
                </li>
              </ul>
            </section>
          </div>
        </details>

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
                  <div class="message-title">
                    <span class="message-title-text">{{ item.block.title }}</span>
                    <a-tag
                      v-if="item.block.role === 'assistant' && turnOutcomeLabel(item.block.turnId)"
                      class="message-title-tag"
                      :color="turnOutcomeTone(item.block.turnId)"
                    >
                      {{ turnOutcomeLabel(item.block.turnId) }}
                    </a-tag>
                  </div>
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
                  v-else-if="item.block.role === 'assistant' && isAssistantStreaming(item.block)"
                  class="markdown-body"
                  :class="{ typing: isAssistantTyping(item.block) }"
                >
                  <div class="assistant-stream-text">{{ visibleChatContent(item.block) }}</div>
                  <span v-if="isAssistantTyping(item.block)" class="answer-cursor" aria-hidden="true" />
                </div>
                <div
                  v-else-if="item.block.role === 'assistant'"
                  class="markdown-body"
                >
                  <div v-html="renderedMarkdown(item.block)" />
                </div>
                <div
                  v-if="item.block.role === 'user' && item.block.attachments?.length"
                  class="message-attachments"
                >
                  <a
                    v-for="attachment in item.block.attachments"
                    :key="attachment.id"
                    class="message-attachment"
                    :class="{ image: attachment.kind === 'image' }"
                    :href="chatAttachmentURL(item.block, attachment)"
                    target="_blank"
                    rel="noopener noreferrer"
                    :title="attachment.name"
                  >
                    <img
                      v-if="attachment.kind === 'image'"
                      :src="chatAttachmentURL(item.block, attachment)"
                      :alt="attachment.name"
                      loading="lazy"
                    />
                    <span v-else class="message-attachment-icon"><FileText :size="18" /></span>
                    <span class="message-attachment-meta">
                      <strong>{{ attachment.name }}</strong>
                      <small>
                        <template v-if="attachmentFormatLabel(attachment.name)">
                          {{ attachmentFormatLabel(attachment.name) }} ·
                        </template>
                        {{ formatAttachmentSize(attachment.size) }}
                      </small>
                    </span>
                  </a>
                </div>
                <p v-if="item.block.role !== 'assistant'" :class="{ typing: isAssistantTyping(item.block) }">
                  <span>{{ visibleChatContent(item.block) }}</span>
                  <span v-if="isAssistantTyping(item.block)" class="answer-cursor" aria-hidden="true" />
                </p>
                <div
                  v-if="item.block.role === 'system' && item.block.continuation?.canContinue"
                  class="message-continuation-actions"
                >
                  <a-button size="small" type="primary" ghost @click="continueTurn(item.block)">
                    <template #icon><ArrowRight :size="13" /></template>
                    继续上一轮
                  </a-button>
                </div>
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

            <section
              v-else-if="item.kind === 'summary' && item.summary"
              class="message-block summary-message"
            >
              <div class="message-avatar">果</div>
              <div class="message-content summary-panel">
                <div class="summary-header">
                  <div class="summary-heading">
                    <strong>任务产物</strong>
                    <small>
                      {{
                        [
                          item.summary.changedFiles.length > 0
                            ? `已编辑 ${item.summary.changedFiles.length} 个文件`
                            : '',
                          summaryCommands(item.summary).length > 0
                            ? `${summaryCommandTitle(item.summary)} ${summaryCommands(item.summary).length} 项`
                            : '',
                        ]
                          .filter(Boolean)
                          .join(' · ')
                      }}
                    </small>
                  </div>
                  <a-button
                    size="small"
                    type="text"
                    class="summary-toggle"
                    :title="isSummaryExpanded(item.summary) ? '收起任务产物' : '展开任务产物'"
                    :aria-label="isSummaryExpanded(item.summary) ? '收起任务产物' : '展开任务产物'"
                    @click="toggleSummaryExpanded(item.summary)"
                  >
                    <template #icon>
                      <ChevronDown
                        :size="13"
                        class="summary-toggle-icon"
                        :class="{ open: isSummaryExpanded(item.summary) }"
                      />
                    </template>
                  </a-button>
                </div>

                <div v-if="isSummaryExpanded(item.summary)" class="summary-body">
                  <div class="summary-toolbar">
                    <a-space :size="4">
                      <a-button
                        size="small"
                        :title="isSummaryRawMode(item.summary) ? '显示内容' : '显示原始事件'"
                        :aria-label="isSummaryRawMode(item.summary) ? '显示内容' : '显示原始事件'"
                        @click="toggleSummaryRawMode(item.summary)"
                      >
                        <template #icon>
                          <Eye v-if="isSummaryRawMode(item.summary)" :size="13" />
                          <Code2 v-else :size="13" />
                        </template>
                      </a-button>
                      <a-button
                        size="small"
                        title="复制 Markdown"
                        aria-label="复制任务产物 Markdown"
                        @click="copySummaryMarkdown(item.summary)"
                      >
                        <template #icon><Copy :size="13" /></template>
                      </a-button>
                    </a-space>
                  </div>

                  <pre v-if="isSummaryRawMode(item.summary)" class="summary-raw">{{ JSON.stringify(item.summary.raw, null, 2) }}</pre>
                  <div v-else class="summary-body-content">
                    <section v-if="item.summary.changedFiles.length > 0" class="summary-section">
                      <div class="summary-section-title">
                        <FileText :size="15" />
                        <span>已编辑文件</span>
                        <small>
                          +{{ summaryFileTotals(item.summary).additions }}
                          -{{ summaryFileTotals(item.summary).deletions }}
                        </small>
                      </div>
                      <div class="summary-file-list">
                        <div v-for="file in item.summary.changedFiles" :key="file.path" class="summary-row">
                          <div class="summary-row-main">
                            <strong>{{ file.displayPath || file.path }}</strong>
                            <small>{{ fileActionText(file.action) }} · {{ file.bytes }} bytes</small>
                          </div>
                          <div class="summary-diff-stat">
                            <span class="plus">+{{ file.additions }}</span>
                            <span class="minus">-{{ file.deletions }}</span>
                          </div>
                        </div>
                      </div>
                    </section>

                    <section v-if="summaryCommands(item.summary).length > 0" class="summary-section">
                      <div class="summary-section-title">
                        <Terminal :size="15" />
                        <span>{{ summaryCommandTitle(item.summary) }}</span>
                      </div>
                      <div class="summary-command-list">
                        <div
                          v-for="command in summaryCommands(item.summary)"
                          :key="command.command"
                          class="summary-row command"
                        >
                          <div class="summary-row-main">
                            <code>{{ command.command }}</code>
                            <pre v-if="command.summary">{{ command.summary }}</pre>
                          </div>
                          <a-tag :color="commandStatusTone(command.status)">
                            {{ commandStatusText(command.status) }}
                          </a-tag>
                        </div>
                      </div>
                    </section>
                  </div>
                </div>
              </div>
            </section>
          </template>
        </div>

        <footer class="composer">
          <div
            class="composer-box"
            :class="{
              'has-attachments': composerAttachments.length > 0,
              'is-dragging': composerDragging,
            }"
            @dragenter.prevent="composerDragging = true"
            @dragover.prevent="composerDragging = true"
            @dragleave.self.prevent="composerDragging = false"
            @drop.prevent="handleComposerDrop"
          >
            <input
              ref="attachmentInputRef"
              class="composer-file-input"
              type="file"
              multiple
              accept="image/*,.txt,.md,.json,.yaml,.yml,.xml,.csv,.log,.go,.py,.js,.jsx,.ts,.tsx,.vue,.java,.kt,.rs,.c,.h,.cpp,.hpp,.css,.scss,.html,.sql,.sh,.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx"
              @change="handleAttachmentInput"
            />
            <div v-if="composerAttachments.length > 0" class="composer-attachments">
              <article
                v-for="attachment in composerAttachments"
                :key="attachment.id"
                class="composer-attachment"
                :class="{ image: attachment.kind === 'image' }"
              >
                <div class="composer-attachment-preview">
                  <img
                    v-if="attachment.kind === 'image'"
                    :src="attachment.dataUrl"
                    :alt="attachment.name"
                  />
                  <FileText v-else :size="20" aria-hidden="true" />
                </div>
                <div class="composer-attachment-meta">
                  <strong :title="attachment.name">{{ attachment.name }}</strong>
                  <span>
                    <template v-if="attachmentFormatLabel(attachment.name)">
                      {{ attachmentFormatLabel(attachment.name) }} ·
                    </template>
                    {{ formatAttachmentSize(attachment.size) }}
                  </span>
                </div>
                <button
                  class="composer-attachment-remove"
                  type="button"
                  :title="`移除 ${attachment.name}`"
                  :aria-label="`移除 ${attachment.name}`"
                  @click="removeComposerAttachment(attachment.id)"
                >
                  <X :size="13" />
                </button>
              </article>
            </div>
            <a-textarea
              v-model:value="userInput"
              class="composer-input"
              :auto-size="{ minRows: 3, maxRows: 9 }"
              :placeholder="composerPlaceholder"
              @keydown="handleComposerKeydown"
              @paste="handleComposerPaste"
            />
            <div class="composer-toolbar">
              <div class="composer-tools">
                <button
                  class="composer-tool-button"
                  type="button"
                  title="添加文件或图片"
                  aria-label="添加文件或图片"
                  :disabled="!selectedTaskId"
                  @click="openAttachmentPicker"
                >
                  <Paperclip :size="17" />
                </button>
                <span v-if="composerAttachments.length > 0" class="composer-attachment-count">
                  {{ composerAttachments.length }} 个附件
                </span>
              </div>
              <a-button
                class="composer-send"
                :class="{ ready: canSubmitTurn }"
                type="primary"
                shape="circle"
                :disabled="!canSubmitTurn"
                :title="selectedTaskIsLive ? '当前任务执行中，请先等待或打断' : '发送'"
                :aria-label="selectedTaskIsLive ? '当前任务执行中，请先等待或打断' : '发送'"
                @click="sendTurn"
              >
                <template #icon><Send :size="16" /></template>
              </a-button>
            </div>
            <div v-if="composerDragging" class="composer-drop-target" aria-hidden="true">
              <UploadCloud :size="30" />
            </div>
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
        <a-tabs
          v-if="!rightPanelCollapsed"
          v-model:activeKey="rightTab"
          class="right-panel-tabs"
          size="small"
          tab-position="left"
        >
          <a-tab-pane key="files">
            <template #tab>
              <span class="right-tab-label">
                <FileText :size="14" />
                文件
              </span>
            </template>
            <section class="panel-form">
              <div class="workspace-files">
                <div class="section-title">
                  <label>工作区文件</label>
                  <a-space>
                    <a-button
                      size="small"
                      :disabled="!workspaceFilePath"
                      title="返回上级"
                      aria-label="返回上级"
                      @click="openWorkspaceFileParent"
                    >
                      <template #icon><ChevronLeft :size="14" /></template>
                    </a-button>
                    <a-button
                      size="small"
                      :loading="fileBrowserLoading"
                      title="刷新文件"
                      aria-label="刷新文件"
                      @click="refreshWorkspaceFiles(workspaceFilePath)"
                    >
                      <template #icon><RefreshCw :size="13" /></template>
                    </a-button>
                  </a-space>
                </div>
                <code class="file-path-chip">{{ workspaceFilePathLabel }}</code>
                <div class="file-list">
                  <button
                    v-for="item in workspaceFiles"
                    :key="item.path"
                    class="file-row"
                    :class="{ active: item.path === selectedWorkspaceFilePath }"
                    type="button"
                    @click="openWorkspaceFile(item)"
                  >
                    <Folder v-if="item.isDir" :size="15" />
                    <FileText v-else :size="15" />
                    <span>{{ item.name }}</span>
                    <small v-if="!item.isDir">{{ formatFileSize(item.size) }}</small>
                  </button>
                  <a-empty v-if="!fileBrowserLoading && workspaceFiles.length === 0" description="暂无文件" />
                </div>
              </div>
            </section>
          </a-tab-pane>

          <a-tab-pane key="approvals">
            <template #tab>
              <span class="right-tab-label">
                <CheckCircle :size="14" />
                审批
                <small v-if="approvals.length > 0">{{ approvals.length }}</small>
              </span>
            </template>
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

          <a-tab-pane key="context">
            <template #tab>
              <span class="right-tab-label">
                <Code2 :size="14" />
                上下文
              </span>
            </template>
            <section class="panel-form">
              <div class="context-pack-card">
                <div class="section-title">
                  <label>最近上下文包</label>
                  <a-tag>{{ latestContextSnapshot.turnLabel }}</a-tag>
                </div>
                <div v-if="latestContextSnapshot.usage" class="context-pack-body">
                  <div class="context-meter-card is-live" :style="contextHeaderStyle">
                    <span class="context-meter" />
                    <strong>{{ contextHeaderText }}</strong>
                  </div>
                  <div class="context-pack-grid">
                    <div>
                      <small>任务摘要</small>
                      <span>{{ latestContextSnapshot.usage.hasTaskSummary ? '已带入' : '暂无' }}</span>
                    </div>
                    <div>
                      <small>文件摘要</small>
                      <span>{{ latestContextSnapshot.usage.selectedFilePaths.length }}</span>
                    </div>
                    <div>
                      <small>裁剪</small>
                      <span>{{ latestContextSnapshot.usage.truncated ? '已截断' : '未截断' }}</span>
                    </div>
                  </div>
                  <div v-if="latestContextSnapshot.usage.selectedFilePaths.length > 0" class="context-path-list">
                    <code v-for="path in latestContextSnapshot.usage.selectedFilePaths" :key="path">
                      {{ path }}
                    </code>
                  </div>
                  <a-collapse
                    v-if="latestContextSnapshot.summary?.summary"
                    class="context-summary-collapse"
                    ghost
                  >
                    <a-collapse-panel key="summary" header="任务滚动摘要">
                      <pre class="context-summary-preview">{{ latestContextSnapshot.summary.summary }}</pre>
                    </a-collapse-panel>
                  </a-collapse>
                </div>
                <a-empty v-else description="暂无上下文记录" />
              </div>

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

          <a-tab-pane key="terminal">
            <template #tab>
              <span class="right-tab-label">
                <Terminal :size="14" />
                终端
              </span>
            </template>
            <ServerTerminalPanel
              :workspace-id="selectedWorkspaceId"
              :workspace-name="selectedWorkspace?.name"
              :active="rightTab === 'terminal' && !rightPanelCollapsed"
            />
          </a-tab-pane>

          <a-tab-pane key="settings">
            <template #tab>
              <span class="right-tab-label">
                <Settings2 :size="14" />
                设置
              </span>
            </template>
            <a-collapse v-model:activeKey="settingsOpenKey" class="settings-collapse" accordion ghost>
              <a-collapse-panel key="access">
                <template #header>
                  <div class="settings-panel-header">
                    <span>访问安全</span>
                    <a-space>
                      <a-tag :color="authUnlocked ? 'green' : 'red'">{{ authStatusText }}</a-tag>
                      <a-button size="small" @click.stop="lockAccess">
                        <template #icon><LockKeyhole :size="14" /></template>
                        锁定
                      </a-button>
                    </a-space>
                  </div>
                </template>
                <section class="settings-panel-body">
                  <a-alert
                    v-if="authStatus?.configured === false"
                    type="info"
                    show-icon
                    message="访问锁未启用"
                    description="后端当前未初始化 PIN 门禁，工作台直接开放。"
                  />
                  <div class="settings-note">
                    <strong>当前会话</strong>
                    <span>
                      {{
                        authStatus?.sessionExpiresAt
                          ? `有效期至 ${authStatus.sessionExpiresAt}`
                          : '当前尚未解锁'
                      }}
                    </span>
                  </div>
                  <div v-if="authStatus?.configured !== false" class="settings-note">
                    <strong>PIN 修改</strong>
                    <span>输入当前 PIN 和新 PIN，修改后会自动刷新会话。</span>
                  </div>
                  <a-space v-if="authStatus?.configured !== false" class="modal-form" direction="vertical" :size="12">
                    <a-input-password
                      v-model:value="authPinForm.currentPin"
                      placeholder="当前 PIN"
                    />
                    <a-input-password
                      v-model:value="authPinForm.newPin"
                      placeholder="新 PIN"
                    />
                    <a-button type="primary" :disabled="!canChangePIN" @click="changeAuthPIN">
                      保存 PIN
                    </a-button>
                  </a-space>
                </section>
              </a-collapse-panel>

              <a-collapse-panel key="appearance">
                <template #header>
                  <div class="settings-panel-header">
                    <span>外观</span>
                    <a-tag>{{ activeTheme.label }}</a-tag>
                  </div>
                </template>
                <section class="settings-panel-body">
                  <div class="settings-toggle-row">
                    <div class="settings-toggle-copy">
                      <strong>显示若水水灵</strong>
                      <small>关闭后不显示宠物，也不再自动移动。</small>
                    </div>
                    <button
                      class="settings-toggle-switch"
                      :class="{ active: waterPetEnabled }"
                      type="button"
                      role="switch"
                      :aria-checked="waterPetEnabled"
                      @click="waterPetEnabled = !waterPetEnabled"
                    >
                      <span>{{ waterPetEnabled ? '已开启' : '已关闭' }}</span>
                      <i aria-hidden="true"></i>
                    </button>
                  </div>
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

              <a-collapse-panel key="skills">
                <template #header>
                  <div class="settings-panel-header">
                    <span>Skills</span>
                    <a-space>
                      <a-tag>{{ skills.filter((item) => item.enabled).length }} / {{ skills.length }}</a-tag>
                      <PackageOpen :size="15" />
                    </a-space>
                  </div>
                </template>
                <section class="settings-panel-body">
                  <div class="skill-install-bar">
                    <a-input
                      v-model:value="skillInstallURL"
                      placeholder="https://.../skill.zip"
                      :disabled="skillInstalling"
                      @press-enter="installSkillFromURL"
                    >
                      <template #prefix><Link2 :size="14" /></template>
                    </a-input>
                    <a-button
                      type="primary"
                      :loading="skillInstalling"
                      :disabled="!skillInstallURL.trim()"
                      @click="installSkillFromURL"
                    >
                      安装
                    </a-button>
                  </div>
                  <input
                    ref="skillUploadInput"
                    class="visually-hidden"
                    type="file"
                    accept=".zip,application/zip"
                    @change="installSkillArchive"
                  />
                  <a-button block :loading="skillInstalling" @click="skillUploadInput?.click()">
                    <template #icon><UploadCloud :size="14" /></template>
                    上传 Skill ZIP
                  </a-button>
                  <div v-if="skills.length" class="skill-list">
                    <div v-for="item in skills" :key="item.id" class="skill-row" :class="{ enabled: item.enabled }">
                      <div class="skill-main">
                        <div class="skill-title">
                          <strong>{{ item.name }}</strong>
                        </div>
                        <p>{{ item.description || item.id }}</p>
                        <div class="skill-meta">
                          <a-tag>{{ item.version }}</a-tag>
                          <a-tag :color="item.enabled ? 'green' : 'default'">
                            {{ item.enabled ? '启用' : '停用' }}
                          </a-tag>
                          <span>{{ skillSourceLabel(item) }}</span>
                          <code>{{ item.sha256.slice(0, 12) }}</code>
                        </div>
                        <div v-if="item.keywords.length" class="skill-keywords">
                          <a-tag v-for="keyword in item.keywords.slice(0, 5)" :key="keyword">
                            {{ keyword }}
                          </a-tag>
                        </div>
                      </div>
                      <div class="skill-actions">
                        <a-switch
                          :checked="item.enabled"
                          size="small"
                          :aria-label="item.enabled ? `停用 ${item.name}` : `启用 ${item.name}`"
                          @change="toggleSkill(item)"
                        />
                        <a-popconfirm title="确认删除这个 Skill？" ok-text="删除" cancel-text="取消" @confirm="deleteSkill(item)">
                          <a-button size="small" danger title="删除 Skill" :aria-label="`删除 ${item.name}`">
                            <template #icon><Trash2 :size="13" /></template>
                          </a-button>
                        </a-popconfirm>
                      </div>
                    </div>
                  </div>
                  <a-empty v-else description="暂无 Skill" />
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
                  <div v-if="activeProvider" class="provider-summary">
                    <div class="provider-summary-main">
                      <small>当前工作区使用</small>
                      <strong>{{ activeProvider.name }}</strong>
                      <span>{{ activeProviderScopeLabel }}</span>
                    </div>
                    <a-tag class="provider-summary-tag">{{ activeProviderScopeLabel }}</a-tag>
                  </div>
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
                              {{ providerRowStatusLabel(item) }}
                            </a-tag>
                            <a-tag v-else-if="item.isDefault" class="provider-status">全局默认</a-tag>
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
                          <a-button
                            v-if="item.id !== activeProviderId"
                            size="small"
                            title="切换当前工作区默认 Provider"
                            @click="setWorkspaceDefaultProvider(item)"
                          >
                            <template #icon><ArrowRight :size="14" /></template>
                            当前工作区
                          </a-button>
                          <a-button
                            v-if="item.id !== defaultProvider?.id"
                            size="small"
                            title="设为全局默认 Provider"
                            @click="setGlobalDefaultProvider(item)"
                          >
                            <template #icon><CheckCircle :size="14" /></template>
                            全局默认
                          </a-button>
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
                  <div v-if="workspaces.length > 0" class="settings-list workspace-settings-list">
                    <div
                      v-for="item in workspaces"
                      :key="item.id"
                      class="workspace-settings-row"
                      :class="{ active: item.id === selectedWorkspaceId }"
                      role="button"
                      tabindex="0"
                      @click="selectedWorkspaceId = item.id"
                      @keydown.enter.prevent="selectedWorkspaceId = item.id"
                      @keydown.space.prevent="selectedWorkspaceId = item.id"
                    >
                      <div class="workspace-settings-main">
                        <div class="workspace-settings-title">
                          <strong>{{ item.name }}</strong>
                          <a-tag v-if="item.id === selectedWorkspaceId" color="green">当前</a-tag>
                        </div>
                        <code>{{ item.rootPath }}</code>
                        <span>{{ item.permissionMode === 'full_access' ? '完全访问' : '请求审批' }}</span>
                      </div>
                      <a-space class="workspace-settings-actions" :size="6">
                        <a-button
                          size="small"
                          title="下载工作区"
                          aria-label="下载工作区"
                          @click.stop="downloadWorkspaceArchive(item)"
                        >
                          <template #icon><Download :size="13" /></template>
                        </a-button>
                        <a-button size="small" title="编辑工作区" aria-label="编辑工作区" @click.stop="openWorkspaceModal(item)">
                          <template #icon><Pencil :size="14" /></template>
                        </a-button>
                        <a-popconfirm title="确认删除这个工作区？" ok-text="删除" cancel-text="取消" @confirm="deleteWorkspace(item)">
                          <a-button size="small" danger title="删除工作区" aria-label="删除工作区" @click.stop>
                            <template #icon><Trash2 :size="14" /></template>
                          </a-button>
                        </a-popconfirm>
                      </a-space>
                    </div>
                  </div>
                  <a-empty v-else description="暂无工作区" />
                </section>
              </a-collapse-panel>
            </a-collapse>
          </a-tab-pane>
        </a-tabs>
      </aside>

      <a-modal
        v-model:open="filePreviewOpen"
        :width="920"
        :footer="null"
        :mask-closable="false"
        class="file-preview-modal"
        @cancel="closeWorkspaceFilePreview"
      >
        <template #title>
          <div class="file-modal-title">
            <FileText :size="15" />
            <span>{{ workspaceFileModalTitle }}</span>
          </div>
        </template>
        <div class="file-modal-toolbar">
          <a-space :size="6" wrap>
            <a-tag>{{ workspaceFileLanguage }}</a-tag>
            <span v-if="workspaceFileContent" class="muted">{{ formatFileSize(workspaceFileContent.size) }}</span>
            <a-tag v-if="workspaceFileContent?.truncated" color="gold">已截断</a-tag>
          </a-space>
          <a-button
            size="small"
            :disabled="!workspaceFileContent"
            title="复制文件内容"
            aria-label="复制文件内容"
            @click="copyWorkspaceFileContent"
          >
            <template #icon><Copy :size="13" /></template>
          </a-button>
          <a-button
            size="small"
            type="primary"
            ghost
            :disabled="!workspaceFileContent"
            title="下载文件"
            aria-label="下载文件"
            @click="downloadWorkspaceFile"
          >
            <template #icon><Download :size="13" /></template>
          </a-button>
        </div>
        <div v-if="fileContentLoading" class="file-modal-empty">读取中...</div>
        <div v-else-if="workspaceFileContent" class="file-modal-code" role="region" aria-label="文件内容">
          <div
            v-for="line in workspaceFilePreviewLines"
            :key="line.number"
            class="file-code-line"
          >
            <span class="file-code-line-number">{{ line.number }}</span>
            <code v-html="line.html" />
          </div>
        </div>
        <div v-if="workspaceFilePreviewHint" class="file-modal-hint">{{ workspaceFilePreviewHint }}</div>
        <div v-else-if="!workspaceFileContent && !fileContentLoading" class="file-modal-empty">未选择文件</div>
      </a-modal>

      <a-modal
        v-model:open="taskModalOpen"
        :title="taskModalTitle"
        :ok-text="editingTaskId ? '保存' : '创建'"
        cancel-text="取消"
        :confirm-loading="taskSubmitting"
        :mask-closable="false"
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
        :mask-closable="false"
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
        :mask-closable="false"
        @ok="saveProvider"
      >
        <a-space class="modal-form" direction="vertical" :size="12">
          <a-input v-model:value="providerForm.name" placeholder="Provider 名称" />
          <a-input v-model:value="providerForm.baseUrl" placeholder="Base URL" />
          <div class="provider-model-field">
            <a-input v-model:value="providerForm.model" placeholder="Model" />
            <a-button :loading="providerModelsLoading" @click="loadProviderModels">
              <template #icon><RefreshCw :size="13" /></template>
              获取模型
            </a-button>
          </div>
          <div v-if="providerModelOptions.length > 0" class="provider-model-options">
            <button
              v-for="item in providerModelOptions"
              :key="item.id"
              class="provider-model-option"
              :class="{ active: providerForm.model === item.id }"
              type="button"
              @click="selectProviderModel(item)"
            >
              {{ item.id }}
            </button>
          </div>
          <p v-if="providerModelsError" class="provider-model-error">{{ providerModelsError }}</p>
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
          <a-collapse ghost class="provider-advanced">
            <a-collapse-panel key="advanced" header="高级参数">
              <div class="provider-advanced-grid">
                <label>
                  <span>连接与响应头超时</span>
                  <a-input-number
                    v-model:value="providerForm.timeoutSeconds"
                    class="full"
                    :min="1"
                    :max="600"
                    :step="5"
                    addon-after="秒"
                  />
                </label>
                <label>
                  <span>流空闲超时</span>
                  <a-input-number
                    v-model:value="providerForm.streamIdleTimeoutSeconds"
                    class="full"
                    :min="5"
                    :max="1800"
                    :step="30"
                    addon-after="秒"
                  />
                </label>
              </div>
            </a-collapse-panel>
          </a-collapse>
        </a-space>
      </a-modal>
    </main>
  </a-config-provider>
</template>
