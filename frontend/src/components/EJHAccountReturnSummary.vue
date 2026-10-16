<script setup>
import { computed } from 'vue'
import { accountAmounts } from '../ejh-account-amounts'
import { formatTime } from '../utils'
import EJHCaseState from './EJHCaseState.vue'

const props = defineProps({ row: { type: Object, required: true } })
const amounts = computed(() => accountAmounts(props.row))
</script>

<template>
 <article class="account-return-summary">
  <header><strong>{{row.email}}</strong><EJHCaseState :row="row"/></header>
  <dl class="return-amounts">
   <div><dt>付款<span v-if="amounts.authorization">（授权）</span></dt><dd>{{amounts.paid===null?'待识别':amounts.paid+' '+amounts.currency}}</dd></div>
   <div><dt>回款</dt><dd v-for="r in amounts.returned" :key="r.currency">{{r.amount}} {{r.currency}}</dd><dd v-if="!amounts.returned.length">{{amounts.invalid||amounts.pending?'待核实':'0.00'+(amounts.currency?' '+amounts.currency:'')}}</dd></div>
   <div><dt>差额</dt><dd>{{amounts.difference===null?'—':amounts.difference+' '+amounts.currency}}</dd></div>
  </dl>
  <p v-if="amounts.pending||amounts.invalid" class="return-note">{{amounts.pending?amounts.pending+' 笔回款待核实，未计入金额':'回款金额待核实'}}</p>
  <p v-else-if="amounts.paid!==null&&amounts.reason" class="return-note">{{amounts.reason}}</p>
  <p v-if="row.state_override?.state==='returned'&&!amounts.returned.length" class="return-note">人工标记已回款，尚无已确认金额</p>
  <footer><span>开通 {{row.opened_at?formatTime(row.opened_at):'—'}}</span><span>回款 {{amounts.returnedAt?formatTime(amounts.returnedAt):'—'}}</span></footer>
 </article>
</template>

<style scoped>
.account-return-summary{padding:14px 0;border-bottom:1px solid var(--line);min-width:0}.account-return-summary:last-child{border-bottom:0}.account-return-summary header{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:10px}.account-return-summary header>strong{font-size:13px;overflow-wrap:anywhere;min-width:0}.return-amounts{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;margin:14px 0 10px}.return-amounts dt{color:var(--muted);font-size:11px;line-height:1.7}.return-amounts dd{margin:6px 0 0;font-size:15px;font-weight:650;overflow-wrap:anywhere;line-height:1.5;font-variant-numeric:tabular-nums}.return-note{margin:6px 0;color:var(--muted);font-size:11px}.account-return-summary footer{display:flex;flex-wrap:wrap;gap:5px 20px;color:var(--muted);font-size:11px;line-height:1.7}@media(max-width:480px){.return-amounts{gap:9px}.return-amounts dd{font-size:13px}.account-return-summary footer{flex-direction:column}}
</style>
