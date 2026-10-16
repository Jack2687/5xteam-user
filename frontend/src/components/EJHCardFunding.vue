<script setup>
import { computed } from 'vue'
import { cardFundingSummary } from '../ejh-card-funding'
const props = defineProps({ card: { type: Object, required: true }, operations: { type: Array, default: () => [] }, trades: { type: Array, default: () => [] } })
const summary = computed(() => cardFundingSummary(props.card, props.operations, props.trades))
const rows = computed(() => {
  const { funded, closure, difference } = summary.value
  const unsettled = closure.pending || closure.unknown
  return [
    { key: 'funded', label: '累计额度', cells: [
      { amounts: funded.amounts, partial: !!funded.missing },
      { amounts: funded.missingSettlements || funded.settlement === '未记录' ? [] : [funded.settlement] },
    ] },
    { key: 'closure', label: '销卡退回', cells: [
      { amounts: closure.transaction, partial: !!(unsettled || closure.missingTransaction) },
      { amounts: closure.settlement, partial: !!(unsettled || closure.missingSettlement) },
    ] },
    { key: 'difference', label: '差额', cells: [difference.transaction, difference.settlement] },
  ]
})
const notes = computed(() => {
  const { funded, closure, difference } = summary.value
  const result = []
  if (funded.missing) result.push('初始或追加的交易金额有缺失，累计额度仅含已知部分。')
  if (funded.missingSettlements) result.push(`${funded.missingSettlements} 笔成功追加未记录结算金额，累计结算金额暂不可计算。`)
  else if (funded.settlement === '未记录') result.push('初始开卡的结算金额未记录。')
  if (closure.pending) result.push(`${closure.pending} 笔销卡退回待入账，尚未计入。`)
  if (closure.unknown) result.push(`${closure.unknown} 笔销卡退回状态待核实，尚未计入。`)
  if (closure.missingTransaction || closure.missingSettlement) result.push('销卡退回有金额缺失，合计仅含已知部分。')
  if (difference.transaction.reason && difference.transaction.reason === difference.settlement.reason) result.push(difference.transaction.reason + '。')
  else {
    if (difference.transaction.reason) result.push('交易差额：' + difference.transaction.reason + '。')
    if (difference.settlement.reason) result.push('结算差额：' + difference.settlement.reason + '。')
  }
  return result
})
</script>

<template>
 <div class="card-funding">
  <div class="funding-table" role="table" aria-label="卡片额度、销卡退回与差额">
   <div class="funding-row funding-head" role="row"><span role="columnheader"></span><span role="columnheader">交易金额</span><span role="columnheader">结算金额</span></div>
   <div v-for="row in rows" :key="row.key" class="funding-row" :class="{'funding-difference':row.key==='difference'}" role="row">
    <span class="funding-label" role="rowheader" :title="row.key==='difference'?'累计额度 − 销卡退回':''">{{row.label}}</span>
    <div v-for="(cell,index) in row.cells" :key="index" class="funding-value" role="cell" :title="cell.reason || (cell.partial?'仅含已知的已入账金额，详见明细':'')">
     <strong v-for="amount in cell.amounts" :key="amount">{{amount}}<sup v-if="cell.partial" aria-label="仅含部分金额">*</sup></strong>
     <span v-if="!cell.amounts.length" class="funding-empty">—</span>
    </div>
   </div>
  </div>
  <details class="funding-details">
   <summary>明细<span v-if="summary.funded.increases"> · 追加 {{summary.funded.increases}} 次</span></summary>
   <div class="funding-detail-body">
    <p>初始额度：{{summary.opening.amount}}<br>初始结算：{{summary.opening.settlement}}</p>
    <p>累计额度含初始额度及成功追加；差额 = 累计额度 − 已入账销卡退回，按同币种计算。</p>
    <p v-for="note in notes" :key="note">{{note}}</p>
   </div>
  </details>
 </div>
</template>

<style scoped>
.card-funding{min-width:236px;font-variant-numeric:tabular-nums;font-size:11px;line-height:1.5}.funding-row{display:grid;grid-template-columns:54px minmax(0,1fr) minmax(0,1fr);gap:6px;align-items:baseline;padding:4px 0}.funding-head{padding-top:0;font-size:10px;color:var(--muted);text-align:right}.funding-label{color:var(--muted)}.funding-value{text-align:right;min-width:0}.funding-value strong{display:block;font-size:11px;line-height:1.5;overflow-wrap:anywhere;font-weight:600}.funding-value sup{color:var(--muted);margin-left:2px}.funding-empty{color:var(--muted)}.funding-difference{margin-top:3px;padding-top:7px;border-top:1px solid var(--line)}.funding-difference .funding-label,.funding-difference strong{color:var(--green-strong);font-weight:600}.funding-details{margin-top:7px;color:var(--muted);font-size:10px}.funding-details summary{cursor:pointer;width:fit-content}.funding-details summary:focus-visible{outline:2px solid var(--green);outline-offset:3px;border-radius:2px}.funding-detail-body{max-width:320px;white-space:normal;overflow-wrap:anywhere}.funding-detail-body p{margin:7px 0 0;font-size:10px;line-height:1.6}
</style>
