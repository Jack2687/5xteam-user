<script setup>
import { costProvider, costSupported, costText, userCostText } from '../proCost'
import { formatTime } from '../utils'
defineProps({ account: { type: Object, required: true } })
</script>

<template>
  <div class="pro-cost">
    <strong>{{ costText(account) }}</strong>
    <template v-if="costSupported(account)">
      <small class="user-cost">{{ userCostText(account) }}</small>
      <small v-if="account.pro_siwc">原站 ${{Number(account.pro_siwc.carried_cost||0).toFixed(2)}} + SIWC ${{Number(account.pro_siwc.new_cost||0).toFixed(2)}}</small>
      <small v-if="account.cost_checked_at" class="checked-at">{{ formatTime(account.cost_checked_at) }}</small>
    </template>
    <small v-else class="checked-at">{{ costProvider(account).toUpperCase() }} 不统计</small>
  </div>
</template>

<style scoped>
.pro-cost { min-width: 116px; font-variant-numeric: tabular-nums; white-space: nowrap; }
.pro-cost strong { font-weight: 600; }
.pro-cost small { display: block; margin-top: 3px; font-size: 10px; color: var(--text-2, var(--muted)); }
.pro-cost .checked-at { color: var(--muted); }
</style>
