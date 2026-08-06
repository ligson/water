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

const API_BASE = import.meta.env.VITE_API_BASE ?? ''

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(API_BASE + path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
  })
  const body = (await res.json()) as Envelope<T>
  if (!res.ok || !body.success) {
    throw new Error(body.message || `HTTP ${res.status}`)
  }
  return body.data
}

export const api = {
  listProviders: () => request<{ items: Provider[] }>('/api/providers'),
  createProvider: (body: Record<string, unknown>) =>
    request<Provider>('/api/providers', { method: 'POST', body: JSON.stringify(body) }),
  updateProvider: (providerId: string, body: Record<string, unknown>) =>
    request<Provider>(`/api/providers/${providerId}`, { method: 'PUT', body: JSON.stringify(body) }),
  deleteProvider: (providerId: string) =>
    request<Record<string, never>>(`/api/providers/${providerId}`, {
      method: 'DELETE',
    }),
  testProvider: (id: string) =>
    request<{ ok: boolean; message: string; latency: string }>(`/api/providers/${id}/test`, {
      method: 'POST',
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
  createTurn: (taskId: string, userInput: string) =>
    request<{ id: string }>(`/api/tasks/${taskId}/turns`, {
      method: 'POST',
      body: JSON.stringify({ userInput }),
    }),
  cancelTask: (taskId: string) =>
    request<Record<string, never>>(`/api/tasks/${taskId}/cancel`, {
      method: 'POST',
    }),
  listTaskEvents: (taskId: string) =>
    request<{ items: TaskEvent[] }>(`/api/tasks/${taskId}/events`),

  listApprovals: (workspaceId: string, status = 'pending') =>
    request<{ items: Approval[] }>(`/api/workspaces/${workspaceId}/approvals?status=${status}`),
  resolveApproval: (approvalId: string, status: 'approved' | 'rejected', message: string) =>
    request<Approval>(`/api/approvals/${approvalId}/resolve`, {
      method: 'POST',
      body: JSON.stringify({ status, message }),
    }),
}

export function taskWebSocketURL(taskId: string, afterSequence = 0): string {
  const base = API_BASE || window.location.origin
  const url = new URL(base, window.location.origin)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  url.pathname = `/ws/tasks/${taskId}`
  url.search = ''
  if (afterSequence > 0) {
    url.searchParams.set('afterSequence', String(afterSequence))
  }
  return url.toString()
}
