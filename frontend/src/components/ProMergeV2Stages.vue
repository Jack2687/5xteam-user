<script setup>
import { computed } from 'vue'
import { formatTime } from '../utils'
import CompactErrorDetails from './CompactErrorDetails.vue'
const props=defineProps({account:{type:Object,required:true},disabled:Boolean,readonly:Boolean})
const emit=defineEmits(['retry','change'])
const state=computed(()=>props.account.pro_merge_v2 || {})
const steps=[['invite','邀请'],['login','团队登录'],['submit','提交停用'],['remove','移出团队'],['groups','后续分组']]
const names={not_started:'未开始',pending:'待处理',running:'执行中',completed:'完成',failed:'失败',unknown:'待核实',interrupted:'已中断'}
function change(key,label,event){const status=event.target.value;event.target.value=state.value.steps?.[key]||'not_started';emit('change',{key,label,status})}
</script>
<template>
  <div class="merge-v2-flow">
    <div class="merge-v2-steps"><label v-for="[key,label] in steps" :key="key" :class="state.steps?.[key]"><b>{{label}}</b><small v-if="readonly">{{names[state.steps?.[key]]||'未开始'}}</small><select v-else :aria-label="'合并空间2.0'+label+'状态'" :value="state.steps?.[key]||'not_started'" :disabled="disabled || !account.pro_merge_v2 || !!state.completed_at || !!state.accepted_at && ['invite','login','submit'].includes(key)" @change="change(key,label,$event)"><option v-for="(text,status) in names" :key="status" :value="status" :disabled="status==='running'">{{text}}</option></select><small v-if="state.manual?.[key]" title="手动修正">手</small></label></div>
    <small v-if="state.started_at">开始：{{formatTime(state.started_at)}}</small>
    <small v-if="state.accepted_at">受理：{{formatTime(state.accepted_at)}}</small>
    <small v-if="state.completed_at">完成：{{formatTime(state.completed_at)}}</small>
    <small v-if="account.pro_auto?.stop_requested && !state.completed_at">已停止。已发出的上游请求无法撤回，续跑仅处理剩余步骤。</small>
    <small v-else-if="state.status==='unknown'">请求结果待核实，保留团队成员，禁止自动重发。</small>
    <small>受理及流程完成不代表上游停用或退款完成。</small>
    <CompactErrorDetails :messages="[state.error,account.pro_post_merge_groups?.error,account.vm2api?.error]"/>
    <button v-if="account.pro_merge_v2 && !state.completed_at" type="button" class="btn ghost compact" :disabled="disabled || state.status==='unknown'" @click="$emit('retry')">执行 / 续跑合并空间2.0</button>
  </div>
</template>
<style scoped>
.merge-v2-flow{width:330px;display:grid;gap:5px;white-space:normal}.merge-v2-steps{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:4px}.merge-v2-steps label{display:flex;align-items:center;gap:5px;padding:4px 6px;border:1px solid var(--line);border-radius:5px;font-size:11px}.merge-v2-flow small{font-size:11px;color:var(--muted)}.merge-v2-steps select{min-width:0;width:78px;border:0;background:transparent;color:inherit;padding:0;font:inherit}.merge-v2-steps .completed{background:var(--green-bg);color:var(--green-strong)}.merge-v2-steps .running,.merge-v2-steps .unknown{background:var(--amber-bg);color:var(--amber)}.merge-v2-steps .failed{background:var(--red-bg);color:var(--red)}.merge-v2-flow button{justify-self:start}
</style>
