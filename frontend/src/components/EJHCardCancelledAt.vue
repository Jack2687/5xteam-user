<script setup>
import { computed } from 'vue'
import { cardLifecycle, cardState } from '../ejh'
import { cancellationTime, cancellationCompletionLookup } from '../ejh-cancel-time'
import { formatTime } from '../utils'
const props = defineProps({ card: { type: Object, required: true }, operations: { type: Array, default: () => [] } })
const cancellation = computed(() => cancellationTime(props.card, props.operations))
const completed = computed(() => cardState(props.card)==='2')
const completedAt = computed(() => cancellationCompletionLookup(props.operations)(props.card))
</script>
<template>
  <small v-if="cardLifecycle(card)==='closed'" class="ejh-cancelled-at" :title="completed?'北京时间；卡台确认销卡成功时的记录时间，不使用申请、开卡或后续同步时间':cancellation.detected?'在 EJH 页面提交；此处为系统首次发现时间，上游未提供实际提交时间':'北京时间；使用销卡申请创建时间，不随状态查询改变'">{{ completed?'成功销卡时间':cancellation.detected?'发现销卡时间':'触发销卡时间' }}：{{ completed?(completedAt?formatTime(completedAt):'未记录'):(cancellation.at?formatTime(cancellation.at):'未记录') }}</small>
</template>
<style scoped>.ejh-cancelled-at{display:block;margin-top:5px;color:var(--muted);font-size:11px;line-height:1.5;white-space:nowrap}</style>
