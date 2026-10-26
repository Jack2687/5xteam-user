<script setup>
import { computed } from 'vue'
import StatusPill from './StatusPill.vue'
import OAIConfirmationReply from './OAIConfirmationReply.vue'
import { formatTime } from '../utils'
import { dispositionLabels } from '../pro-disposition'
const props = defineProps({ records: Object, mode: String, labelled: Boolean })
const entries = computed(() => props.mode ? [[props.mode, props.records?.[props.mode]]] : Object.entries(props.records || {}))
const label = record => record?.status === 'confirmed' && record.confirmed_at ? 'OAI 已确认' : ({ waiting: '等待询问', preparing:'预备 Help 会话', running: '询问中', failed: '询问失败', unconfirmed:'OAI 未确认', review:'待核实', stopped:'已停止', disabled: '未启用' }[record?.status] || '未确认')
const tone = record => record?.status === 'confirmed' && record.confirmed_at ? 'success' : ['failed','unconfirmed'].includes(record?.status) ? 'danger' : 'pending'
</script>
<template>
  <div class="oai-confirmation" aria-label="OAI 是否确认">
    <small v-if="labelled" class="oai-heading">OAI 确认</small>
    <span v-if="!entries.length" class="oai-empty">未确认</span>
    <div v-for="[key, record] in entries" :key="key" class="oai-entry">
      <small v-if="!mode && entries.length > 1">{{dispositionLabels[key]}}</small>
      <OAIConfirmationReply :record="record"><StatusPill :tone="tone(record)">{{label(record)}}</StatusPill></OAIConfirmationReply>
      <small v-if="record?.confirmed_at">{{formatTime(record.confirmed_at)}}</small>
      <template v-else-if="record?.status === 'waiting'">
        <small v-if="record.not_before">最早询问 {{formatTime(record.not_before)}}</small>
      </template>
      <small v-if="record?.next_attempt_at">下次尝试 {{formatTime(record.next_attempt_at)}}</small>
      <small v-if="record?.error" class="oai-error" :title="record.error">{{record.error}}</small>
    </div>
  </div>
</template>
<style scoped>
.oai-confirmation{min-width:110px;max-width:220px;font-size:11px}.oai-entry+.oai-entry{margin-top:8px}.oai-confirmation small{display:block;margin-top:4px;color:var(--muted);font-size:10px;line-height:1.5}.oai-heading{margin-bottom:5px}.oai-empty{color:var(--muted)}.oai-error{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
</style>
