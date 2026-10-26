<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { reconciliation } from '../nexovcc-reconciliation'
import { money as formatMoney, rmb as formatRMB } from '../nexovcc-finance'
const props=defineProps({data:{type:Object,required:true},refreshSeconds:{type:Number,default:30},syncing:Boolean,masked:Boolean})
defineEmits(['details'])
const money=value=>props.masked?'---':formatMoney(value)
const rmb=value=>props.masked?'---':formatRMB(value)
const now=ref(Date.now())
let timer
onMounted(()=>{timer=setInterval(()=>now.value=Date.now(),10000)})
onBeforeUnmount(()=>clearInterval(timer))
const report=computed(()=>reconciliation(props.data,now.value,props.refreshSeconds*2))
</script>
<template>
 <section class="reconcile-bar" :class="masked?'unknown':syncing?'stale':report.state" aria-label="资金核对">
  <div class="reconcile-status"><i></i><div><strong>资金核对 · {{masked?'已关闭':syncing?'同步中':report.label}}</strong><small>{{syncing?'正在更新余额与流水，完成后核对':'按最近同步数据 · 含估算 · ≤ 1 USD 为小额差异'}}</small></div></div>
  <div class="reconcile-values">
   <div><span>已付款在途</span><b><button @click="$emit('details',{type:'trades'})">{{money(report.pendingAssets)}}</button> <em>USD</em></b><small>{{rmb(report.pendingAssets)}} R · 已计入核对</small></div>
   <div><span>账面差额</span><b>{{masked?'---':syncing?'—':money(report.gap)}} <em>USD</em></b><small>{{masked?'---':syncing?'—':rmb(report.gap)}} R</small></div>
   <div><span>其中销卡待核实</span><b>{{masked?'---':report.gap===null?'—':money(report.closurePending)}} <em>USD</em></b><small>{{masked?'---':report.gap===null?'—':rmb(report.closurePending)}} R · 不计成本</small></div>
   <div><span>其余待核对</span><b>{{masked?'---':syncing?'—':money(report.unexplained)}} <em>USD</em></b><small>{{masked?'---':syncing?'—':rmb(report.unexplained)}} R · 未认定为损失</small></div>
  </div>
  <p v-if="!masked&&report.gap!==null&&!syncing" class="formula">充值 <button @click="$emit('details',{type:'recharges'})">{{money(report.totals.gross)}}</button> − 余额及在途 <b>{{money(report.assets)}}</b> − 总成本 <button @click="$emit('details',{type:'funds',feesOnly:true})">{{money(report.totals.cost)}}</button><template v-if="report.externalWithdrawn"> − 账户提现 <button @click="$emit('details',{type:'funds',kind:'1001'})">{{money(report.externalWithdrawn)}}</button></template><template v-if="report.totals.paymentOnly.amount"> − 只付款支出 <b>{{money(report.totals.paymentOnly.amount)}}</b></template> ＝ 差额 <b>{{masked?'---':syncing?'—':money(report.gap)}}</b> USD</p>
  <p v-if="!masked&&report.reasons.length" class="notice">{{report.reasons.join('；')}}</p>
  <details><summary>核对明细</summary><div class="breakdown"><span>账户余额 <b>{{money(report.account)}} USD</b></span><span>正常及销卡处理中余额 <b>{{money(report.cardBalance)}} USD</b></span><span>已付款在途 <button @click="$emit('details',{type:'trades'})">{{money(report.pendingAssets)}} USD</button></span><span>已转出账户资金 <button @click="$emit('details',{type:'funds',kind:'1001'})">{{money(report.externalWithdrawn)}} USD</button></span></div><p v-if="!masked&&report.closedAuthorization">销卡后待结算授权 {{money(report.closedAuthorization)}} USD · 余款已转回主账户，不再重复计入在途；原付款仍待退款或结算。</p><p v-if="!masked&&report.totals.balanceRecoveredPending">余额先恢复、退款流水待确认 {{money(report.totals.balanceRecoveredPending)}} USD · 本次暂估已从在途扣除，避免与卡余额重复计算；账号回款状态保持不变。</p><p>冻结金额单独展示，不重复加到余额；开卡入金、充值到卡、卡片提现均为资金划转。销卡待核实金额不再重复计入卡余额。卡片余额差额单列暂估成本；这里剩余的账面差额仅用于查账，不重复计入成本。</p></details>
 </section>
</template>
<style scoped>
.reconcile-bar{padding:18px 22px;border:1px solid #e8c589;border-radius:12px;background:#fff9eb;color:#795416;display:flex;align-items:center;justify-content:space-between;gap:14px 24px;font-variant-numeric:tabular-nums;flex-wrap:wrap}.reconcile-status{display:flex;align-items:center;gap:10px}.reconcile-status i{width:8px;height:8px;border-radius:50%;background:currentColor}.reconcile-status strong{font-size:14px}.reconcile-bar small{display:block;font-size:11px;line-height:1.8;opacity:.85}.reconcile-values{display:flex;gap:24px;flex-wrap:wrap}.reconcile-values span{display:block;font-size:11px}.reconcile-values b{display:block;font-size:23px;margin:6px 0}.reconcile-values em{font-size:11px;font-style:normal;font-weight:400}.reconcile-bar p{margin:0;font-size:12px;line-height:1.8;flex-basis:100%}.formula{border-top:1px solid #e8c58966;padding-top:12px}.reconcile-bar button{font:inherit;color:inherit;border:0;background:none;text-decoration:underline;text-underline-offset:3px;padding:0;cursor:pointer}.balanced{color:var(--green-strong);background:var(--green-soft);border-color:var(--line)}.unknown,.stale{color:var(--muted);background:var(--surface);border-color:var(--line)}details{width:100%;font-size:11px}summary{width:fit-content;cursor:pointer}.breakdown{display:flex;gap:14px 24px;flex-wrap:wrap;margin:14px 0}.breakdown b{margin-left:8px}details p{font-size:11px}@media(max-width:700px){.reconcile-values{width:100%;justify-content:space-between;gap:10px}.reconcile-values b{font-size:18px}.reconcile-bar{padding:15px}.reconcile-values small{font-size:10px}}
</style>
