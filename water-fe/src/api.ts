export interface Envelope<T> {
  success: boolean
  requestId: string
  message: string
  httpCode: number
  data: T
}

export interface Provider {
  id: string
  name: string
  type: string
  baseUrl: string
  model: string
  apiKeyConfigured: boolean
  isDefault: boolean
  enabled: boolean
  contextWindowTokens: number
  timeoutMs: number
  maxRetries: number
  streamIdleTimeoutMs: number
}

export interface ProviderModelOption {
  id: string
}

export interface Skill {
  id: string
  name: string
  version: string
  description: string
  keywords: string[]
  source: 'upload' | 'url' | string
  sourceUrl?: string
  sha256: string
  enabled: boolean
  installedAt: string
  updatedAt: string
}

export interface Workspace {
  id: string
  name: string
  rootPath: string
  defaultProviderId?: string
  permissionMode: 'request_approval' | 'full_access'
  trusted: boolean
}

export interface Task {
  id: string
  workspaceId: string
  title: string
  status: string
}

export interface TaskEvent {
  eventId: string
  type: string
  requestId: string
  workspaceId?: string
  taskId?: string
  turnId?: string
  sequence: number
  createdAt: string
  payload?: unknown
  payloadJson?: string
}

export interface ScheduledTask {
  id: string
  workspaceId: string
  name: string
  prompt: string
  scheduleType: 'daily' | 'interval'
  scheduleExpression: string
  timezone: string
  enabled: boolean
  concurrencyPolicy: 'skip'
  approvalPolicy: 'pause'
  maxRetries: number
  retryIntervalSeconds: number
  nextRunAt?: string
  lastRunAt?: string
  createdAt: string
  updatedAt: string
}

export interface ScheduledTaskRun {
  id: string
  scheduledTaskId: string
  taskId?: string
  turnId?: string
  triggerType: 'scheduled' | 'manual' | 'retry'
  status: string
  scheduledAt: string
  startedAt?: string
  finishedAt?: string
  attempt: number
  promptSnapshot: string
  resultSummary: string
  errorMessage: string
  createdAt: string
  updatedAt: string
}

export interface TurnAttachmentInput {
  name: string
  mimeType: string
  dataUrl: string
}

export interface Approval {
  id: string
  workspaceId: string
  taskId?: string
  actionType: string
  target: string
  riskSummary: string
  expectedImpact: string
  status: string
  requestedAt: string
}

export interface ExternalPath {
  id: string
  workspaceId: string
  path: string
  pathType: 'file' | 'directory'
  accessMode: 'read' | 'write'
  sourceTaskId?: string
}

export interface WorkspaceFileItem {
  name: string
  path: string
  isDir: boolean
  size: number
  modifiedAt: string
}

export interface WorkspaceFileContent {
  path: string
  content: string
  size: number
  truncated: boolean
}

export interface TerminalSession {
  id: string
  workspaceId: string
  profileId: string
  status: string
  cwd: string
  cols: number
  rows: number
  createdAt: string
  updatedAt: string
  lastActiveAt?: string
  closedAt?: string
}

export interface AuthStatus {
  configured: boolean
  authenticated: boolean
  locked: boolean
  sessionExpiresAt: string
  lastUnlockedAt: string
  pinLockedUntil: string
  pinRetryAfterSeconds: number
}

export interface AuthSession {
  accessToken: string
  expiresAt: string
}

const API_BASE = import.meta.env.VITE_API_BASE ?? ''
const ACCESS_TOKEN_KEY = 'water-access-token'

export function getAccessToken(): string {
  if (typeof window === 'undefined') return ''
  return window.localStorage.getItem(ACCESS_TOKEN_KEY) ?? ''
}

export function setAccessToken(token: string) {
  if (typeof window === 'undefined') return
  if (token) {
    window.localStorage.setItem(ACCESS_TOKEN_KEY, token)
  } else {
    window.localStorage.removeItem(ACCESS_TOKEN_KEY)
  }
}

export function clearAccessToken() {
  setAccessToken('')
}

function buildHeaders(init?: RequestInit): HeadersInit {
  const headers = new Headers(init?.headers ?? {})
  if (!headers.has('Content-Type') && !(init?.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json')
  }
  const token = getAccessToken()
  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }
  return headers
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(API_BASE + path, {
    ...init,
    headers: buildHeaders(init),
  })
  const body = (await res.json()) as Envelope<T>
  if (!res.ok || !body.success) {
    const error = new Error(body.message || `HTTP ${res.status}`) as Error & {
      status?: number
      data?: unknown
    }
    error.status = res.status
    error.data = body.data
    throw error
  }
  return body.data
}

async function download(path: string): Promise<Blob> {
  const res = await fetch(API_BASE + path, {
    headers: buildHeaders(),
  })
  if (!res.ok) {
    const body = (await res.json().catch(() => undefined)) as Envelope<unknown> | undefined
    const error = new Error(body?.message || `HTTP ${res.status}`) as Error & { status?: number }
    error.status = res.status
    throw error
  }
  return await res.blob()
}

export const api = {
  authStatus: () => request<AuthStatus>('/api/auth/status'),
  unlockAuth: (pin: string) =>
    request<AuthSession>('/api/auth/unlock', {
      method: 'POST',
      body: JSON.stringify({ pin }),
    }),
  lockAuth: () =>
    request<Record<string, never>>('/api/auth/lock', {
      method: 'POST',
    }),
  changeAuthPin: (currentPin: string, newPin: string) =>
    request<AuthSession>('/api/auth/change-pin', {
      method: 'POST',
      body: JSON.stringify({ currentPin, newPin }),
    }),
  listProviders: () => request<{ items: Provider[] }>('/api/providers'),
  createProvider: (body: Record<string, unknown>) =>
    request<Provider>('/api/providers', { method: 'POST', body: JSON.stringify(body) }),
  updateProvider: (providerId: string, body: Record<string, unknown>) =>
    request<Provider>(`/api/providers/${providerId}`, { method: 'PUT', body: JSON.stringify(body) }),
  deleteProvider: (providerId: string) =>
    request<Record<string, never>>(`/api/providers/${providerId}`, {
      method: 'DELETE',
    }),
  setDefaultProvider: (providerId: string) =>
    request<Provider>(`/api/providers/${providerId}/default`, {
      method: 'POST',
    }),
  testProvider: (id: string) =>
    request<{ ok: boolean; message: string; latency: string }>(`/api/providers/${id}/test`, {
      method: 'POST',
    }),
  listProviderModels: (body: Record<string, unknown>) =>
    request<{ items: ProviderModelOption[] }>('/api/provider-models', {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  listSkills: () => request<{ items: Skill[] }>('/api/skills'),
  installSkillArchive: (file: File) => {
    const body = new FormData()
    body.set('file', file)
    return request<Skill>('/api/skills', { method: 'POST', body })
  },
  installSkillURL: (url: string) =>
    request<Skill>('/api/skills/install', {
      method: 'POST',
      body: JSON.stringify({ url }),
    }),
  setSkillEnabled: (skillId: string, enabled: boolean) =>
    request<Skill>(`/api/skills/${encodeURIComponent(skillId)}/${enabled ? 'enable' : 'disable'}`, {
      method: 'POST',
    }),
  deleteSkill: (skillId: string) =>
    request<Record<string, never>>(`/api/skills/${encodeURIComponent(skillId)}`, {
      method: 'DELETE',
    }),

  listWorkspaces: () => request<{ items: Workspace[] }>('/api/workspaces'),
  createWorkspace: (body: Record<string, unknown>) =>
    request<Workspace>('/api/workspaces', { method: 'POST', body: JSON.stringify(body) }),
  updateWorkspace: (workspaceId: string, body: Record<string, unknown>) =>
    request<Workspace>(`/api/workspaces/${workspaceId}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  deleteWorkspace: (workspaceId: string) =>
    request<Record<string, never>>(`/api/workspaces/${workspaceId}`, {
      method: 'DELETE',
    }),
  listExternalPaths: (workspaceId: string) =>
    request<{ items: ExternalPath[] }>(`/api/workspaces/${workspaceId}/external-paths`),
  createExternalPath: (workspaceId: string, body: Record<string, unknown>) =>
    request<ExternalPath>(`/api/workspaces/${workspaceId}/external-paths`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  deleteExternalPath: (workspaceId: string, pathId: string) =>
    request<Record<string, never>>(`/api/workspaces/${workspaceId}/external-paths/${pathId}`, {
      method: 'DELETE',
    }),
  listWorkspaceFiles: (workspaceId: string, path = '') =>
    request<{ path: string; items: WorkspaceFileItem[] }>(
      `/api/workspaces/${workspaceId}/files?path=${encodeURIComponent(path)}`,
    ),
  readWorkspaceFile: (workspaceId: string, path: string) =>
    request<WorkspaceFileContent>(
      `/api/workspaces/${workspaceId}/files/content?path=${encodeURIComponent(path)}`,
    ),
  createTerminalSession: (body: Record<string, unknown>) =>
    request<TerminalSession>('/api/terminal-sessions', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  closeTerminalSession: (sessionId: string) =>
    request<Record<string, never>>(`/api/terminal-sessions/${sessionId}`, {
      method: 'DELETE',
    }),
  downloadWorkspaceFile: (workspaceId: string, path: string) =>
    download(`/api/workspaces/${workspaceId}/files/download?path=${encodeURIComponent(path)}`),
  downloadWorkspaceArchive: (workspaceId: string) => download(`/api/workspaces/${workspaceId}/archive`),

  listTasks: (workspaceId: string) =>
    request<{ items: Task[] }>(`/api/workspaces/${workspaceId}/tasks`),
  createTask: (workspaceId: string, title: string) =>
    request<Task>(`/api/workspaces/${workspaceId}/tasks`, {
      method: 'POST',
      body: JSON.stringify({ title }),
    }),
  updateTask: (taskId: string, title: string) =>
    request<Task>(`/api/tasks/${taskId}`, {
      method: 'PUT',
      body: JSON.stringify({ title }),
    }),
  deleteTask: (taskId: string) =>
    request<Record<string, never>>(`/api/tasks/${taskId}`, {
      method: 'DELETE',
    }),
  createTurn: (taskId: string, userInput: string, attachments: TurnAttachmentInput[] = []) =>
    request<{ id: string }>(`/api/tasks/${taskId}/turns`, {
      method: 'POST',
      body: JSON.stringify({ userInput, attachments }),
    }),
  cancelTask: (taskId: string) =>
    request<Record<string, never>>(`/api/tasks/${taskId}/cancel`, {
      method: 'POST',
    }),
  listTaskEvents: (taskId: string) =>
    request<{ items: TaskEvent[] }>(`/api/tasks/${taskId}/events`),

  listScheduledTasks: (workspaceId = '') =>
    request<{ items: ScheduledTask[] }>(
      `/api/scheduled-tasks${workspaceId ? `?workspaceId=${encodeURIComponent(workspaceId)}` : ''}`,
    ),
  createScheduledTask: (body: Record<string, unknown>) =>
    request<ScheduledTask>('/api/scheduled-tasks', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  updateScheduledTask: (scheduledTaskId: string, body: Record<string, unknown>) =>
    request<ScheduledTask>(`/api/scheduled-tasks/${scheduledTaskId}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  deleteScheduledTask: (scheduledTaskId: string) =>
    request<Record<string, never>>(`/api/scheduled-tasks/${scheduledTaskId}`, {
      method: 'DELETE',
    }),
  setScheduledTaskEnabled: (scheduledTaskId: string, enabled: boolean) =>
    request<ScheduledTask>(`/api/scheduled-tasks/${scheduledTaskId}/${enabled ? 'enable' : 'disable'}`, {
      method: 'POST',
    }),
  runScheduledTaskNow: (scheduledTaskId: string) =>
    request<ScheduledTaskRun>(`/api/scheduled-tasks/${scheduledTaskId}/run-now`, {
      method: 'POST',
    }),
  listScheduledTaskRuns: (scheduledTaskId: string, limit = 50) =>
    request<{ items: ScheduledTaskRun[] }>(
      `/api/scheduled-tasks/${scheduledTaskId}/runs?limit=${limit}`,
    ),
  getScheduledTaskRun: (runId: string) =>
    request<ScheduledTaskRun>(`/api/scheduled-task-runs/${runId}`),
  cancelScheduledTaskRun: (runId: string) =>
    request<ScheduledTaskRun>(`/api/scheduled-task-runs/${runId}/cancel`, {
      method: 'POST',
    }),

  listApprovals: (workspaceId: string, status = 'pending') =>
    request<{ items: Approval[] }>(`/api/workspaces/${workspaceId}/approvals?status=${status}`),
  resolveApproval: (approvalId: string, status: 'approved' | 'rejected', message: string) =>
    request<Approval>(`/api/approvals/${approvalId}/resolve`, {
      method: 'POST',
      body: JSON.stringify({ status, message }),
    }),
}

export function workspaceFileDownloadURL(workspaceId: string, path: string): string {
  const url = new URL(API_BASE || window.location.origin, window.location.origin)
  url.pathname = `/api/workspaces/${workspaceId}/files/download`
  url.searchParams.set('path', path)
  const token = getAccessToken()
  if (token) url.searchParams.set('accessToken', token)
  return url.toString()
}

export function workspaceArchiveDownloadURL(workspaceId: string): string {
  const url = new URL(API_BASE || window.location.origin, window.location.origin)
  url.pathname = `/api/workspaces/${workspaceId}/archive`
  const token = getAccessToken()
  if (token) url.searchParams.set('accessToken', token)
  return url.toString()
}

export function taskAttachmentURL(taskId: string, attachmentId: string): string {
  const url = new URL(API_BASE || window.location.origin, window.location.origin)
  url.pathname = `/api/tasks/${taskId}/attachments`
  url.searchParams.set('id', attachmentId)
  const token = getAccessToken()
  if (token) url.searchParams.set('accessToken', token)
  return url.toString()
}

export function taskWebSocketURL(taskId: string, afterSequence = 0): string {
  const base = API_BASE || window.location.origin
  const url = new URL(base, window.location.origin)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  url.pathname = `/ws/tasks/${taskId}`
  url.search = ''
  const token = getAccessToken()
  if (token) {
    url.searchParams.set('accessToken', token)
  }
  if (afterSequence > 0) {
    url.searchParams.set('afterSequence', String(afterSequence))
  }
  return url.toString()
}

export function terminalWebSocketURL(sessionId: string): string {
  const base = API_BASE || window.location.origin
  const url = new URL(base, window.location.origin)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  url.pathname = `/ws/terminal-sessions/${sessionId}`
  url.search = ''
  const token = getAccessToken()
  if (token) {
    url.searchParams.set('accessToken', token)
  }
  return url.toString()
}
