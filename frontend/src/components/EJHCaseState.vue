<script setup>
import { computed, nextTick, ref } from 'vue'
import { api } from '../api'
import { returnStates } from '../ejh'
import { formatTime } from '../utils'
import ReturnStatusBadge from './ReturnStatusBadge.vue'
const props = defineProps({ row: { type: Object, required: true }, editable: Boolean, disabled: Boolean })
const emit = defineEmits(['updated'])
const opened = ref(false), busy = ref(false), error = ref(''), reason = ref(''), state = ref(''), confirmed = ref(false), original = ref(null), select = ref(null), restoring = ref(false)
const manual = computed(() => !!props.row.state_override?.state)
const effective = computed(() => restoring.value ? original.value?.automatic_state : state.value)
const release = computed(() => ['returned', 'unopened'].includes(effective.value))
const canSave = computed(() => !busy.value && confirmed.value && reason.value.trim().length >= 3 && reason.value.trim().length <= 600)
async function open() {
 if(props.disabled || busy.value) return
 original.value = JSON.parse(JSON.stringify(props.row))
 state.value = props.row.state; reason.value = ''; confirmed.value = false; restoring.value = false; error.value = ''; opened.value = true
 await nextTick(); select.value?.focus()
}
async function save() {
 if(!canSave.value) return
 busy.value = true; error.value = ''
 try {
  const row = await api('/api/ejh/cases/'+encodeURIComponent(original.value.id)+'/state', { method:'PUT', body:{state:restoring.value?'auto':state.value, reason:reason.value.trim(), expected_state:original.value.state, expected_revision:original.value.state_override?.revision||0, confirm:true} })
  emit('updated', row); opened.value = false
 } catch(e) { error.value = e.message } finally { busy.value = false }
}
</script>
<template>
 <span class="case-state"><ReturnStatusBadge :state="row.state"/><small v-if="manual" class="manual-state" :title="row.state_override.reason+' · '+formatTime(row.state_override.updated_at)">人工设置</small><button v-if="editable" type="button" class="btn ghost compact" :disabled="disabled||busy" :aria-label="'调整 '+row.email+' 的回款状态'" @click.stop="open">调整状态</button></span>
 <Teleport to="body"><div v-if="opened" class="modal-backdrop case-state-overlay" @click.self="!busy&&(opened=false)" @keydown.esc.stop="!busy&&(opened=false)"><form class="modal case-state-dialog" role="dialog" aria-modal="true" aria-label="调整账号回款状态" @submit.prevent="save">
 <h2>调整账号回款状态</h2><p><strong>{{original.email}}</strong><br/>卡片 {{original.card_name||'EJH'}} · 尾号 {{original.last4||'—'}}<br/><small>开通订单 {{original.id}}</small></p>
 <p>当前状态：{{returnStates[original.state]}}<br/>自动判断：{{returnStates[original.automatic_state]||returnStates[original.state]}}</p>
 <p v-if="original.state_override?.state" class="muted">上次人工设置：{{formatTime(original.state_override.updated_at)}}<br/>{{original.state_override.reason}}</p>
 <label class="field"><span>回款状态</span><select ref="select" v-model="state" :disabled="busy||restoring" @change="confirmed=false"><option v-for="(label,value) in returnStates" :key="value" :value="value">{{label}}</option></select></label>
 <label v-if="original.state_override?.state" class="state-confirm"><input v-model="restoring" type="checkbox" :disabled="busy" @change="confirmed=false"/>恢复自动判断</label>
 <p class="state-effect">{{release?'将解除这笔订单的占用；卡片仍须启用、状态正常且有剩余名额，才可再次使用。':'这笔订单将保持占用，不能作为空闲卡再次分配，也不能销卡。'}}{{effective==='unopened'?'设为未开通后将从账号回款列表隐藏，仍可在卡片关联账号中查看和调整。':''}}</p>
 <p class="muted">仅修改本地回款状态，不发起付款、退款或销卡。累计开通次数与真实流水金额保留。{{restoring?'取消人工覆盖后，按订单和流水重新判断。':'人工状态会持续生效，自动同步不会覆盖；可随时恢复自动判断。'}}</p>
 <label class="field"><span>调整原因 / 核实依据</span><textarea v-model="reason" :disabled="busy" minlength="3" maxlength="600" required placeholder="填写已核实的结果或修改原因"/></label>
 <label class="state-confirm"><input v-model="confirmed" type="checkbox" :disabled="busy"/>我已核实该账号，确认按上述状态调整</label>
 <p v-if="error" role="alert" class="danger-text">{{error}}</p><p v-if="busy" role="status">正在保存状态并更新卡片占用…</p>
 <div class="panel-actions"><button type="submit" class="btn primary" :disabled="!canSave">{{busy?'保存中…':restoring?'恢复自动判断':'保存状态'}}</button><button type="button" class="btn ghost" :disabled="busy" @click="opened=false">取消</button></div>
 </form></div></Teleport>
</template>
<style scoped>
.case-state{display:inline-flex;align-items:center;gap:7px;flex-wrap:wrap}.manual-state{color:var(--muted);font-size:11px}.case-state-overlay{z-index:1200}.case-state-dialog{width:min(560px,calc(100vw - 32px));max-height:90vh;overflow:auto}.case-state-dialog p{line-height:1.7;overflow-wrap:anywhere}.state-effect{padding:12px;border-radius:8px;background:var(--surface-soft);border:1px solid var(--border)}.state-confirm{display:flex;align-items:flex-start;gap:8px;margin:14px 0;font-size:13px}.case-state-dialog textarea{min-height:80px}
</style>
