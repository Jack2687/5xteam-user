<script setup>
import { computed } from 'vue'
import { money, rmb } from '../nexovcc-finance'
const props=defineProps({totals:{type:Object,required:true}})
defineEmits(['details'])
const incomplete=computed(()=>!props.totals.ready||props.totals.unknownRevenue||props.totals.unmatchedCases||props.totals.unmatched||props.totals.unverified||props.totals.foreign||props.totals.unknownFunds)
const usd=v=>props.totals.ready?money(v):'—'
const r=v=>props.totals.ready?rmb(v):'—'
const fees=computed(()=>[
 {label:'开卡费',value:props.totals.opening,kind:'3003'},
 {label:'授权撤销费',value:props.totals.reversal,kind:'2003'},
 {label:'交易手续费',value:props.totals.transaction,kind:'2007'},
 {label:'其他费用',value:props.totals.otherFees,kind:'card-other'},
 {label:'卡内损耗',value:props.totals.cardLoss},
 {label:'清算成本',value:props.totals.settlement},
])
</script>
<template>
 <div class="card-money">
  <div class="money-row"><span>消费收益</span><button @click="$emit('details',{type:'accounts'})"><strong>{{totals.unknownRevenue?'—':money(totals.revenue)}} R</strong></button></div>
  <div class="money-row"><span>卡片成本</span><button @click="$emit('details',{type:'funds',feesOnly:true})"><strong>{{r(totals.cost)}} R</strong><small>{{usd(totals.cost)}} USD</small></button></div>
  <div class="cost-note"><span>开卡 {{r(totals.opening)}} R</span><span>撤销 {{r(totals.reversal)}} R</span><span>交易及其他 {{r(totals.transaction+totals.otherFees)}} R</span><span>损耗及清算 {{r(totals.cardLoss+totals.settlement)}} R</span></div>
  <div class="money-row profit"><span>卡片结余{{totals.pending?'（暂计）':''}}</span><strong :class="{negative:!incomplete&&totals.profit<0}">{{incomplete?'—':money(totals.profit)}} R</strong></div>
  <small>充值手续费在总账扣除</small>
  <small v-if="incomplete" class="warning">{{!totals.ready?'流水尚未同步':totals.unknownRevenue?'消费尚未查询完整':totals.unmatched||totals.unmatchedCases?'付款与账号待匹配':'部分费用待核实'}}，暂不计算结余</small>
  <details><summary>付款、回款与费用明细</summary><div class="payment-grid"><div><span>付款</span><strong>{{usd(totals.paid)}}</strong></div><div><span>回款</span><strong>{{usd(totals.returned)}}</strong></div><div><span>差额</span><strong>{{usd(totals.paid-totals.returned)}}</strong></div></div><small>USD · 待回款 {{usd(totals.pending)}} USD</small>
   <div v-for="fee in fees" :key="fee.label" class="money-row"><span>{{fee.label}}</span><button @click="$emit('details',fee.kind?{type:'funds',kind:fee.kind}:{type:'trades'})"><strong>{{r(fee.value)}} R</strong><small>{{usd(fee.value)}} USD</small></button></div>
   <button class="more" @click="$emit('details',{type:'trades'})">查看本卡交易流水</button>
  </details>
 </div>
</template>
<style scoped>
.card-money{min-width:225px;font-variant-numeric:tabular-nums}.money-row{display:flex;justify-content:space-between;align-items:baseline;gap:12px;font-size:11px;border-top:1px solid var(--line);padding-top:9px;margin-top:9px}.money-row:first-child{border:0;margin:0;padding:0}.money-row>span{color:var(--muted)}.money-row strong{font-size:12px}.money-row button,.money-row>strong{text-align:right}.card-money small{display:block;font-size:10px;line-height:1.7;color:var(--muted);font-weight:400;margin-top:3px}.cost-note{display:grid;grid-template-columns:1fr 1fr;gap:3px 10px;font-size:10px;color:var(--muted);margin-top:8px;line-height:1.6}.profit strong{font-size:15px;color:var(--green-strong)}.profit .negative{color:var(--red)}.card-money .warning{color:#a47523}button{font:inherit;color:var(--text);cursor:pointer;background:none;border:0;padding:0}button:hover{text-decoration:underline}details{margin-top:12px}summary{cursor:pointer;font-size:10px;color:var(--green-strong)}.payment-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;margin-top:12px}.payment-grid span{display:block;font-size:10px;color:var(--muted);margin-bottom:6px}.payment-grid strong{font-size:12px}.more{color:var(--green-strong);font-size:11px;margin-top:12px}
</style>
