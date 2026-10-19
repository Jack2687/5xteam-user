<script setup>
import { computed, ref, watch } from 'vue'
import { X } from 'lucide-vue-next'
import { formatTime } from '../utils'
import { fundNames,tradeStates,feeKinds,inDateRange } from '../nexovcc-finance'
import Pagination from './Pagination.vue'
const props=defineProps({data:{type:Object,required:true},options:{type:Object,required:true},pageSize:{type:Number,default:10}})
defineEmits(['close'])
const type=ref(props.options.type||'trades'),kind=ref(props.options.kind||''),state=ref(''),query=ref(''),from=ref(''),to=ref(''),page=ref(1),size=ref(props.pageSize),feesOnly=ref(!!props.options.feesOnly)
const names={trades:'卡片交易',funds:'资金明细',recharges:'充值记录',days:'平台日汇总'}
const filtered=computed(()=>{
 let rows=props.data.finance?.[type.value]||[]
 if(props.options.serial)rows=rows.filter(r=>r.serial_no===props.options.serial)
 if(type.value==='trades'&&props.options.group)rows=rows.filter(r=>props.options.group.trade_ids.includes(r.id))
 if(type.value==='funds'&&props.options.group)rows=rows.filter(r=>props.options.group.trade_ids.includes(r.link))
 if(type.value==='funds'&&kind.value){const kinds={funding:[3001,4001],'card-transfers':[3001,4001,4002,3002],'case-other':[2008,2009,4005,4006,5009,5010],'card-other':[2008,2009,4005,4006,8888,5003,5009,5010],8888:[8888,5003],3003:[3003,5001],2003:[2003,5004],2007:[2007,5008]}[kind.value]||[Number(kind.value)];rows=rows.filter(r=>kinds.includes(r.kind))}
 if(type.value==='funds'&&feesOnly.value)rows=rows.filter(r=>feeKinds.includes(r.kind))
 if(state.value)rows=rows.filter(r=>r.status===state.value)
 const q=query.value.trim().toLowerCase()
 return rows.filter(r=>inDateRange(r.at||r.day,from.value,to.value)&&(!q||[r.id,r.serial_no,r.last4,r.merchant,r.authorization,r.link].some(v=>String(v||'').toLowerCase().includes(q)))).slice().sort((a,b)=>new Date(b.at||b.day)-new Date(a.at||a.day))
})
const visible=computed(()=>filtered.value.slice((page.value-1)*size.value,page.value*size.value))
const at=v=>v?formatTime(v):'—'
watch([type,kind,state,query,from,to,feesOnly],()=>page.value=1)
watch(type,()=>{kind.value='';state.value='';feesOnly.value=false})
watch(()=>props.pageSize,v=>{size.value=v;page.value=1})
</script>
<template>
 <Teleport to="body"><div class="modal-backdrop" @click.self="$emit('close')"><section class="modal ledger" role="dialog" aria-modal="true" aria-label="环球交易与资金明细">
  <div class="modal-heading"><h2>{{options.serial?'本卡':'环球'}}交易与资金明细</h2><button class="icon-button" aria-label="关闭明细" @click="$emit('close')"><X :size="18"/></button></div>
  <nav><button v-for="(name,key) in names" v-show="!options.serial||!['recharges','days'].includes(key)" :key="key" :class="{selected:type===key}" @click="type=key">{{name}}</button></nav>
  <p v-if="options.group" class="note">授权 {{options.group.authorization}} · 付款 {{options.group.payment}} USD · 回款 {{options.group.returned}} USD · 差额 {{options.group.difference}} USD</p>
  <div class="filters"><input v-model="query" type="search" placeholder="尾号、流水号、商户、授权码"/><input v-model="from" type="date" aria-label="开始日期"/><span>至</span><input v-model="to" type="date" aria-label="结束日期"/><select v-if="type==='funds'" v-model="kind" aria-label="资金类型"><option value="">全部类型</option><option value="case-other">账号其他费用</option><option value="card-other">卡片其他费用</option><option value="funding">累计额度（开卡＋追加）</option><option value="card-transfers">卡片划转（入金／提现／销卡退回）</option><option v-for="(name,k) in fundNames" :key="k" :value="k">{{name}}</option></select><label v-if="type==='funds'"><input v-model="feesOnly" type="checkbox"/>只看费用</label><select v-if="type==='trades'" v-model="state" aria-label="交易状态"><option value="">全部状态</option><option v-for="(name,k) in tradeStates" :key="k" :value="k">{{name}}</option></select><select v-if="type==='recharges'" v-model="state" aria-label="充值状态"><option value="">全部状态</option><option value="1">成功</option><option value="5">已作废</option></select></div>
  <div class="table-wrap"><table v-if="type==='trades'"><thead><tr><th>交易时间 / 编号</th><th>卡片 / 商户</th><th>交易状态</th><th>账单金额</th><th>商户金额</th><th>授权码</th></tr></thead><tbody><tr v-for="r in visible" :key="r.id"><td>{{at(r.at)}}<small>{{r.id}}</small></td><td>•••• {{r.last4}}<small>{{r.merchant||'—'}}</small></td><td>{{tradeStates[r.status]||r.status}}</td><td>{{r.amount}} {{r.currency}}</td><td>{{r.merchant_amount}} {{r.merchant_currency}}</td><td>{{r.authorization||'—'}}</td></tr></tbody></table>
  <table v-else-if="type==='funds'"><thead><tr><th>时间</th><th>变动类型</th><th>卡片</th><th>变动金额</th><th>变动后账户余额</th><th>关联交易</th></tr></thead><tbody><tr v-for="r in visible" :key="r.id"><td>{{at(r.at)}}</td><td>{{fundNames[r.kind]||`待核实（${r.kind}）`}}</td><td>{{r.last4?'•••• '+r.last4:'账户'}}<small>{{r.serial_no}}</small></td><td :class="{negative:Number(r.amount)<0}">{{r.amount}} {{r.currency}}</td><td>{{r.balance}} {{r.currency}}</td><td>{{r.link||'—'}}</td></tr></tbody></table>
  <table v-else-if="type==='recharges'"><thead><tr><th>时间 / 订单</th><th>状态</th><th>充值总额</th><th>实际到账</th><th>平台返回手续费</th><th>成本按 1% 计算</th></tr></thead><tbody><tr v-for="r in visible" :key="r.id"><td>{{at(r.at)}}<small>{{r.id}}</small></td><td>{{r.status==='1'?'成功':r.status==='5'?'已作废':r.status}}</td><td>{{r.amount}} {{r.currency}}</td><td>{{r.received}} {{r.currency}}</td><td>{{r.fee}} {{r.currency}}</td><td>{{r.status==='1'?(Number(r.amount)*.01).toFixed(2):'—'}} {{r.currency}}</td></tr></tbody></table>
  <table v-else><thead><tr><th>日期</th><th>充值</th><th>付款 / 撤销 / 退款</th><th>开卡费</th><th>撤销费</th><th>币种</th></tr></thead><tbody><tr v-for="r in visible" :key="r.day+'-'+r.currency"><td>{{r.day}}</td><td>{{r.values.income_day||'0'}}</td><td>{{r.values.consume_day||'0'}} / {{r.values.cancel_day||'0'}} / {{r.values.refund_day||'0'}}</td><td>{{r.values.open_card_day||'0'}}</td><td>{{r.values.un_auth_day||r.values.cancel_fee||'0'}}</td><td>{{r.currency}}</td></tr></tbody></table>
  <div v-if="!visible.length" class="empty">暂无符合条件的流水</div></div>
  <Pagination :page="page" :page-size="size" :total="filtered.length" @update:page="page=$event" @update:page-size="size=$event;page=1"/>
  <p class="note">{{type==='days'?'平台日汇总仅供核对，不重复计入费用。':type==='trades'?'授权和撤销可能各有一条记录；成本按同一授权合并计算，未回款授权不当作成本。':'资金划转不当作成本。费用取实际明细；充值手续费单独按成功充值总额的 1% 计算。'}} 最近同步 {{at(data.finance?.at)}}</p>
 </section></div></Teleport>
</template>
<style scoped>
.modal-backdrop{z-index:12000}.ledger{width:min(1200px,96vw);max-height:92vh;overflow:auto}.ledger h2{font-size:18px}nav{display:flex;gap:8px;margin:20px 0;border-bottom:1px solid var(--line);padding-bottom:10px}nav button{background:none;border:0;border-radius:7px;color:var(--muted);padding:9px 15px;cursor:pointer}nav .selected{background:var(--green-soft);color:var(--green-strong)}.filters{display:flex;flex-wrap:wrap;align-items:center;gap:10px;margin-bottom:16px;font-size:12px}.filters input:not([type=checkbox]),select{background:var(--surface);color:var(--text);border:1px solid var(--line);border-radius:6px;padding:9px}.filters input[type=search]{width:245px}.table-wrap{overflow:auto}table{width:100%;border-collapse:collapse;font-size:12px;text-align:left}th,td{padding:14px;border-bottom:1px solid var(--line);vertical-align:top}th{white-space:nowrap;background:var(--bg);color:var(--muted);font-weight:400}td{min-width:110px;max-width:270px;overflow-wrap:anywhere}small{display:block;color:var(--muted);margin-top:7px;font-size:11px}.note{font-size:11px;line-height:1.8;color:var(--muted)}.empty{text-align:center;padding:35px;color:var(--muted)}.negative{color:var(--red)}
</style>
