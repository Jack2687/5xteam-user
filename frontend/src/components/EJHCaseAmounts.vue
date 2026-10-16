<script setup>
import { computed } from 'vue'
import { accountAmounts } from '../ejh-account-amounts'
import { accountConsumption, consumptionText } from '../ejh-consumption'
import { tradeKinds } from '../ejh'
import ActivationCost from './ActivationCost.vue'
import ReturnStatusBadge from './ReturnStatusBadge.vue'
import EJHReversalFee from './EJHReversalFee.vue'
const props=defineProps({row:{type:Object,required:true},cards:{type:Array,default:()=>[]},operations:{type:Array,default:()=>[]},feePercent:{type:Number,default:6}})
const amounts=computed(()=>accountAmounts(props.row))
</script>
<template><div class="account-money"><div class="account-money-grid">
<div><small>付款{{amounts.authorization?'（授权占用）':''}}</small><strong>{{amounts.paid===null?'待核实':amounts.paid+' '+amounts.currency}}</strong><small v-if="amounts.paid!==null&&!amounts.posted">待入账</small></div>
<div><small>已确认回款</small><strong v-for="r in amounts.returned" :key="r.currency">{{r.amount}} {{r.currency}}</strong><strong v-if="!amounts.returned.length">{{amounts.invalid?'待核实':'0.00'+(amounts.currency?' '+amounts.currency:'')}}</strong><small v-if="amounts.pending">{{amounts.pending}} 笔待核实，未计入</small><small v-else-if="!amounts.returned.length&&!amounts.invalid">{{row.state_override?.state==='returned'?'人工标记已回款；尚无已确认金额流水':'暂无已确认回款'}}</small></div>
<div><small>差额（付款 − 回款）</small><strong>{{amounts.difference===null?'—':amounts.difference+' '+amounts.currency}}</strong><small>{{amounts.reason||(amounts.authorization?'按授权占用计算':'按刷卡金额计算')}}</small></div>
<div class="consumption-total" title="Pro 管理中的用户消耗统计，按账号显示"><small>消费收益</small><strong>{{consumptionText(accountConsumption(row))}}</strong></div>
</div><EJHReversalFee show-profit :consumption="accountConsumption(row)" :records="row.returns||[]" :fee-percent="feePercent" :cards="cards" :operations="operations" confirmed-only/><details><summary>查看流水与匹配依据</summary><ActivationCost :row="row" details/><div v-for="t in row.returns||[]" :key="t.id"><span>{{tradeKinds[t.trade.tradeType]}}</span> · <ReturnStatusBadge :state="t.confirmed?'returned':'unverified'"/><p>{{t.confirmed_amount||'金额待核实'}} {{t.confirmed_currency}} · {{t.evidence}}</p></div></details></div></template>
<style scoped>.account-money{min-width:440px;max-width:760px}.account-money-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:16px}.account-money-grid small,.account-money-grid strong{display:block;line-height:1.65}.account-money-grid small{font-size:11px;color:var(--muted)}.account-money-grid strong{font-size:14px;overflow-wrap:anywhere}.account-money details{margin-top:12px}.account-money summary{cursor:pointer;color:var(--green-strong);font-size:12px}.account-money p{font-size:12px}</style>
