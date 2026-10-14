<script setup>
import { computed,onMounted,onBeforeUnmount,ref } from 'vue'
import { formatTime } from '../utils'
const props=defineProps({account:Object}),now=ref(Date.now());let timer
onMounted(()=>{timer=setInterval(()=>now.value=Date.now(),1000)})
onBeforeUnmount(()=>clearInterval(timer))
const elapsed=computed(()=>Math.max(0,Math.floor((now.value-Date.parse(props.account.pro_push_started_at))/60000)))
const remaining=computed(()=>Math.ceil((Date.parse(props.account.force_merge_deadline)-now.value)/60000))
</script>
<template><span v-if="!account.pro_push_started_at">未开始</span><template v-else><strong>{{elapsed}} 分钟</strong><small :title="formatTime(account.pro_push_started_at)">首次推送起算</small><small v-if="account.space_merged_once">已合并</small><small v-else-if="account.push_status!=='completed'">等待推送成功</small><small v-else-if="account.force_merge_deadline" :title="formatTime(account.force_merge_deadline)">{{remaining>0?'距超时 '+remaining+' 分钟':'已到阈值，等待调度'}}</small><small v-if="account.pro_auto?.status==='stopped'||account.pro_migration?.paused">已停止 / 暂停</small><small v-if="account.pro_merge_trigger==='push_timeout'">触发：推送超时</small></template></template>
<style scoped>small{display:block;font-size:11px;color:var(--muted);margin-top:4px}</style>
