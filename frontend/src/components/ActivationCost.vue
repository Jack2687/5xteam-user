<script setup>
import { computed } from 'vue'
import { tradeKinds } from '../ejh'
import { formatTime } from '../utils'
import { supplierCredits } from '../gptpay'
const props=defineProps({row:{type:Object,required:true},details:Boolean})
const current=computed(()=>props.row)
const credits=value=>supplierCredits(value,current.value.credits?.unit==='cent_R'?'xupay':'tokenseek')
const charge=computed(()=>current.value.charge)
const amount=(value,currency)=>value!==''&&value!=null&&currency ? value+' '+currency : '未提供'
const headline=computed(()=>!charge.value ? (current.value.card_type==='ejh'?'卡片开通金额：等待自动识别':'卡片开通金额：暂无卡台流水') : (charge.value.type==='1'?'授权占用：':'清算刷卡：')+amount(charge.value.card_amount,charge.value.card_currency))
</script>
<template><div class="activation-cost"><strong>{{headline}}</strong><small v-if="charge">{{tradeKinds[charge.type]}} · {{charge.status==='2'?'已入账':'待入账 / 未知'}}<span v-if="charge.type==='1'">（授权金额不代表最终清算）</span></small><template v-if="details"><p v-if="charge">交易金额 {{amount(charge.trade_amount,charge.trade_currency)}} · 结算金额 {{amount(charge.bill_amount,charge.bill_currency)}}<br>流水 {{charge.trade_no}} · {{charge.merchant||'商户未提供'}}<br>交易 {{charge.trade_time||'—'}} · 更新 {{formatTime(charge.synced_at)}}<br>关联依据：{{current.charge_evidence||'已有回款关联的原交易'}}</p><p v-else>后台读取交易后，自动识别同卡授权或清算流水，按开通时间关联账号。</p><p v-if="current.credits">供应商服务费：已扣 {{credits(current.credits.charged)}} · 预留 {{credits(current.credits.reserved)}} · 已释放 {{credits(current.credits.released)}}<br>供应商结算状态 {{current.credits.settlement||'未提供'}} · 更新 {{formatTime(current.credits.updated_at)}}</p><p>刷卡、交易、结算金额各为独立币种口径；授权与清算不相加。退款单独列出，服务费不计入卡余额。</p></template></div></template>
<style scoped>.activation-cost{font-size:12px;line-height:1.7;overflow-wrap:anywhere}.activation-cost strong,.activation-cost small{display:block}.activation-cost small,.activation-cost p{color:var(--muted)}.activation-cost p{margin:8px 0}</style>
