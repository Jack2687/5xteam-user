<script setup>
import { computed } from 'vue'
import { cardAmounts } from '../ejh-account-amounts'
import { cardConsumption } from '../ejh-consumption'
import EJHReversalFee from './EJHReversalFee.vue'
const props=defineProps({card:{type:Object,required:true},trades:{type:Array,default:()=>[]},cases:{type:Array,default:()=>[]},operations:{type:Array,default:()=>[]},feePercent:{type:Number,default:6}})
const amounts=computed(()=>cardAmounts(props.card,props.trades))
const consumption=computed(()=>cardConsumption(props.cases))
const reversalTrades=computed(()=>props.trades.filter(t=>t.card_id===props.card.id))
</script>
<template><div class="card-money"><div class="money-head"><span>付款</span><span>回款</span><span>差额</span></div><div v-for="r in amounts.rows" :key="r.currency" class="money-row"><strong>{{r.paid}}<small>{{r.currency}}{{r.authorization?' · 含授权':''}}</small></strong><strong>{{r.returned}}<small>{{r.currency}}</small></strong><strong>{{r.difference??'—'}}<small>{{r.currency}}</small></strong></div><p v-if="!amounts.rows.length">暂无可识别交易</p><div class="money-fee consumption-total" title="该卡关联账号的用户消耗统计合计，同一账号只计算一次"><span>消费总额</span><strong>{{consumption.text}}</strong></div><small v-if="consumption.missing">{{consumption.missing}} 个账号未查询，合计仅含已知数据</small><div class="money-fee"><span>开卡费</span><strong>{{amounts.fee}}</strong></div><EJHReversalFee show-profit :consumption="consumption.total" :consumption-missing="consumption.missing" :records="reversalTrades" :fee-percent="feePercent" :cards="[card]" :operations="operations"/><small v-if="amounts.invalid">部分金额或币种缺失，差额暂不可计算</small><small v-if="amounts.pending">{{amounts.pending}} 笔回款待入账，未计入</small><small>差额＝付款 − 回款；开卡费单列，销卡退余额不计入。</small></div></template>
<style scoped>.card-money{margin-top:10px;min-width:210px;font-variant-numeric:tabular-nums}.money-head,.money-row{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px}.money-head{font-size:11px;color:var(--muted);margin-bottom:5px}.money-row{margin:5px 0}.money-row strong{font-size:12px;overflow-wrap:anywhere}.card-money small{display:block;font-size:10px;line-height:1.6;color:var(--muted);font-weight:400}.card-money p{font-size:11px;color:var(--muted)}.money-fee{display:flex;justify-content:space-between;font-size:11px;margin:8px 0 4px;padding-top:7px;border-top:1px solid var(--line)}</style>
