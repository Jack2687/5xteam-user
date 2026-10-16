<script setup>
import { computed } from 'vue'
const props = defineProps({ card: { type: Object, required: true }, operations: { type: Array, default: () => [] } })
const opened = computed(() => {
  const operation = props.operations.filter(o => o.method === 'open' && o.status === 'completed' && (o.card_id === props.card.id || o.order_no && o.order_no === props.card.remote?.orderNo)).sort((a,b) => Date.parse(a.created_at) - Date.parse(b.created_at))[0]
  const value = operation?.created_at || props.card.local?.created_at || props.card.created_at
  const date = new Date(value || '')
  return Number.isFinite(date.getTime()) && date.getFullYear() > 1970 ? new Intl.DateTimeFormat('zh-CN', { year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit',hour12:false,timeZone:'Asia/Shanghai' }).format(date) : '未记录'
})
</script>
<template><span class="ejh-opened-at" title="北京时间；优先使用成功开卡记录，历史导入卡使用首次录入时间">开卡：{{ opened }}</span></template>
<style scoped>.ejh-opened-at{display:block;margin-top:5px;color:var(--muted);font-size:11px;line-height:1.5;white-space:nowrap}</style>
