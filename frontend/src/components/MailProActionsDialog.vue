<script setup>
import { computed, onBeforeUnmount, reactive, ref } from 'vue'
import { CheckCircle2, History, LoaderCircle, MessageSquareText, Play, Square, X } from 'lucide-vue-next'
import { api } from '../api'
import { formatTime } from '../utils'
import { dispositionLabels } from '../pro-disposition'
import ProOAIConfirmationStages from './ProOAIConfirmationStages.vue'
import ProAccountLogDialog from './ProAccountLogDialog.vue'

const emit = defineEmits(['updated'])
const props = defineProps({ defaultPageSize: { type: Number, default: 10 } })
const view = reactive({ account: null, mode: '', oaiMode: '', tab: 'final', loading: false, busy: false, error: '', complete: false, paused: false })
const logs = ref(null)
let timer, generation = 0
const base = () => `/api/mail/accounts/${encodeURIComponent(view.account.email)}`
const active = computed(() => !!view.account?.pro_workflow_running)
const modeLabel = computed(() => dispositionLabels[view.mode] || '尚未启用收尾流程')
const oaiModes = computed(() => [...new Set([view.oaiMode, ...Object.keys(view.account?.pro_oai_confirmations || {})].filter(Boolean))])
const states = { pending: '未开始', not_started: '未开始', waiting: '等待中', queued: '排队中', running: '执行中', completed: '已完成', team_removed: '已移出', failed: '失败', interrupted: '已中断', unknown: '待核实' }
const flow = computed(() => {
  const p = view.account || {}
  if (view.mode === 'merge_v2') return { record: p.pro_merge_v2, steps: [['invite','邀请'],['login','团队登录'],['submit','提交停用'],['remove','移出团队'],['groups','后续分组']] }
  if (view.mode === 'deactivate') return { record: p.pro_deactivation, steps: [['credentials','凭据'],['verify','验证'],['submit','提交删除'],['confirm','结果核验']] }
  if (view.mode === 'merchant') {
    const m = p.pro_merchant || {}
    return { record: { ...m, steps: { submit: m.accepted_at ? 'completed' : m.status === 'failed' ? 'failed' : m.submitted_at ? 'running' : 'pending', query: m.completed_at ? 'completed' : m.status === 'failed' ? 'failed' : m.accepted_at ? 'waiting' : 'pending', groups: m.retired_at ? 'completed' : 'pending' } }, steps: [['submit','提交申请'],['query','查询结果'],['groups','后续处理']] }
  }
  return { record: { steps: { invite: p.pro_invite_status, accept: p.pro_accept_status, transfer: p.pro_transfer_status, remove: p.pro_remove_status }, error: p.pro_last_error }, steps: [['invite','邀请'],['accept','进入空间'],['transfer','合并空间'],['remove','移出团队']] }
})
const unknown = computed(() => flow.value.record?.status === 'unknown' || view.account?.pro_transfer_status === 'unknown')
const errors = computed(() => [...new Set([view.error, flow.value.record?.error, view.account?.pro_last_error, view.account?.pro_post_merge_groups?.error].filter(Boolean))])
const finalLabel = computed(() => view.complete ? '收尾已完成' : active.value ? '收尾执行中' : view.account?.mail_pro_actions?.final_mode ? '继续收尾' : '执行收尾')

function close() { generation++; clearTimeout(timer); view.account = null; emit('updated') }
async function refresh() {
  if (!view.account) return
  const current = generation
  const data = await api(`${base()}/pro-actions`)
  if (current !== generation) return
  view.account = data.account
  view.mode = data.mode
  view.oaiMode = data.oai_mode
  view.complete = data.final_completed
  view.paused = data.auto_paused
}
function schedule() {
  clearTimeout(timer)
  if (!view.account) return
  const current = generation
  timer = window.setTimeout(async () => {
    if (view.busy) { schedule(); return }
    try { await refresh() } catch (error) { if (current === generation) view.error = error.message }
    if (current === generation) schedule()
  }, 2000)
}
async function runFinal() {
  if (view.busy || !view.mode || active.value || view.complete || unknown.value) return
  const current = generation
  view.busy = true; view.error = ''
  try {
    await api(`${base()}/final-step`, { method: 'POST', body: { mode: view.mode } })
    if (current === generation) { await refresh(); emit('updated') }
  } catch (error) { if (current === generation) view.error = error.message }
  finally { if (current === generation) view.busy = false }
}
async function stop() {
  const current = generation
  view.busy = true; view.error = ''
  try { await api(`${base()}/final-step/stop`, { method: 'POST', body: {} }); if (current === generation) await refresh() }
  catch (error) { if (current === generation) view.error = error.message }
  finally { if (current === generation) view.busy = false }
}
async function updated() {
  try { await refresh(); emit('updated') } catch (error) { view.error = error.message }
}
async function open(account, tab = 'final', execute = true) {
  close()
  const current = generation
  Object.assign(view, { account, mode: '', oaiMode: '', tab, loading: true, busy: false, error: '', complete: false, paused: false })
  try {
    await refresh()
    if (current !== generation) return
    if (execute && tab === 'final') await runFinal()
    if (execute && tab === 'oai' && !view.account.pro_oai_confirmations?.[view.oaiMode]) {
      await api(`${base()}/oai-confirmation`, { method: 'POST', body: { mode: view.oaiMode, action: 'retry' } })
      if (current === generation) { await refresh(); emit('updated') }
    }
  } catch (error) { if (current === generation) view.error = error.message }
  finally { if (current === generation) { view.loading = false; schedule() } }
}
onBeforeUnmount(() => { generation++; clearTimeout(timer) })
defineExpose({ open })
</script>

<template>
  <Teleport to="body">
    <div v-if="view.account" class="modal-backdrop mail-actions-backdrop" @click.self="close">
      <section class="mail-actions-dialog panel" role="dialog" aria-modal="true" aria-label="邮件账号流程" @keydown.esc="close">
        <header><div><small>邮件管理 · 独立执行记录</small><h2>{{ view.account.email }}</h2></div><button type="button" class="icon-button" aria-label="关闭邮件账号流程" @click="close"><X :size="18"/></button></header>
        <nav aria-label="邮件账号流程切换"><button :class="{active:view.tab==='final'}" @click="view.tab='final'"><CheckCircle2 :size="15"/>收尾流程</button><button :class="{active:view.tab==='oai'}" @click="view.tab='oai'"><MessageSquareText :size="15"/>OAI 确认</button></nav>
        <p class="table-note">账号保留在邮件管理；使用 Pro 管理的流程、代理、并发及重试配置。关闭窗口不会中断后台任务。</p>
        <p v-if="view.loading"><LoaderCircle :size="15" class="spin"/>正在读取流程…</p>
        <template v-else-if="view.tab==='final'">
          <div class="mail-flow-heading"><strong>{{modeLabel}}</strong><span>{{active?'后台执行中':view.complete?'已完成':view.account.pro_auto?.stop_requested?'已停止':'等待执行'}}</span></div>
          <p v-if="view.account.mail_pro_actions?.final_mode" class="table-note">本账号已固定为「{{modeLabel}}」，续跑沿用原流程。</p>
          <p v-if="!view.mode" class="table-note">请先在 Pro 管理中开启一种收尾方式，然后重新打开此窗口。</p>
          <div v-else class="mail-final-steps" aria-label="收尾步骤">
            <div v-for="[key,label] in flow.steps" :key="key" :class="flow.record?.steps?.[key]" :data-stage="key"><span>{{label}}</span><strong><LoaderCircle v-if="flow.record?.steps?.[key]==='running'" :size="13" class="spin"/>{{states[flow.record?.steps?.[key]]||'未开始'}}</strong></div>
          </div>
          <p v-if="flow.record?.message">{{flow.record.message}}</p>
          <small v-if="flow.record?.accepted_at">受理时间：{{formatTime(flow.record.accepted_at)}}</small>
          <small v-if="flow.record?.next_check_at">下次查询：{{formatTime(flow.record.next_check_at)}}</small>
          <p v-if="unknown" class="danger-text">提交结果待核实，请勿重复提交。原进度与检查点已保留。</p>
          <p v-for="error in errors" :key="error" class="danger-text" role="alert">{{error}}</p>
          <div class="mail-flow-actions"><button class="btn primary" :disabled="view.busy||!view.mode||active||view.complete||unknown" @click="runFinal"><Play :size="14"/>{{finalLabel}}</button><button v-if="view.account.mail_pro_actions?.final_mode && !view.complete && !view.account.pro_auto?.stop_requested" class="btn ghost" :disabled="view.busy" @click="stop"><Square :size="13"/>停止收尾</button><button v-if="view.account.mail_pro_actions" class="btn ghost" @click="logs?.open(view.account)"><History :size="14"/>查看流程日志</button></div>
        </template>
        <template v-else>
          <p class="table-note">手动确认可用于外部账号，仅询问处理结果；不会执行合并、删除或更改回款状态。</p>
          <p v-if="view.error" class="danger-text" role="alert">{{view.error}}</p>
          <div v-for="mode in oaiModes" :key="mode" class="mail-oai-record"><strong>{{dispositionLabels[mode]}} · OAI 确认</strong><ProOAIConfirmationStages :account="view.account" :mode="mode" mail-scope allow-standalone @updated="updated"/></div>
        </template>
      </section>
    </div>
  </Teleport>
  <ProAccountLogDialog ref="logs" :default-page-size="props.defaultPageSize" endpoint-prefix="/api/mail/accounts" mail-scope/>
</template>

<style scoped>
.mail-actions-backdrop{z-index:70}.mail-actions-dialog{width:min(820px,calc(100vw - 32px));max-height:88vh;overflow:auto;padding:24px;display:grid;gap:16px}.mail-actions-dialog header{display:flex;justify-content:space-between;align-items:flex-start;gap:18px}.mail-actions-dialog h2{font-size:17px;overflow-wrap:anywhere;margin:6px 0 0}.mail-actions-dialog header small{color:var(--muted)}.mail-actions-dialog nav{display:flex;gap:18px;border-bottom:1px solid var(--line)}.mail-actions-dialog nav button{display:flex;align-items:center;gap:6px;padding:10px 0;border-bottom:2px solid transparent;color:var(--muted)}.mail-actions-dialog nav button.active{color:var(--green-strong);border-color:var(--green)}.mail-flow-heading{display:flex;justify-content:space-between;gap:12px}.mail-flow-heading span{font-size:12px;color:var(--muted)}.mail-final-steps{display:flex;flex-wrap:wrap;gap:8px}.mail-final-steps>div{flex:1;min-width:100px;padding:12px;border:1px solid var(--line);border-radius:8px;display:grid;gap:8px}.mail-final-steps span{font-size:12px;color:var(--muted)}.mail-final-steps strong{font-size:12px;display:flex;gap:5px;align-items:center}.mail-final-steps .completed{background:var(--green-bg);color:var(--green-strong)}.mail-final-steps .running,.mail-final-steps .waiting,.mail-final-steps .unknown{background:var(--amber-bg);color:var(--amber)}.mail-final-steps .failed{background:var(--red-bg);color:var(--red)}.mail-flow-actions{display:flex;flex-wrap:wrap;gap:10px}.mail-oai-record{display:grid;gap:12px;padding:16px;border:1px solid var(--line);border-radius:8px}.mail-oai-record :deep(.pro-oai-flow){min-width:0;max-width:none}.mail-actions-dialog p{margin:0;font-size:12px;line-height:1.7;overflow-wrap:anywhere}.mail-actions-dialog small{font-size:11px}
</style>
