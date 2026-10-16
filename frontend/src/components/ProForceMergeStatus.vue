<script setup>
import {computed} from 'vue'
const props=defineProps({decision:Object,config:Object,unavailable:Boolean,now:Number,compact:Boolean})
const stats=computed(()=>props.decision?.force_merge||{})
const rows=computed(()=>(props.decision?.snapshot?.accounts||[]).filter(a=>a.ejh&&a.ready))
const remaining=a=>a.force_merge_deadline?Math.max(0,(Date.parse(a.force_merge_deadline)-props.now)/60000).toFixed(1)+' 分钟':a.push_timeout_enabled===false?'未开启时限':'时限未知'
const label=computed(()=>props.unavailable?'数据不可用':Number.isFinite(stats.value.average_minutes)?stats.value.average_minutes.toFixed(1)+' 分钟':'无有效计时账号')
const state=computed(()=>props.unavailable?'采集恢复后重新判断':!props.config?.force_merge_enabled?'规则未开启':!props.config?.enabled?'总开关关闭':props.config?.mode!=='auto'?'观察模式':stats.value.triggered?'已触发接替':'未达阈值')
const count=value=>props.unavailable?'—':value||0
</script>
<template><div class="force-merge-status"><span>强制合并前平均剩余</span><strong :class="{compact}">{{label}}</strong><p>{{state}} · 阈值 {{config?.force_merge_minutes||15}} 分钟</p><p>参与 {{count(stats.participants)}} 个 · 临期 {{count(stats.below)}} 个</p><p>待补 {{count(decision?.force_merge_need)}} · 接替中 {{count(stats.pending)}} · 就绪 {{count(stats.ready)}}</p><small>未开启 {{count(stats.untimed)}} · 时限未知 {{count(stats.unknown)}}（均排除）</small><details v-if="rows.length"><summary>逐账号倒计时</summary><div v-for="a in rows" :key="a.id" class="account-countdown"><span>{{a.name||'#'+a.id}}</span><b>{{unavailable?'数据不可用':remaining(a)}}</b></div></details></div></template>
<style scoped>.force-merge-status>span{font-size:12px;color:var(--muted)}.force-merge-status strong{display:block;font-size:22px;margin:15px 0;font-weight:550}.force-merge-status p{font-size:12px;line-height:1.6;margin:6px 0}.force-merge-status small{color:var(--muted);font-size:11px}.force-merge-status summary{cursor:pointer;font-size:12px;margin-top:12px}.account-countdown{display:flex;justify-content:space-between;gap:10px;overflow-wrap:anywhere;padding:8px 0;font-size:11px}.account-countdown b{white-space:nowrap;font-weight:500}</style>
