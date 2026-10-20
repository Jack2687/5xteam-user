<script setup>
import { computed } from 'vue'
import { caseDetails } from '../nexovcc-case-details'
import { money, rmb, tradeStates } from '../nexovcc-finance'
import { formatTime } from '../utils'
const props=defineProps({row:{type:Object,required:true},finance:{type:Object,default:()=>({})},running:Boolean})
defineEmits(['details','match'])
const value=computed(()=>caseDetails(props.row,props.finance))
const feeRows=computed(()=>[
 {label:'开卡费（分摊）',amount:value.value.opening,kind:'3003',scope:'card'},
 {label:'卡服务费（月 · 分摊）',amount:value.value.monthly,kind:'3004',scope:'card'},
 {label:'授权撤销费',amount:value.value.reversal,kind:'2003'},
 {label:'交易手续费',amount:value.value.transaction,kind:'2007'},
 {label:'退款手续费',amount:value.value.refundFee,kind:'8888'},
 {label:'其他费用',amount:value.value.otherFees,kind:'case-other'},
 {label:'卡内损耗',amount:value.value.cardLoss},
 {label:'清算成本',amount:value.value.settlement},
])
const at=v=>v&&!v.startsWith('0001')&&Number.isFinite(Date.parse(v))?formatTime(v):'—'
</script>
<template>
 <div class="account-money">
  <div class="amount-grid">
   <div><span>付款{{value.provisional?'（授权占用）':''}}</span><strong>{{money(value.paid)}} USD</strong><small v-for="m in value.merchantPaid.filter(m=>m.currency!=='USD')" :key="m.currency">{{money(m.amount)}} {{m.currency}}</small><small>{{rmb(value.paid)}} R</small></div>
   <div><span>已确认回款</span><strong>{{money(value.returned)}} USD</strong><small v-for="m in value.merchantReturned.filter(m=>m.currency!=='USD')" :key="m.currency">{{money(m.amount)}} {{m.currency}}</small><small>{{rmb(value.returned)}} R</small></div>
   <div><span>差额（付款 − 回款）</span><strong>{{money(value.difference)}} USD</strong><small>{{rmb(value.difference)}} R</small><small>{{!row.match?'待匹配流水':value.closed?'已结算差额':'待回款，不计成本'}}</small></div>
   <div><span>消费收益</span><strong>{{money(value.revenue)}} R</strong><small>{{value.revenue===null?'消费尚未同步':'Sub2 实际消费'}}</small></div>
  </div>
  <p v-if="['opened','waiting'].includes(row.state)||value.pending>0" class="return-note">尚待回款：<b>{{money(value.pending)}} USD</b>（已扣清算）</p>
  <p v-if="value.manualUnconfirmed" class="warning">人工标记已回款；流水尚未确认全额回款，金额仍按实际记录显示。</p>
  <p v-if="row.match&&!row.match.group.valid" class="warning">{{row.match.group.problem||'流水金额待核实'}}</p>
  <div class="fee-grid"><div v-for="item in feeRows" :key="item.label" class="fee-line"><span>{{item.label}}</span><button :disabled="item.scope==='card'?!row.card_id:!row.match" @click="$emit('details',{row,options:item.kind?{type:'funds',kind:item.kind,scope:item.scope}:{type:'trades'}})"><strong>{{money(item.amount)}} USD</strong><small>{{rmb(item.amount)}} R</small></button></div></div>
  <div class="result-row"><div><span>账号成本{{value.provisional?'（当前）':''}}</span><strong>{{rmb(value.cost)}} R<small>{{money(value.cost)}} USD</small></strong></div><div class="profit"><span>账号结余{{value.provisional?'（暂计）':''}}</span><strong :class="{negative:value.profit<0}">{{money(value.profit)}} R</strong></div></div>
  <small v-if="value.sharedCount" class="note">开卡费、月服务费按本卡已开通 {{value.sharedCount}} 个账号分摊，保留两位小数；尾差保留在总账。<template v-if="value.sharedApplied===false">本账号分摊已计入首次成功开通。</template></small>
  <small class="note">{{value.provisional?'回款完成后更新最终结余；':''}}充值手续费在总账扣除。1 USD = 6.7 R。</small>
  <small v-if="row.revenue_note" class="note">{{row.revenue_note}}</small>
  <details><summary>查看流水与匹配依据 <span>{{row.match?(row.match.method==='auto'?'自动匹配':'人工关联'):'待匹配'}}</span></summary>
   <div class="evidence"><p>授权码：{{row.match?.group?.authorization||'—'}}<br>匹配依据：{{row.match?.evidence||'尚无匹配记录，可核实后关联同卡流水。'}}</p><p v-if="row.manual_state">人工状态依据：{{row.reason||'—'}}</p><p v-if="row.match&&!value.feesReady" class="warning">费用明细待同步核实，未将缺失金额当成零。</p>
    <div v-for="trade in value.trades" :key="trade.id" class="trade-line"><span>{{tradeStates[trade.status]||trade.status}} · {{at(trade.at)}}<small>{{trade.id}}</small></span><strong>{{trade.amount}} {{trade.currency}}<small>{{trade.merchant_amount}} {{trade.merchant_currency}}</small></strong></div>
    <div class="links"><button @click="$emit('details',{row,options:{type:'trades'}})">交易明细</button><button :disabled="!row.match" @click="$emit('details',{row,options:{type:'funds'}})">费用明细</button><button :disabled="running||row.payment_status!=='success'" @click="$emit('match',row)">关联流水</button></div>
   </div>
  </details>
 </div>
</template>
<style scoped>
.account-money{min-width:520px;max-width:850px;font-variant-numeric:tabular-nums}.amount-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:16px}.amount-grid span{font-size:11px;color:var(--muted);display:block;margin-bottom:7px}.amount-grid strong{font-size:14px;display:block}.account-money small{display:block;font-size:10px;line-height:1.7;color:var(--muted);font-weight:400}.return-note,.warning{font-size:11px;line-height:1.7;margin:10px 0}.warning{color:#a47523}.fee-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:0 24px;margin-top:12px}.fee-line{display:flex;align-items:baseline;justify-content:space-between;gap:12px;padding:8px 0;border-top:1px solid var(--line);font-size:11px}.fee-line>span{color:var(--muted)}.fee-line button{text-align:right;color:var(--text)}.result-row{display:grid;grid-template-columns:1fr 1fr;gap:24px;border-top:1px solid var(--line);padding-top:12px;margin-top:4px}.result-row>div{display:flex;justify-content:space-between;align-items:baseline;gap:12px;font-size:12px}.result-row strong{text-align:right}.profit strong{font-size:17px;color:var(--green-strong)}.profit .negative{color:var(--red)}.note{margin-top:6px}button{font:inherit;border:0;background:none;padding:0;color:var(--green-strong);cursor:pointer}button:disabled{cursor:default;opacity:.6}button:not(:disabled):hover{text-decoration:underline}details{margin-top:12px}summary{font-size:11px;cursor:pointer;color:var(--green-strong);width:fit-content}summary>span{color:var(--muted);margin-left:10px}.evidence{background:var(--bg);border-radius:7px;margin-top:10px;padding:12px;font-size:11px;line-height:1.7;overflow-wrap:anywhere}.evidence p{margin:0 0 10px}.trade-line{display:flex;justify-content:space-between;gap:12px;border-top:1px solid var(--line);padding:8px 0}.trade-line strong{text-align:right}.links{display:flex;gap:18px;margin-top:12px}@media(max-width:600px){.account-money{min-width:460px}.amount-grid{gap:10px}.fee-grid,.result-row{gap:0 14px}}
</style>
