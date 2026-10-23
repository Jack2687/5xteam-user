<script setup>
import { computed } from 'vue'
import { LoaderCircle } from 'lucide-vue-next'
import { formatTime } from '../utils'
import CompactErrorDetails from './CompactErrorDetails.vue'
const props = defineProps({ account: { type: Object, required: true }, disabled: Boolean })
const emit = defineEmits(['retry', 'change'])
const state = computed(() => props.account.pro_deactivation || {})
const steps = [['credentials', '凭据'], ['verify', '验证'], ['submit', '提交'], ['confirm', '确认']]
const names = { not_started: '未开始', pending: '待处理', running: '执行中', completed: '成功', waiting: '等待确认', failed: '失败', unknown: '结果待核验', interrupted: '已中断' }
const groups = computed(() => props.account.pro_post_merge_groups)
const errors = computed(() => [state.value.error, groups.value?.error])
const summary = computed(() => {
  if (state.value.confirmed_at && state.value.confirmation_source === 'sub2_http_401') return '删除已确认（Sub2 测试 401）'
  if (state.value.confirmed_at) return state.value.confirmation_source === 'http_401' ? '删除已确认（核验 HTTP 401）' : '删除已确认'
  if (state.value.steps?.credentials === 'running') return '正在重新登录'
  if (state.value.submitted_at) return '已提交，等待确认'
  if (state.value.error) return '删除未完成'
  return state.value.message ? '删除处理中' : ''
})
function change(key,label,event) { const status=event.target.value; event.target.value=state.value.steps?.[key]||'not_started'; emit('change',{key,label,status}) }
</script>
<template>
  <div class="deletion-flow">
    <div class="deletion-steps"><span v-for="[key,label] in steps" :key="key" :class="state.steps?.[key]" :title="state.manual?.[key] ? '手动修正：'+formatTime(state.manual[key].updated_at) : ''"><LoaderCircle v-if="state.steps?.[key] === 'running'" class="spin" :size="11"/><b>{{ label }}</b><select :value="state.steps?.[key] || 'not_started'" :aria-label="'删除账户'+label+'阶段状态'" :disabled="disabled" @change="change(key,label,$event)"><option value="not_started">未开始</option><option value="pending">待处理</option><option v-if="state.steps?.[key] === 'running'" value="running" disabled>执行中</option><option v-if="key === 'confirm'" value="waiting">等待确认</option><option value="completed">成功</option><option value="failed">失败</option><option value="unknown">待核实</option><option value="interrupted">已中断</option></select><i v-if="state.manual?.[key]" class="manual-mark">手</i></span></div>
    <small v-if="summary" :title="state.message">{{ summary }}</small>
    <small v-if="state.accepted_at">提交成功：{{ formatTime(state.accepted_at) }}</small>
    <small v-else-if="state.submitted_at">提交请求：{{ formatTime(state.submitted_at) }}</small>
    <small v-if="state.confirmed_at">{{ state.confirmation_source === 'manual' ? '手动确认' : '确认时间' }}：{{ formatTime(state.confirmed_at) }}</small>
    <small v-if="state.confirmed_at">已停止定时续跑</small>
    <small v-else-if="state.next_check_at">下次核验：{{ formatTime(state.next_check_at) }}</small>
    <small v-if="groups" :class="{'danger-text':groups.error}">分组调整：{{ names[groups.status] || '待执行' }}</small>
    <CompactErrorDetails :messages="errors" />
    <button v-if="state.submitted_at && (state.error || groups && groups.status !== 'completed')" type="button" class="btn ghost compact" :disabled="disabled" @click="$emit('retry')">{{ state.confirmed_at ? '续跑分组调整' : '续跑核验及分组调整' }}</button>
  </div>
</template>
<style scoped>
.deletion-flow{width:310px;min-width:280px;max-width:320px;display:grid;gap:5px;white-space:normal}.deletion-steps{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:4px}.deletion-steps>span{display:flex;align-items:center;gap:5px;padding:4px 6px;border:1px solid var(--line);border-radius:5px;font-size:11px;color:var(--muted)}.deletion-flow small{font-size:11px;color:var(--muted);overflow-wrap:anywhere}.deletion-steps .completed{color:var(--green-strong);background:var(--green-bg)}.deletion-steps .running,.deletion-steps .waiting,.deletion-steps .unknown{color:var(--amber);background:var(--amber-bg)}.deletion-steps .failed{color:var(--red);background:var(--red-bg)}.spin{animation:spin 1s linear infinite}@keyframes spin{to{transform:rotate(360deg)}}
</style>
<style scoped>
.deletion-steps select{width:82px;min-width:0;border:0;background:transparent;color:inherit;padding:0;font:inherit;cursor:pointer}.deletion-steps select:disabled{cursor:default}.deletion-steps select:focus-visible{outline:2px solid var(--green);outline-offset:2px}.manual-mark{font-size:9px;font-style:normal;border:1px solid currentColor;border-radius:3px;padding:0 2px}
</style>
