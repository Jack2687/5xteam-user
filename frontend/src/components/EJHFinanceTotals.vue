<script setup>
import { computed } from 'vue'
import { financeOverview } from '../ejh-finance'
const props=defineProps({data:{type:Object,required:true}})
const totals=computed(()=>financeOverview(props.data))
</script>
<template>
 <div class="finance-totals" aria-label="费用与消费汇总">
  <div><span>实际到账总额</span><strong>{{totals.net??'—'}} <em>USD</em></strong><small>充值手续费 {{totals.depositFee??'—'}} USD · 1%</small></div>
  <div><span>授权撤销费总额</span><strong v-for="row in totals.reversal.rows" :key="row.currency">{{row.amount}} <em>{{row.currency}}</em></strong><strong v-if="!totals.reversal.rows.length">{{totals.reversal.missing||totals.reversal.pending?'—':'0.00'}}</strong><small>折合 {{totals.reversal.usd.estimated?'≈ ':''}}{{totals.reversal.usd.amount??(totals.reversal.missing||totals.reversal.pending?'—':'0.00')}} USD · 费率 {{data.reversal_fee_percent??6}}%</small><small class="fee-r" title="授权撤销费 USD × 6.7">折合 {{totals.profit.estimated?'≈ ':''}}{{totals.profit.amount??'—'}} R{{totals.profit.incomplete&&totals.profit.amount!==null?'（部分）':''}}</small><small v-if="totals.reversal.missing||totals.reversal.pending||totals.reversal.usd.missing" class="warning">部分记录待确认或缺少折算依据</small></div>
  <div><span>开卡费总额</span><strong>{{totals.openingFee}} <em>USDT</em></strong><small>{{totals.openingCount}} 张 × 0.50 · 含已销卡</small></div>
  <div><span>消费总额</span><strong>{{totals.consumption.total?.toFixed(4)??'—'}} <em>R</em></strong><small>下游用户消耗 · 账号去重</small><small v-if="totals.consumption.missing" class="warning">{{totals.consumption.missing}} 个账号未查询，仅合计已知数据</small></div>
  <div class="profit"><span>利润总额 <small v-if="totals.profit.estimated">估算</small></span><strong :class="{negative:totals.profit.profit?.startsWith('-')}">{{totals.profit.profit??'—'}} <em>R</em></strong><small>消费 R − 撤销费 USD × 6.7</small><small v-if="totals.profit.profit===null" class="warning">{{totals.consumption.missing||totals.consumption.total===null?'消费未查询完整':'撤销费待核实'}}</small></div>
  <div class="loss"><span>实际损耗 <small v-if="totals.estimated">估算</small></span><strong>{{totals.loss??'—'}} <em>USD</em></strong><small>{{totals.lossReasons.join('；')||'扣除账户余额、卡内余额及在途付款'}}</small><small v-if="totals.stale" class="warning">{{totals.stale}} 张卡使用上次余额</small></div>
 </div>
</template>
<style scoped>
.finance-totals{display:grid;grid-template-columns:repeat(6,minmax(0,1fr));border-top:1px solid var(--line)}.finance-totals>div{padding:20px;min-width:0;border-right:1px solid var(--line)}.finance-totals>div:last-child{border-right:0}.finance-totals span{display:flex;gap:8px;align-items:center;color:var(--muted);font-size:12px}.finance-totals strong{display:block;font-size:23px;font-weight:600;font-variant-numeric:tabular-nums;margin:12px 0 8px;overflow-wrap:anywhere}.finance-totals em{font-size:11px;font-style:normal;color:var(--muted);font-weight:400}.finance-totals small{display:block;color:var(--muted);font-size:10px;line-height:1.7}.finance-totals .loss{background:var(--green-bg)}.loss strong{color:var(--green-strong)}.finance-totals .fee-r{font-size:13px;font-weight:600;color:var(--text-2)}.profit strong{color:var(--green-strong)}.profit .negative{color:var(--red)}.finance-totals .warning{color:var(--red)}@media(max-width:1200px){.finance-totals{grid-template-columns:repeat(3,minmax(0,1fr))}.finance-totals>div:nth-child(n+4){border-top:1px solid var(--line)}}@media(max-width:640px){.finance-totals{grid-template-columns:1fr}.finance-totals>div+div{border-top:1px solid var(--line)}.finance-totals>div{border-right:0}}
</style>
