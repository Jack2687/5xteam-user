<script setup>
import { computed } from 'vue'
import { cardOccupancy, paymentStatusLabel } from '../card-status'
const props = defineProps({ card: { type: Object, required: true } })
const local = computed(() => props.card.local || props.card)
const summary = computed(() => cardOccupancy(props.card))
</script>
<template>
  <div v-if="local.active_accounts" class="card-occupancy">
    <strong>{{summary.label}} · {{local.active_accounts}} 个账号</strong>
    <div v-for="row in local.occupants || []" :key="row.email + ':' + (row.order_id || 'reserved')">
      <span>{{row.email}}{{row.deleted ? ' · 账号已删除' : ''}}</span>
      <small>{{paymentStatusLabel(row.status)}}<template v-if="row.order_id"> · 订单 {{row.order_id}}</template></small>
    </div>
    <span v-if="!local.occupants?.length">{{local.active_email}}</span>
    <small>{{summary.detail}}</small>
  </div>
</template>
<style scoped>
.card-occupancy{font-size:12px;line-height:1.7;overflow-wrap:anywhere;margin:10px 0}.card-occupancy strong{font-weight:500}.card-occupancy span,.card-occupancy small{display:block}.card-occupancy small{font-size:11px;color:var(--muted)}
</style>
