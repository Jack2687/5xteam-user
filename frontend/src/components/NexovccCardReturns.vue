<script setup>
import OAIReturnReply from './OAIReturnReply.vue'
import {waitingOAIConfirmedCount} from '../oai-confirmation'
import { computed } from 'vue'
import { returnStates } from '../nexovcc'
import { cardReturnCounts } from '../nexovcc-reconciliation'
import { cardReturnNotice } from '../nexovcc-return-notice'
import NexovccReturnNotice from './NexovccReturnNotice.vue'
import { formatTime } from '../utils'
const props=defineProps({row:{type:Object,required:true},cases:{type:Array,default:()=>[]},data:{type:Object,default:()=>({})}})
defineEmits(['inspect'])
const counts=computed(()=>cardReturnCounts(props.cases))
const notice=computed(()=>cardReturnNotice(props.data,props.row))
const latestRequest=computed(()=>props.cases.filter(c=>c.state==='waiting'&&c.requested_at&&!c.requested_at.startsWith('0001')).map(c=>c.requested_at).sort().at(-1))
</script>
<template>
 <div class="card-returns" aria-label="账号回款状态">
  <div v-if="row.local?.active_accounts||row.local?.pending_accounts" class="occupants"><strong>占用 {{row.local.active_accounts||0}} · 在途 {{row.local.pending_accounts||0}}</strong><button v-for="occupant in row.local.occupants||[]" :key="occupant.email+':'+occupant.order_id" :title="occupant.email" @click="$emit('inspect',{type:'accounts',search:occupant.email})">{{occupant.email}}<small v-if="occupant.deleted">账号已删除</small></button><small v-if="!row.local.occupants?.length">{{row.local.active_email||'等待流程更新'}}</small></div>
  <div class="return-badges"><OAIReturnReply v-for="(name,state) in returnStates" :key="state" :rows="state==='waiting'?cases:[]" within-button><button :class="state" @click="$emit('inspect',{type:'accounts',state})">{{name}} <b>{{counts[state]}}</b><small v-if="state==='waiting'&&waitingOAIConfirmedCount(cases)>0" class="oai-return-highlight">✓ OAI 已确认{{counts.waiting>1?' '+waitingOAIConfirmedCount(cases)+'/'+counts.waiting:''}}</small><small v-if="state==='waiting'&&latestRequest">申请 {{formatTime(latestRequest)}}</small></button></OAIReturnReply></div>
  <NexovccReturnNotice :notice="notice"/>
 </div>
</template>
<style scoped>
.return-badges :deep(.oai-reply-anchor){display:flex}.return-badges button{width:100%}
.oai-return-highlight{color:var(--green-strong);background:var(--green-bg);padding:3px 5px;border-radius:4px}
.card-returns{min-width:200px;font-size:11px}.occupants{display:grid;gap:6px;margin-bottom:12px;max-width:260px}.occupants strong{font-weight:500;color:var(--muted)}.occupants button{background:none;border:0;padding:0;text-align:left;overflow-wrap:anywhere;color:var(--green-strong);font:inherit;cursor:pointer}.occupants small{display:block;font-size:10px;color:var(--muted)}.return-badges{display:grid;grid-template-columns:auto auto;gap:7px}.return-badges button{border:1px solid var(--line);border-radius:6px;background:var(--bg);color:var(--muted);padding:6px 8px;font:inherit;text-align:left;cursor:pointer}.return-badges button:hover{border-color:currentColor}.return-badges b{margin-left:5px;font-weight:600}.return-badges .opened{background:#eaf2ff;color:#376ec2}.return-badges .waiting{background:#fff6e4;color:#91651d}.return-badges .returned{background:var(--green-soft);color:var(--green-strong)}.return-badges small{display:block;font-size:9px;white-space:nowrap;margin-top:3px}
</style>
