<script setup>
import { computed } from 'vue'
import { cardLifecycle } from '../ejh'
import { cancellationTime } from '../ejh-cancel-time'
import { formatTime } from '../utils'
const props = defineProps({ card: { type: Object, required: true }, operations: { type: Array, default: () => [] } })
const cancellation = computed(() => cancellationTime(props.card, props.operations))
</script>
<template>
  <small v-if="cardLifecycle(card)==='closed'" class="ejh-cancelled-at" :title="cancellation.detected?'在 EJH 页面提交；此处为系统首次发现时间，上游未提供实际提交时间':'北京时间；使用销卡申请创建时间，不随状态查询改变'">{{ cancellation.detected?'发现销卡时间':'触发销卡时间' }}：{{ cancellation.at?formatTime(cancellation.at):'未记录' }}</small>
</template>
<style scoped>.ejh-cancelled-at{display:block;margin-top:5px;color:var(--muted);font-size:11px;line-height:1.5;white-space:nowrap}</style>
