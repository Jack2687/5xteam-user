<script setup>
import { computed } from 'vue'
import { financeOverview } from '../ejh-finance'
const props=defineProps({data:{type:Object,required:true}})
const totals=computed(()=>financeOverview(props.data))
const earnings=computed(()=>totals.value.earnings)
</script>
<template>
 <div class="finance-totals" aria-label="收益与成本汇总">
  <div class="finance-main">
   <div><span>消费收益</span><strong>{{earnings.consumptionR??'—'}} <em>R</em></strong><small>下游用户消费 · 扣费前收益</small><small v-if="totals.consumption.missing" class="warning">{{totals.consumption.missing}} 个账号未查询，仅含已知数据</small></div>
   <div><span>总成本 <small v-if="earnings.estimated">估算</small></span><strong>{{earnings.costR??'—'}} <em>R</em></strong><small>开卡费 + 充值手续费 + 授权撤销费</small><small v-if="earnings.costIncomplete" class="warning">已知 {{earnings.knownCostR??'—'}} R · 部分费用待核实</small></div>
   <div class="profit"><span>净利润 <small v-if="earnings.estimated">估算</small></span><strong :class="{negative:earnings.profitR?.startsWith('-')}">{{earnings.profitR??'—'}} <em>R</em></strong><small>消费收益 − 总成本</small><small v-if="earnings.incomplete" class="warning">数据未完整，暂不计算净利润</small></div>
  </div>
  <div class="finance-fees">
   <div><span>开卡费</span><strong>{{earnings.openingR}} <em>R</em></strong><small>{{totals.openingFee}} USDT · {{totals.openingCount}} 张 × 0.50（含已销卡）</small></div>
   <div><span>充值手续费</span><strong>{{earnings.rechargeR??'—'}} <em>R</em></strong><small>{{totals.depositFee??'—'}} USD · 充值总额 × 1%</small></div>
   <div><span>授权撤销费</span><strong>{{earnings.reversalR??'—'}} <em>R</em><small v-if="totals.reversal.missing||totals.reversal.pending||totals.reversal.usd.missing" class="warning">部分待核实</small></strong><small>{{totals.reversal.usd.amount??(totals.reversal.count?'—':'0.00')}} USD · 费率 {{data.reversal_fee_percent??6}}%</small><small>{{totals.reversal.rows.map(r=>r.amount+' '+r.currency).join(' / ')||'暂无已知撤销费用'}}</small></div>
  </div>
  <div class="finance-cash"><span>实际到账 <b>{{totals.netR??'—'}} R</b> <small>（{{totals.net??'—'}} USD）</small></span><span :title="totals.lossReasons.join('；')">实际损耗（资金核对）<b>{{totals.lossR??'—'}} R</b> <small>（{{totals.loss??'—'}} USD）</small></span><small>1 USD = 6.7 R · 开卡 USDT 按 1:1 USD 记账</small><small v-if="totals.lossReasons.length" class="warning">{{totals.lossReasons.join('；')}}</small><small v-if="totals.stale" class="warning">{{totals.stale}} 张卡使用上次余额</small></div>
 </div>
</template>
<style scoped>
.finance-totals{border-top:1px solid var(--line);font-variant-numeric:tabular-nums}.finance-main,.finance-fees{display:grid;grid-template-columns:repeat(3,minmax(0,1fr))}.finance-main>div,.finance-fees>div{padding:18px 20px;min-width:0}.finance-main>div+div,.finance-fees>div+div{border-left:1px solid var(--line)}.finance-totals span{display:flex;gap:8px;align-items:center;color:var(--muted);font-size:12px}.finance-totals strong{display:block;font-size:28px;font-weight:600;margin:10px 0 6px;overflow-wrap:anywhere}.finance-totals em{font-size:11px;font-style:normal;color:var(--muted);font-weight:400}.finance-totals small{display:block;color:var(--muted);font-size:10px;line-height:1.7}.finance-main .profit{background:var(--green-bg)}.profit strong{color:var(--green-strong)}.profit .negative{color:var(--red)}.finance-fees{border-top:1px solid var(--line)}.finance-fees strong{font-size:20px}.finance-cash{display:flex;flex-wrap:wrap;gap:10px 24px;padding:12px 20px;border-top:1px solid var(--line);background:var(--bg)}.finance-cash span{flex-wrap:wrap;font-size:11px;gap:4px}.finance-cash b{color:var(--text)}.finance-totals .warning{color:var(--red)}@media(max-width:640px){.finance-main,.finance-fees{grid-template-columns:1fr}.finance-main>div+div,.finance-fees>div+div{border-left:0;border-top:1px solid var(--line)}}
</style>
