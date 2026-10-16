<script setup>
import { computed } from 'vue'
import { summarizeReversalFees } from '../ejh-reversal-fees'
const props = defineProps({ records: { type: Array, default: () => [] }, feePercent: { type: Number, default: 6 }, cards: { type: Array, default: () => [] }, operations: { type: Array, default: () => [] }, confirmedOnly: Boolean, hideLabel: Boolean })
const fee = computed(() => summarizeReversalFees(props.records, props.feePercent, props.confirmedOnly, props.cards, props.operations))
</script>

<template>
 <div class="reversal-fee" :class="{'reversal-fee-cell':hideLabel}">
  <span v-if="!hideLabel">授权撤销费</span>
  <div class="reversal-fee-values">
   <strong v-for="row in fee.rows" :key="row.currency">{{row.amount}} {{row.currency}}</strong>
   <strong v-if="!fee.rows.length">{{fee.missing||fee.pending?'待核实':'—'}}</strong>
   <strong v-if="fee.usd.amount!==null && (fee.usd.estimated || fee.rows.some(row=>row.currency!=='USD'))" class="reversal-fee-usd" title="按各卡初始开卡的 USD 结算比例估算">{{fee.usd.estimated?'≈ ':''}}{{fee.usd.amount}} USD{{fee.usd.missing?'（部分）':''}}</strong>
   <small v-if="fee.usd.missing">{{fee.usd.missing}} 笔缺少 USD 折算依据，未计入</small>
   <small v-if="fee.count">按 {{fee.percent}}% 计算</small>
   <small v-if="fee.missing">{{fee.missing}} 笔金额或比例待核实</small>
   <small v-if="fee.pending">{{fee.pending}} 笔待确认，未计入</small>
  </div>
 </div>
</template>

<style scoped>
.reversal-fee{display:flex;justify-content:space-between;align-items:baseline;gap:12px;margin:9px 0 4px;padding-top:8px;border-top:1px solid var(--line);font-size:11px}.reversal-fee>span{color:var(--muted);flex-shrink:0}.reversal-fee-values{text-align:right;min-width:0;font-variant-numeric:tabular-nums}.reversal-fee strong{display:block;font-size:12px;overflow-wrap:anywhere}.reversal-fee small{display:block;font-size:10px;color:var(--muted);line-height:1.6}.reversal-fee-cell{display:block;border:0;margin:0;padding:0}.reversal-fee-cell .reversal-fee-values{text-align:left}
</style>
