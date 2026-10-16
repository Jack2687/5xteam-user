<script setup>
import ActivationCost from './ActivationCost.vue'
import EJHCaseState from './EJHCaseState.vue'
import { formatTime } from '../utils'
import { paymentStatusLabel } from '../card-status'
defineProps({ row: { type: Object, required: true }, compact: Boolean, editableState: Boolean, disabled: Boolean })
defineEmits(['updated'])
</script>
<template><div class="return-case-line"><strong>{{row.email}}</strong><EJHCaseState :row="row" :editable="editableState" :disabled="disabled" @updated="$emit('updated',$event)"/><small>开通订单：{{paymentStatusLabel(row.payment_status)}} · {{row.id}}</small><small>{{row.plan || '—'}} · {{row.provider || '—'}} · {{formatTime(row.opened_at)}}{{row.deleted ? ' · 账号已删除，历史保留' : ''}}</small><ActivationCost :row="row" :details="!compact" :editable="!compact"/></div></template>
<style scoped>.return-case-line{display:flex;flex-wrap:wrap;align-items:center;gap:8px;margin:12px 0;overflow-wrap:anywhere}.return-case-line :deep(.activation-cost){flex-basis:100%}.return-case-line strong{font-size:12px;white-space:normal}.return-case-line small{flex-basis:100%;font-size:11px;color:var(--muted);line-height:1.7}</style>
