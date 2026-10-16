<script setup>
import { computed } from 'vue'
import { summarizeReversalFees, reversalProfit } from '../ejh-reversal-fees'
const props = defineProps({ records: { type: Array, default: () => [] }, feePercent: { type: Number, default: 6 }, cards: { type: Array, default: () => [] }, operations: { type: Array, default: () => [] }, confirmedOnly: Boolean, hideLabel: Boolean, showProfit: Boolean, consumption: {type:Number,default:null}, consumptionMissing: {type:Number,default:0} })
const fee = computed(() => summarizeReversalFees(props.records, props.feePercent, props.confirmedOnly, props.cards, props.operations))
const result = computed(() => reversalProfit(fee.value, props.consumption, props.consumptionMissing))
</script>

<template>
 <div class="reversal-fee" :class="{'reversal-fee-cell':hideLabel}">
  <span v-if="!hideLabel">授权撤销费</span>
  <div class="reversal-fee-values">
   <strong v-for="row in fee.rows" :key="row.currency">{{row.amount}} {{row.currency}}</strong>
   <strong v-if="!fee.rows.length">{{fee.missing||fee.pending?'待核实':'—'}}</strong>
   <strong v-if="fee.usd.amount!==null && (fee.usd.estimated || fee.rows.some(row=>row.currency!=='USD'))" class="reversal-fee-usd" title="按各卡初始开卡的 USD 结算比例估算">{{fee.usd.estimated?'≈ ':''}}{{fee.usd.amount}} USD{{fee.usd.missing?'（部分）':''}}</strong>
   <strong v-if="showProfit || fee.count || fee.missing || fee.pending" class="reversal-fee-r" title="授权撤销费 USD × 6.7">折合 {{result.estimated?'≈ ':''}}{{result.amount??'—'}} R{{result.incomplete&&result.amount!==null?'（部分）':''}}</strong>
   <small v-if="fee.usd.missing">{{fee.usd.missing}} 笔缺少 USD 折算依据，未计入</small>
   <small v-if="fee.count">按 {{fee.percent}}% 计算</small>
   <small v-if="fee.missing">{{fee.missing}} 笔金额或比例待核实</small>
   <small v-if="fee.pending">{{fee.pending}} 笔待确认，未计入</small>
  </div>
 </div>
 <div v-if="showProfit" class="reversal-profit" title="账号结余＝消费收益 R − 本账号授权撤销费 R；开卡费和充值手续费在总账扣除">
  <span>账号结余</span><div><strong :class="{'negative':result.profit?.startsWith('-')}">{{result.profit!==null&&result.estimated?'≈ ':''}}{{result.profit??'—'}} R</strong><small>未分摊开卡费、充值手续费</small><small v-if="result.profit===null">{{consumptionMissing||consumption===null?'消费未查询完整':'撤销费待核实'}}</small></div>
 </div>
</template>

<style scoped>
.reversal-fee{display:flex;justify-content:space-between;align-items:baseline;gap:12px;margin:9px 0 4px;padding-top:8px;border-top:1px solid var(--line);font-size:11px}.reversal-fee>span{color:var(--muted);flex-shrink:0}.reversal-fee-values{text-align:right;min-width:0;font-variant-numeric:tabular-nums}.reversal-fee strong{display:block;font-size:12px;overflow-wrap:anywhere}.reversal-fee small{display:block;font-size:10px;color:var(--muted);line-height:1.6}.reversal-fee-cell{display:block;border:0;margin:0;padding:0}.reversal-fee-cell .reversal-fee-values{text-align:left}
.reversal-profit{display:flex;align-items:baseline;justify-content:space-between;gap:12px;margin-top:8px;padding-top:8px;border-top:1px solid var(--line);font-size:11px}.reversal-profit>span{color:var(--muted)}.reversal-profit>div{text-align:right;min-width:0}.reversal-profit strong{font-size:12px;color:var(--green-strong);font-variant-numeric:tabular-nums;overflow-wrap:anywhere}.reversal-profit .negative{color:var(--red)}.reversal-profit small{display:block;font-size:10px;color:var(--muted)}.reversal-fee-r{margin-top:3px}
</style>
