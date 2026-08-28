<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { message } from 'ant-design-vue'
import {
  CalendarClock,
  ChevronRight,
  Clock3,
  History,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Square,
  Trash2,
} from '@lucide/vue'
import {
  api,
  type ScheduledTask,
  type ScheduledTaskRun,
  type Workspace,
} from '../../api'

const props = defineProps<{
  workspaceId: string
  workspaces: Workspace[]
  active: boolean
}>()

const emit = defineEmits<{
  openTask: [taskId: string]
}>()

const items = ref<ScheduledTask[]>([])
const runsBySchedule = ref<Record<string, ScheduledTaskRun[]>>({})
const loading = ref(false)
const saving = ref(false)
const modalOpen = ref(false)
const editingId = ref('')
const expandedId = ref('')
let pollTimer: number | undefined

const form = reactive({
  workspaceId: '',
  name: '',
  prompt: '',
  scheduleType: 'daily' as 'daily' | 'interval',
  dailyTime: '09:00',
  intervalValue: 60,
  intervalUnit: 'minutes' as 'minutes' | 'hours',
  timezone: 'Asia/Shanghai',
  enabled: true,
  maxRetries: 0,
  retryIntervalMinutes: 5,
})

const intervalSeconds = computed(() => {
  const multiplier = form.intervalUnit === 'hours' ? 3600 : 60
  return form.intervalValue * multiplier
})

const intervalMinimum = computed(() => form.intervalUnit === 'hours' ? 1 : 5)
const intervalMaximum = computed(() => form.intervalUnit === 'hours' ? 720 : 43200)

const canSave = computed(() => {
  if (!form.workspaceId || !form.name.trim() || !form.prompt.trim()) return false
  if (form.scheduleType === 'daily') return /^\d{2}:\d{2}$/.test(form.dailyTime)
  return intervalSeconds.value >= 300 && intervalSeconds.value <= 30 * 24 * 60 * 60
})

const hasActiveRuns = computed(() =>
  Object.values(runsBySchedule.value)
    .flat()
    .some((run) => ['queued', 'running', 'waiting_approval'].includes(run.status)),
)

function scheduleExpression() {
  if (form.scheduleType === 'daily') return form.dailyTime
  return String(intervalSeconds.value)
}

function resetForm() {
  form.workspaceId = props.workspaceId || props.workspaces[0]?.id || ''
  form.name = ''
  form.prompt = ''
  form.scheduleType = 'daily'
  form.dailyTime = '09:00'
  form.intervalValue = 60
  form.intervalUnit = 'minutes'
  form.timezone = 'Asia/Shanghai'
  form.enabled = true
  form.maxRetries = 0
  form.retryIntervalMinutes = 5
}

function openCreate() {
  editingId.value = ''
  resetForm()
  modalOpen.value = true
}

function openEdit(item: ScheduledTask) {
  editingId.value = item.id
  form.workspaceId = item.workspaceId
  form.name = item.name
  form.prompt = item.prompt
  form.scheduleType = item.scheduleType
  form.dailyTime = item.scheduleType === 'daily' ? item.scheduleExpression : '09:00'
  if (item.scheduleType === 'interval') {
    const seconds = Number(item.scheduleExpression)
    form.intervalUnit = seconds % 3600 === 0 ? 'hours' : 'minutes'
    form.intervalValue = form.intervalUnit === 'hours' ? seconds / 3600 : seconds / 60
  }
  form.timezone = item.timezone
  form.enabled = item.enabled
  form.maxRetries = item.maxRetries
  form.retryIntervalMinutes = Math.max(1, Math.round(item.retryIntervalSeconds / 60))
  modalOpen.value = true
}

async function loadItems(quiet = false) {
  if (!props.workspaceId) {
    items.value = []
    runsBySchedule.value = {}
    return
  }
  if (!quiet) loading.value = true
  try {
    const result = await api.listScheduledTasks(props.workspaceId)
    items.value = result.items
    const activeIds = new Set(result.items.map((item) => item.id))
    runsBySchedule.value = Object.fromEntries(
      Object.entries(runsBySchedule.value).filter(([id]) => activeIds.has(id)),
    )
    await Promise.all(result.items.map((item) => loadRuns(item.id, true)))
  } catch (error) {
    if (!quiet) message.error(error instanceof Error ? error.message : '加载自动任务失败')
  } finally {
    if (!quiet) loading.value = false
  }
}

async function loadRuns(id: string, quiet = false) {
  try {
    const result = await api.listScheduledTaskRuns(id, expandedId.value === id ? 50 : 1)
    runsBySchedule.value = { ...runsBySchedule.value, [id]: result.items }
  } catch (error) {
    if (!quiet) message.error(error instanceof Error ? error.message : '加载执行记录失败')
  }
}

async function save() {
  if (!canSave.value || saving.value) return
  saving.value = true
  const body = {
    workspaceId: form.workspaceId,
    name: form.name.trim(),
    prompt: form.prompt.trim(),
    scheduleType: form.scheduleType,
    scheduleExpression: scheduleExpression(),
    timezone: form.timezone,
    enabled: form.enabled,
    maxRetries: form.maxRetries,
    retryIntervalSeconds: Math.max(1, form.retryIntervalMinutes) * 60,
  }
  try {
    if (editingId.value) {
      await api.updateScheduledTask(editingId.value, body)
      message.success('自动任务已更新')
    } else {
      await api.createScheduledTask(body)
      message.success('自动任务已创建')
    }
    modalOpen.value = false
    await loadItems()
  } catch (error) {
    message.error(error instanceof Error ? error.message : '保存自动任务失败')
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(item: ScheduledTask, checked: boolean) {
  try {
    await api.setScheduledTaskEnabled(item.id, checked)
    item.enabled = checked
    await loadItems(true)
  } catch (error) {
    message.error(error instanceof Error ? error.message : '更新自动任务状态失败')
  }
}

async function runNow(item: ScheduledTask) {
  try {
    await api.runScheduledTaskNow(item.id)
    expandedId.value = item.id
    message.success('已加入执行队列')
    window.setTimeout(() => loadRuns(item.id), 500)
  } catch (error) {
    message.error(error instanceof Error ? error.message : '启动自动任务失败')
  }
}

async function remove(item: ScheduledTask) {
  try {
    await api.deleteScheduledTask(item.id)
    if (expandedId.value === item.id) expandedId.value = ''
    await loadItems()
    message.success('自动任务已删除')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '删除自动任务失败')
  }
}

async function cancelRun(run: ScheduledTaskRun) {
  try {
    await api.cancelScheduledTaskRun(run.id)
    await loadRuns(run.scheduledTaskId)
    message.success('执行已取消')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '取消执行失败')
  }
}

async function toggleRuns(item: ScheduledTask) {
  expandedId.value = expandedId.value === item.id ? '' : item.id
  if (expandedId.value) await loadRuns(item.id)
}

function scheduleLabel(item: ScheduledTask) {
  if (item.scheduleType === 'daily') return `每天 ${item.scheduleExpression}`
  const seconds = Number(item.scheduleExpression)
  if (seconds % 3600 === 0) return `每 ${seconds / 3600} 小时`
  return `每 ${Math.round(seconds / 60)} 分钟`
}

function dateTime(value?: string) {
  if (!value) return '暂无'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
    hour12: false,
  }).format(new Date(value))
}

function runStatusLabel(status: string) {
  return {
    queued: '排队中', running: '执行中', waiting_approval: '等待审批', succeeded: '成功',
    failed: '失败', cancelled: '已取消', skipped: '已跳过', interrupted: '已中断',
  }[status] ?? status
}

function runStatusColor(status: string) {
  if (status === 'succeeded') return 'green'
  if (['failed', 'interrupted'].includes(status)) return 'red'
  if (['queued', 'running'].includes(status)) return 'blue'
  if (status === 'waiting_approval') return 'gold'
  return 'default'
}

function triggerLabel(trigger: string) {
  return { scheduled: '定时', manual: '手动', retry: '重试' }[trigger] ?? trigger
}

function latestRun(item: ScheduledTask) {
  return runsBySchedule.value[item.id]?.[0]
}

function restartPoller() {
  if (pollTimer) window.clearInterval(pollTimer)
  pollTimer = undefined
  if (!props.active) return
  pollTimer = window.setInterval(() => {
    if (hasActiveRuns.value) loadItems(true)
  }, 5000)
}

watch(() => props.workspaceId, () => {
  expandedId.value = ''
  runsBySchedule.value = {}
  loadItems()
})
watch(() => props.active, (active) => {
  if (active) loadItems(true)
  restartPoller()
})

onMounted(() => {
  loadItems()
  restartPoller()
})

onBeforeUnmount(() => {
  if (pollTimer) window.clearInterval(pollTimer)
})
</script>

<template>
  <section class="schedule-panel">
    <header class="schedule-toolbar">
      <div>
        <strong>自动任务</strong>
        <small>{{ items.length }} 个计划</small>
      </div>
      <a-space>
        <a-button size="small" title="刷新" aria-label="刷新自动任务" :loading="loading" @click="loadItems()">
          <template #icon><RefreshCw :size="14" /></template>
        </a-button>
        <a-button size="small" type="primary" :disabled="!workspaceId" @click="openCreate">
          <template #icon><Plus :size="14" /></template>
          新建
        </a-button>
      </a-space>
    </header>

    <a-empty v-if="!workspaceId" description="请先选择工作区" />
    <a-empty v-else-if="!loading && items.length === 0" description="暂无自动任务" />

    <div v-else class="schedule-list">
      <article v-for="item in items" :key="item.id" class="schedule-row">
        <div class="schedule-main">
          <div class="schedule-title-row">
            <div class="schedule-title">
              <CalendarClock :size="16" />
              <strong>{{ item.name }}</strong>
            </div>
            <a-switch :checked="item.enabled" size="small" @change="(checked: boolean) => toggleEnabled(item, checked)" />
          </div>
          <p>{{ item.prompt }}</p>
          <div class="schedule-meta">
            <span><Clock3 :size="13" />{{ scheduleLabel(item) }}</span>
            <span>下次：{{ item.enabled ? dateTime(item.nextRunAt) : '已暂停' }}</span>
          </div>
          <div v-if="latestRun(item)" class="latest-run">
            <a-tag :color="runStatusColor(latestRun(item)!.status)">{{ runStatusLabel(latestRun(item)!.status) }}</a-tag>
            <span>{{ latestRun(item)!.resultSummary || latestRun(item)!.errorMessage || dateTime(latestRun(item)!.startedAt) }}</span>
          </div>
        </div>
        <div class="schedule-actions">
          <a-button size="small" title="立即执行" aria-label="立即执行" @click="runNow(item)">
            <template #icon><Play :size="13" /></template>
          </a-button>
          <a-button size="small" title="执行记录" aria-label="执行记录" @click="toggleRuns(item)">
            <template #icon><History :size="13" /></template>
          </a-button>
          <a-button size="small" title="编辑" aria-label="编辑自动任务" @click="openEdit(item)">
            <template #icon><Pencil :size="13" /></template>
          </a-button>
          <a-popconfirm title="删除自动任务及其执行记录？正在执行的实例也会停止。" ok-text="删除" cancel-text="取消" @confirm="remove(item)">
            <a-button size="small" danger title="删除" aria-label="删除自动任务">
              <template #icon><Trash2 :size="13" /></template>
            </a-button>
          </a-popconfirm>
        </div>

        <div v-if="expandedId === item.id" class="run-history">
          <div class="run-history-title">
            <strong>执行记录</strong>
            <a-button size="small" type="text" @click="loadRuns(item.id)">
              <template #icon><RefreshCw :size="13" /></template>
            </a-button>
          </div>
          <a-empty v-if="!(runsBySchedule[item.id]?.length)" description="暂无执行记录" />
          <div v-for="run in runsBySchedule[item.id]" :key="run.id" class="run-row">
            <div class="run-status">
              <a-tag :color="runStatusColor(run.status)">{{ runStatusLabel(run.status) }}</a-tag>
              <span>{{ triggerLabel(run.triggerType) }}<template v-if="run.attempt > 1"> · 第 {{ run.attempt }} 次</template></span>
              <time>{{ dateTime(run.startedAt || run.scheduledAt) }}</time>
            </div>
            <p v-if="run.resultSummary" class="run-result">{{ run.resultSummary }}</p>
            <p v-else-if="run.errorMessage" class="run-error">{{ run.errorMessage }}</p>
            <div class="run-actions">
              <a-button v-if="run.taskId" size="small" @click="emit('openTask', run.taskId)">
                查看完整过程
                <template #icon><ChevronRight :size="13" /></template>
              </a-button>
              <a-button
                v-if="['queued', 'running', 'waiting_approval'].includes(run.status)"
                size="small"
                danger
                @click="cancelRun(run)"
              >
                <template #icon><Square :size="12" /></template>
                取消
              </a-button>
            </div>
          </div>
        </div>
      </article>
    </div>

    <a-modal
      v-model:open="modalOpen"
      :title="editingId ? '编辑自动任务' : '新建自动任务'"
      :confirm-loading="saving"
      :ok-button-props="{ disabled: !canSave }"
      ok-text="保存"
      cancel-text="取消"
      width="620px"
      @ok="save"
    >
      <a-form layout="vertical" class="schedule-form">
        <a-form-item label="名称" required>
          <a-input v-model:value="form.name" maxlength="80" placeholder="例如：每日检查项目测试" />
        </a-form-item>
        <a-form-item label="工作区" required>
          <a-select v-model:value="form.workspaceId">
            <a-select-option v-for="workspace in workspaces" :key="workspace.id" :value="workspace.id">
              {{ workspace.name }}
            </a-select-option>
          </a-select>
        </a-form-item>
        <a-form-item label="提示词" required>
          <a-textarea v-model:value="form.prompt" :rows="7" maxlength="12000" show-count placeholder="若水每次执行时收到的完整任务要求" />
        </a-form-item>
        <a-form-item label="执行计划" required>
          <a-radio-group v-model:value="form.scheduleType" button-style="solid">
            <a-radio-button value="daily">每天</a-radio-button>
            <a-radio-button value="interval">固定间隔</a-radio-button>
          </a-radio-group>
        </a-form-item>
        <div class="schedule-time-row">
          <a-form-item v-if="form.scheduleType === 'daily'" label="执行时间">
            <a-input v-model:value="form.dailyTime" type="time" />
          </a-form-item>
          <template v-else>
            <a-form-item label="间隔">
              <a-input-number v-model:value="form.intervalValue" :min="intervalMinimum" :max="intervalMaximum" />
            </a-form-item>
            <a-form-item label="单位">
              <a-select v-model:value="form.intervalUnit">
                <a-select-option value="minutes">分钟</a-select-option>
                <a-select-option value="hours">小时</a-select-option>
              </a-select>
            </a-form-item>
          </template>
          <a-form-item label="时区">
            <a-select v-model:value="form.timezone">
              <a-select-option value="Asia/Shanghai">中国标准时间</a-select-option>
              <a-select-option value="UTC">UTC</a-select-option>
              <a-select-option value="Asia/Tokyo">日本标准时间</a-select-option>
              <a-select-option value="America/Los_Angeles">美国太平洋时间</a-select-option>
            </a-select>
          </a-form-item>
        </div>
        <div class="schedule-switch-row">
          <span>保存后启用</span>
          <a-switch v-model:checked="form.enabled" />
        </div>
        <a-collapse ghost class="schedule-advanced">
          <a-collapse-panel key="advanced" header="高级策略">
            <div class="policy-note">
              <strong>并发与审批</strong>
              <span>上次未结束时跳过本次；遇到审批时暂停，等待你处理。</span>
            </div>
            <div class="schedule-time-row">
              <a-form-item label="失败重试次数">
                <a-input-number v-model:value="form.maxRetries" :min="0" :max="5" />
              </a-form-item>
              <a-form-item label="重试间隔（分钟）">
                <a-input-number v-model:value="form.retryIntervalMinutes" :min="1" :max="1440" />
              </a-form-item>
            </div>
          </a-collapse-panel>
        </a-collapse>
      </a-form>
    </a-modal>
  </section>
</template>

<style scoped>
.schedule-panel { display: flex; min-height: 0; height: 100%; flex-direction: column; color: var(--ink); }
.schedule-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 2px 2px 14px; border-bottom: 1px solid var(--line); }
.schedule-toolbar > div { display: flex; flex-direction: column; gap: 2px; }
.schedule-toolbar small { color: var(--muted); }
.schedule-list { min-height: 0; overflow-y: auto; }
.schedule-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 10px 12px; padding: 16px 2px; border-bottom: 1px solid var(--line); }
.schedule-main { min-width: 0; }
.schedule-title-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.schedule-title { display: flex; min-width: 0; align-items: center; gap: 8px; }
.schedule-title strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.schedule-main > p { display: -webkit-box; overflow: hidden; margin: 8px 0; color: var(--muted); font-size: 12px; line-height: 1.55; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.schedule-meta { display: flex; flex-wrap: wrap; gap: 6px 14px; color: var(--muted); font-size: 12px; }
.schedule-meta span { display: inline-flex; align-items: center; gap: 5px; }
.latest-run { display: flex; min-width: 0; align-items: center; gap: 7px; margin-top: 9px; }
.latest-run span { overflow: hidden; color: var(--muted); font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.schedule-actions { display: flex; align-items: flex-start; gap: 5px; }
.run-history { grid-column: 1 / -1; padding: 12px 0 0 24px; border-top: 1px dashed var(--line); }
.run-history-title { display: flex; align-items: center; justify-content: space-between; margin-bottom: 4px; }
.run-row { padding: 12px 0; border-bottom: 1px solid color-mix(in srgb, var(--line) 70%, transparent); }
.run-row:last-child { border-bottom: 0; }
.run-status { display: flex; flex-wrap: wrap; align-items: center; gap: 7px; color: var(--muted); font-size: 12px; }
.run-status time { margin-left: auto; }
.run-result, .run-error { display: -webkit-box; overflow: hidden; margin: 8px 0; font-size: 12px; line-height: 1.6; -webkit-box-orient: vertical; -webkit-line-clamp: 3; }
.run-error { color: #b42318; }
.run-actions { display: flex; justify-content: flex-end; gap: 6px; }
.schedule-form { margin-top: 14px; }
.schedule-time-row { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; }
.schedule-time-row :deep(.ant-input-number), .schedule-time-row :deep(.ant-select) { width: 100%; }
.schedule-switch-row { display: flex; align-items: center; justify-content: space-between; padding: 10px 0 16px; }
.schedule-advanced { border-top: 1px solid var(--line); }
.policy-note { display: flex; flex-direction: column; gap: 4px; margin-bottom: 14px; color: var(--muted); font-size: 12px; }
.policy-note strong { color: var(--ink); }
@media (max-width: 760px) {
  .schedule-row { grid-template-columns: 1fr; }
  .schedule-actions { justify-content: flex-end; }
  .schedule-time-row { grid-template-columns: 1fr; }
  .run-history { padding-left: 0; }
}
</style>
