<script setup>
import PaymentOnlySummary from './PaymentOnlySummary.vue'
import { computed } from 'vue'
import { financeTotals, money, rmb } from '../nexovcc-finance'
const props=defineProps({data:{type:Object,required:true},serial:{type:String,default:''},masked:Boolean,collapsible:Boolean})
defineEmits(['details'])
const t=computed(()=>financeTotals(props.data,props.serial))
const partial=computed(()=>t.value.unknownRevenue||t.value.unmatchedCases||t.value.unmatched||t.value.unverified||t.value.staleReviews||t.value.foreign||t.value.unknownFunds)
const usd=v=>props.masked?'---':t.value.ready?money(v):'—'
const r=v=>props.masked?'---':t.value.ready?rmb(v):'—'
</script>
<template>
 <section class="finance-summary">
  <div class="profit-row" :class="{'with-payment':!serial}"><button @click="$emit('details',{type:'accounts'})"><span>消费收益</span><strong>{{masked?'---':money(t.revenue)}} <em>R</em></strong><small>{{!masked&&t.unknownRevenue?`${t.unknownRevenue} 个账号待同步收益`:'Sub2 实际消费'}}</small></button>
   <button @click="$emit('details',{type:'funds',feesOnly:true})"><span>{{serial?'本卡成本':'总成本'}}{{t.balanceDifference?'（含暂估）':''}}</span><strong>{{r(t.cost)}} <em>R</em></strong><small>{{usd(t.cost)}} USD{{serial?' · 不含账户充值费':''}}</small></button>
   <button class="profit" :class="{negative:t.profit<0}" @click="$emit('details',{type:'accounts'})"><span>{{partial?'已知收益净额':t.balanceDifference||t.balanceRecoveredPending?'净利润（暂计）':'净利润'}}</span><strong>{{masked?'---':t.ready?money(t.profit):'—'}} <em>R</em></strong><small>收益 − 成本 · 1 USD = 6.7 R</small><small v-if="t.paymentOnly.missing">只付款金额待核实，暂不计算净利润</small></button>
   <PaymentOnlySummary v-if="!serial" compact issuer="nexovcc" :data="data" :masked="masked"/>
  </div>
  <details class="cost-details" :open="!collapsible"><summary><span>费用与收支明细</span><small>API 收费、开卡费、手续费、付款与回款</small><span class="expand-label"></span></summary>
  <div class="cost-row"><button @click="$emit('details',{type:'funds',kind:'api-merchant'})"><span>API 收费</span><b>{{usd(t.merchantFee)}} USD</b><small>{{r(t.merchantFee)}} R · 外部商户最终成功后记账</small></button>
   <button v-if="!serial" @click="$emit('details',{type:'recharges'})"><span>成功充值 / 实际到账</span><b>{{usd(t.gross)}} / {{usd(t.received)}} USD</b><small>充值费 {{usd(t.rechargeFee)}} USD / {{r(t.rechargeFee)}} R · 1%</small></button>
   <button @click="$emit('details',{type:'funds',kind:'3003'})"><span>开卡费</span><b>{{usd(t.opening)}} USD</b><small>{{r(t.opening)}} R</small></button>
   <button @click="$emit('details',{type:'funds',kind:'card-service'})"><span>卡服务费</span><b>{{usd(t.service)}} USD</b><small>{{r(t.service)}} R</small></button>
   <button @click="$emit('details',{type:'funds',kind:'2003'})"><span>授权撤销费</span><b>{{usd(t.reversal)}} USD</b><small>{{r(t.reversal)}} R</small></button>
   <button @click="$emit('details',{type:'funds',kind:'2007'})"><span>交易手续费</span><b>{{usd(t.transaction)}} USD</b><small>{{r(t.transaction)}} R</small></button><button @click="$emit('details',{type:'funds',kind:'card-other'})"><span>其他费用</span><b>{{usd(t.otherFees)}} USD</b><small>{{r(t.otherFees)}} R · 含人工核实费用</small></button>
   <button @click="$emit('details',{type:'trades'})"><span>卡内损耗 / 清算支出</span><b>{{usd(t.cardLoss)}} / {{usd(t.settlement)}} USD</b><small>{{r(t.cardLoss+t.settlement)}} R · 原始已结算；成本已扣只付款本金 {{usd(t.paymentOnly.costExcluded)}} USD</small></button>
   <button v-if="t.balanceDifference" @click="$emit('details',{type:'funds',kind:'balance-difference'})"><span>余额差额（暂估）</span><b>{{usd(t.balanceDifference)}} USD</b><small>{{r(t.balanceDifference)}} R · 已计成本，流水补齐不重复计费</small></button>
   <button @click="$emit('details',{type:'trades'})"><span>付款 / 回款</span><b>{{usd(t.paid)}} / {{usd(t.returned)}} USD</b><small>待回款 {{usd(t.pending)}} USD · 不计成本</small></button>
  </div>
  </details>
  <p v-if="!masked&&(!t.ready||t.unmatchedCases||t.unmatched||t.unverified||t.staleReviews||t.foreign||t.unknownFunds)" class="finance-note"><template v-if="!t.ready">等待同步财务流水。</template><template v-else>已读取的费用已计入成本。<template v-if="t.unmatchedCases"> {{t.unmatchedCases}} 个账号待匹配流水。</template><template v-if="t.unmatched"> {{t.unmatched}} 笔付款待关联账号。</template><button v-if="t.unverified" class="review-link" @click="$emit('details',{type:'reviews'})">{{t.unverified}} 组交易待核实 · 点击处理</button><button v-if="t.staleReviews" class="review-link" @click="$emit('details',{type:'reviews'})">{{t.staleReviews}} 条核实记录需重新检查</button><template v-if="t.foreign"> {{t.foreign}} 笔非 USD 流水未折算。</template><template v-if="t.unknownFunds"> {{t.unknownFunds}} 笔未知资金类型待核实。</template></template></p>
 </section>
</template>
<style scoped>
.finance-summary{border:1px solid var(--line);border-radius:14px;background:var(--surface);overflow:hidden}.profit-row{display:grid;grid-template-columns:repeat(3,minmax(0,1fr))}.profit-row button{padding:20px 24px;border-right:1px solid var(--line)}button{display:grid;gap:9px;text-align:left;border:0;background:none;color:var(--text);cursor:pointer;min-width:0}button:hover{background:var(--bg)}button span{color:var(--muted);font-size:12px}strong{font-size:28px;letter-spacing:-.6px;font-variant-numeric:tabular-nums}em{font-size:12px;font-style:normal;font-weight:400}small{font-size:11px;line-height:1.6;color:var(--muted)}.profit{background:var(--green-soft);color:var(--green-strong)}.profit.negative{color:var(--red)}.cost-row{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));border-top:1px solid var(--line)}.cost-row button{padding:15px 24px;border-bottom:1px solid var(--line)}b{font-size:14px;font-variant-numeric:tabular-nums}.review-link{display:inline;padding:0;text-decoration:underline;color:inherit;font-size:inherit}.finance-note{font-size:11px;margin:10px 24px;line-height:1.6;color:#b7791f}@media(max-width:700px){.profit-row button{padding:16px 12px}strong{font-size:22px}.cost-row{grid-template-columns:repeat(2,minmax(0,1fr))}.cost-row button{padding:12px}}
</style>

<style scoped>
.profit-row.with-payment{grid-template-columns:repeat(4,minmax(0,1fr))}.profit-row button{align-content:start;padding:18px 22px}.profit-row strong{font-size:24px;overflow-wrap:anywhere}.cost-details>summary{display:flex;align-items:center;gap:12px;padding:12px 22px;border-top:1px solid var(--line);cursor:pointer;font-size:12px;color:var(--text);list-style:none}.cost-details>summary::-webkit-details-marker{display:none}.expand-label{margin-left:auto;white-space:nowrap;color:var(--green-strong)}.expand-label::after{content:'展开 ▾'}.cost-details[open]>summary .expand-label::after{content:'收起 ▴'}.cost-row>button{align-content:start}.cost-details>summary:hover{background:var(--bg)}@media(max-width:700px){.profit-row.with-payment{grid-template-columns:repeat(2,minmax(0,1fr))}.profit-row.with-payment>:nth-child(n+3){border-top:1px solid var(--line)}.profit-row button{padding:16px 12px}.profit-row strong{font-size:22px}.cost-details>summary{padding:12px;flex-wrap:wrap}.cost-details>summary>small{display:none}}
</style>
