<script setup>
import {computed,ref} from 'vue'
import {LoaderCircle,MessageSquareText} from 'lucide-vue-next'
import {api} from '../api'
import {formatTime} from '../utils'
import OAIConfirmationStatus from './OAIConfirmationStatus.vue'
import OAIConfirmationReply from './OAIConfirmationReply.vue'
const props=defineProps({account:Object,mode:String,orderId:String,actionsAvailable:{type:Boolean,default:true}})
const emit=defineEmits(['updated'])
const record=computed(()=>props.account?.pro_oai_confirmations?.[props.mode])
const historicalComplete=computed(()=>props.account?.pro_oai_available_modes?.includes(props.mode)||({merge:props.account?.pro_remove_status==='completed' && props.account?.pro_transfer_status==='completed',deactivate:!!props.account?.pro_deactivation?.confirmed_at,merchant:!!props.account?.pro_merchant?.retired_at && props.account?.pro_merchant?.status==='completed',merge_v2:props.account?.pro_merge_v2?.status==='completed'}[props.mode]))
const busy=ref(false),error=ref('')
const stages=[['wait','等待'],['login','登录'],['authorize','Help 授权'],['send','提问'],['reply','回答'],['confirm','确认']]
const labels={waiting:'等待中',running:'执行中',completed:'已完成',confirmed:'已确认',unconfirmed:'未确认',review:'待核实',failed:'失败',interrupted:'已停止',skipped:'无需执行'}
async function action(action){busy.value=true;error.value='';try{await api(`/api/pro-accounts/${encodeURIComponent(props.account.email)}/oai-confirmation`,{method:'POST',body:{mode:props.mode,action,order_id:props.orderId}});emit('updated')}catch(e){error.value=e.message}finally{busy.value=false}}
</script>
<template><div class="pro-oai-flow">
 <OAIConfirmationStatus :records="account?.pro_oai_confirmations" :mode="mode"/>
 <div v-if="actionsAvailable && !record && historicalComplete" class="oai-actions"><button type="button" :disabled="busy" @click="action('retry')">开始确认</button></div>
 <div v-if="record && record.status!=='disabled'" class="oai-steps" aria-label="OAI 确认流程">
  <span v-for="[key,name] in stages" :key="key" :data-stage="key" :class="record.steps?.[key] || 'pending'" :title="name+'：'+(labels[record.steps?.[key]]||'未开始')+(record.step_times?.[key]?' · '+formatTime(record.step_times[key]):'')"><LoaderCircle v-if="record.steps?.[key]==='running'" :size="11" class="spin"/>{{name}}<small>{{labels[record.steps?.[key]]||'未开始'}}</small></span>
 </div>
 <div v-if="actionsAvailable && record && record.completed_at && !record.completed_at.startsWith('0001') && !record.confirmed_at" class="oai-actions">
  <small v-if="record.attempts">尝试 {{record.attempts}} / {{record.max_attempts||3}}</small>
  <button v-if="['waiting','running'].includes(record.status)||record.next_attempt_at||(record.status==='review'&&record.checked_at&&record.attempts<(record.max_attempts||3))" type="button" :disabled="busy" @click="action('stop')">停止确认</button>
  <button v-if="record.status==='waiting'" type="button" :disabled="busy" @click="action('retry')">立即确认</button>
  <button v-if="['failed','unconfirmed','review','stopped','disabled'].includes(record.status)" type="button" :disabled="busy" @click="action('retry')">重试确认</button>
 </div>
 <div v-if="actionsAvailable && record && record.completed_at && !record.completed_at.startsWith('0001') && record.confirmation_source!=='card_return'" class="oai-actions"><button type="button" :disabled="busy" title="重新登录并授权 Help，从头执行；历史回答保留" @click="action('restart')">重新执行</button></div>
 <small v-if="error" class="danger-text">{{error}}</small>
 <div class="oai-result">
  <OAIConfirmationReply :record="record" trigger-label="结果情况，悬停查看 Help 完整回答"><span class="oai-result-button"><MessageSquareText :size="12"/><span>结果情况</span><small>{{record?.answer||record?.replies?.length?'查看回答':record?.error?'查看情况':'暂无回答'}}</small></span></OAIConfirmationReply>
 </div>
</div></template>
<style scoped>
.oai-result{margin-top:8px}.oai-result-button{display:inline-flex;align-items:center;gap:5px;padding:5px 7px;border:1px solid var(--line);border-radius:5px;font-size:11px;color:var(--green-strong);background:var(--surface)}.oai-result-button small{margin-left:4px;font-size:10px;color:var(--muted)}.oai-result-button:hover{border-color:var(--green);background:var(--green-bg)}
.pro-oai-flow{min-width:290px;max-width:340px}.oai-steps{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:4px;margin-top:8px}.oai-steps>span{display:flex;align-items:center;gap:4px;padding:4px 5px;font-size:10px;border:1px solid var(--line);border-radius:5px;white-space:nowrap;color:var(--muted)}.oai-steps small{font-size:9px}.completed,.confirmed{color:var(--green-strong)!important;background:var(--green-bg)}.running,.waiting,.review{color:var(--amber)!important;background:var(--amber-bg)}.failed,.unconfirmed,.interrupted{color:var(--red)!important;background:var(--red-bg)}.oai-actions{display:flex;align-items:center;gap:10px;margin-top:8px;font-size:10px;color:var(--muted)}.oai-actions button{padding:3px 6px;border:1px solid var(--line);border-radius:4px}.spin{animation:oai-spin 1s linear infinite}@keyframes oai-spin{to{transform:rotate(360deg)}}
</style>
