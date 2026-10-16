<script setup>
import { computed } from 'vue'
import { returnStatus, returnStatusLabels } from '../ejh'
const props = defineProps({ state: { type: String, default: 'unopened' }, requestedAt: String, showRequestedAt: Boolean, requestLabel: {type:String,default:'申请'} })
const status = computed(() => returnStatus(props.state))
const requestTime = computed(() => {const at=Date.parse(props.requestedAt);return Number.isFinite(at)?new Date(at).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'时间未记录'})
</script>
<template><span class="return-status" :class="'return-status--' + status" :data-return-state="status"><i aria-hidden="true"></i><span class="return-status-text"><span><slot>{{ returnStatusLabels[status] }}</slot></span><small v-if="status==='waiting'&&showRequestedAt" class="return-request-time">{{requestLabel}} {{requestTime}}</small></span></span></template>
<style scoped>
.return-status{display:inline-flex;align-items:center;gap:6px;padding:5px 9px;border-radius:6px;font-size:11px;line-height:1.4;font-weight:600;white-space:nowrap;background:var(--surface-2);color:var(--muted)}
.return-status i{height:6px;width:6px;flex-shrink:0;border-radius:50%;background:currentColor}
.return-status-text{display:flex;flex-direction:column;align-items:flex-start}.return-request-time{display:block;font-size:10px;font-weight:400;line-height:1.5;color:inherit;margin:3px 0 0;white-space:normal;text-align:left}
.return-status--opened{color:var(--blue);background:var(--blue-bg)}
.return-status--waiting,.return-status--unverified,.return-status--unmatched{color:var(--amber);background:var(--amber-bg)}
.return-status--returned{color:var(--green-strong);background:var(--green-bg)}
</style>
