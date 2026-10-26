<script setup>
import {computed,ref,watch,nextTick} from 'vue'
import {api} from '../api'
import {formatTime} from '../utils'
import {tradeStates,rmb,cents,tradeGroupNeedsReview} from '../nexovcc-finance'
import Pagination from './Pagination.vue'
const props=defineProps({data:{type:Object,required:true},serial:{type:String,default:''},pageSize:{type:Number,default:10}})
const emit=defineEmits(['saved','busy'])
const mode=ref('pending'),query=ref(''),page=ref(1),size=ref(props.pageSize),draft=ref(null),saving=ref(false),error=ref(''),message=ref('')
const groups=computed(()=>props.data.review_groups||[])
const reviews=computed(()=>props.data.trade_reviews||[])
const trades=g=>(props.data.finance?.trades||[]).filter(t=>g?.trade_ids?.includes(t.id))
const info=id=>groups.value.find(g=>g.id===id)
const review=id=>reviews.value.find(r=>r.group_id===id)
const at=g=>trades(g).reduce((latest,t)=>Date.parse(t.at)>Date.parse(latest||'1970-01-01')?t.at:latest,'')
const last4=g=>trades(g)[0]?.last4||'—'
const filtered=computed(()=>{
 const rows=mode.value==='pending'?groups.value.filter(g=>tradeGroupNeedsReview(props.data,g)&&!review(g.id)?.applied).map(g=>({id:g.id,g})):reviews.value.map(r=>({id:r.group_id,g:info(r.group_id),r}))
 const q=query.value.trim().toLowerCase()
 return rows.filter(row=>(!props.serial||row.g?.serial_no===props.serial)&&(!q||[row.id,row.g?.authorization,last4(row.g),row.r?.reason,...trades(row.g).map(t=>t.id)].some(v=>String(v||'').toLowerCase().includes(q)))).sort((a,b)=>Date.parse(b.r?.updated_at||at(b.g)||'1970-01-01')-Date.parse(a.r?.updated_at||at(a.g)||'1970-01-01'))
})
const visible=computed(()=>filtered.value.slice((page.value-1)*size.value,page.value*size.value))
const refundsOnly=g=>trades(g).length>0&&trades(g).every(t=>['REFUNDED','REVERSED'].includes(t.status)&&t.currency==='USD')
const targets=g=>groups.value.filter(t=>t.id!==g.id&&t.serial_no===g.serial_no&&t.valid&&t.authorization&&cents(t.payment)-cents(t.returned)>=cents(g.returned)&&!review(t.id)?.applied)
function edit(g,kind,voided=false){
 const old=review(g?.id);error.value='';message.value=''
 draft.value={g,kind,voided,amount:old?.amount||g?.expense_limit||'',target_id:old?.target_id||'',reason:voided?'':old?.reason||'',revision:old?.revision||0}
 nextTick(()=>document.querySelector('.review-form')?.scrollIntoView({block:'start',behavior:'smooth'}))
}
async function save(){
 if(saving.value||!draft.value)return
 const d=draft.value;saving.value=true;emit('busy',true);error.value=''
 try{
  const saved=await api('/api/nexovcc/trade-reviews/'+encodeURIComponent(d.g.id),{method:'PUT',body:{kind:d.kind,amount:d.amount,target_id:d.kind==='refund_link'?d.target_id:'',reason:d.reason,revision:d.revision,voided:d.voided,fingerprint:d.g.fingerprint,target_fingerprint:info(d.target_id)?.fingerprint||''}})
  emit('saved',saved);draft.value=null;mode.value='history';message.value='已保存，成本和资金核对已重新计算'
 }catch(e){error.value=e.message}finally{saving.value=false;emit('busy',false)}
}
watch([mode,query],()=>page.value=1)
watch(()=>props.pageSize,v=>{size.value=v;page.value=1})
watch(()=>filtered.value.length,()=>{page.value=Math.min(page.value,Math.max(1,Math.ceil(filtered.value.length/size.value)))})
</script>
<template>
 <section class="trade-review">
  <div class="review-toolbar"><div><button :class="{active:mode==='pending'}" @click="mode='pending'">待核实</button><button :class="{active:mode==='history'}" @click="mode='history'">处理记录</button></div><input v-model="query" type="search" placeholder="尾号、授权码、流水号或原因"/></div>
  <p class="hint">其他费用计入 USD / R 成本，并保留原因。退款可关联同卡原付款；原始流水保留。</p>
  <p v-if="error" class="error" role="alert">{{error}}</p><p v-if="message" class="hint" role="status">{{message}}</p>
  <form v-if="draft" class="review-form" @submit.prevent="save">
   <strong>{{draft.voided?'撤销处理':draft.kind==='other_fee'?'记为其他费用':'关联原付款'}} · 卡尾号 {{last4(draft.g)}} · {{draft.g.authorization||'无授权码'}}</strong>
   <label v-if="!draft.voided&&draft.kind==='other_fee'">净费用 USD<input v-model="draft.amount" type="text" inputmode="decimal" pattern="[0-9]+([.][0-9]{1,2})?" required :disabled="saving"/><small>{{rmb(cents(draft.amount))}} R · 不含已自动计入的资金手续费</small></label>
   <label v-if="!draft.voided&&draft.kind==='refund_link'">选择同卡原付款<select v-model="draft.target_id" required :disabled="saving"><option value="" disabled>请选择原付款</option><option v-for="g in targets(draft.g)" :key="g.id" :value="g.id">{{g.authorization}} · 付款 {{g.payment}} / 已退 {{g.returned}} USD · {{formatTime(g.at)}}</option></select></label>
   <label>处理原因<textarea v-model="draft.reason" required minlength="5" maxlength="500" rows="2" placeholder="填写核实依据或费用原因，至少 5 个字" :disabled="saving"/></label>
   <small v-if="!draft.voided&&draft.kind==='other_fee'">只调整财务分类，不确认退款到账，也不解除提现或销卡限制。</small>
   <div class="actions"><button class="primary" :disabled="saving">{{saving?'保存中…':'确认保存'}}</button><button type="button" :disabled="saving" @click="draft=null">取消</button></div>
  </form>
  <article v-for="row in visible" :key="row.id" class="review-row">
   <header><div><strong>•••• {{last4(row.g)}} · 授权 {{row.g?.authorization||'未提供'}}</strong><small>{{formatTime(row.r?.updated_at||at(row.g))}}</small></div><span>{{row.r?row.r.voided?'已撤销':row.r.applied?'已处理':'需重新核实':row.g.problem}}</span></header>
   <template v-if="row.r"><p>{{row.r.kind==='other_fee'?`其他费用 ${row.r.amount} USD · ${rmb(cents(row.r.amount))} R`:`退款已关联授权 ${info(row.r.target_id)?.authorization||row.r.target_id}`}}</p><p>原因：{{row.r.reason}}</p><p v-if="row.r.problem" class="error">{{row.r.problem}}</p></template>
   <p v-else>原账本汇总：付款 {{row.g.payment}} / 回款 {{row.g.returned}} USD · 已计手续费 {{row.g.fees}} USD</p>
   <details :open="mode==='pending'"><summary>查看原始交易（{{trades(row.g).length}} 笔）</summary><div class="table-scroll"><table><thead><tr><th>时间 / 流水号</th><th>状态 / 商户</th><th>账单金额</th><th>商户金额</th></tr></thead><tbody><tr v-for="t in trades(row.g)" :key="t.id"><td>{{formatTime(t.at)}}<small>{{t.id}}</small></td><td>{{tradeStates[t.status]||t.status}}<small>{{t.merchant}}</small></td><td>{{t.amount}} {{t.currency}}</td><td>{{t.merchant_amount}} {{t.merchant_currency}}</td></tr></tbody></table></div></details>
   <div class="actions"><template v-if="!row.g"><button v-if="row.r&&!row.r.voided" :disabled="saving" @click="edit({id:row.id,trade_ids:[]},row.r.kind,true)">撤销处理</button></template><template v-else><template v-if="!row.r||row.r.voided"><button :disabled="saving||!!row.g.expense_problem" :title="row.g.expense_problem" @click="edit(row.g,'other_fee')">记为其他费用</button><button v-if="refundsOnly(row.g)" :disabled="saving||row.g.linked" @click="edit(row.g,'refund_link')">关联原付款</button><small v-if="row.g.expense_problem">{{row.g.expense_problem}}</small></template><template v-else><button :disabled="saving" @click="edit(row.g,row.r.kind)">修改 / 重新核实</button><button :disabled="saving" @click="edit(row.g,row.r.kind,true)">撤销处理</button></template></template></div>
  </article>
  <p v-if="!visible.length" class="empty">{{mode==='pending'?'暂无待核实交易':'暂无处理记录'}}</p>
  <Pagination :page="page" :page-size="size" :total="filtered.length" @update:page="page=$event" @update:page-size="size=$event;page=1"/>
 </section>
</template>
<style scoped>
.trade-review{font-size:13px}.review-toolbar,.actions,header{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}.review-toolbar>div,.actions{display:flex;gap:8px}.actions{justify-content:flex-start;margin-top:12px}button{border:1px solid var(--line);background:var(--surface);color:var(--text);border-radius:7px;padding:8px 12px;cursor:pointer}button.active,.primary{background:var(--green-soft);color:var(--green-strong)}button:disabled{opacity:.5;cursor:default}input,select,textarea{border:1px solid var(--line);border-radius:6px;padding:9px;background:var(--surface);color:var(--text);box-sizing:border-box;max-width:100%}.review-toolbar input{width:280px}.hint,small{color:var(--muted);font-size:11px;line-height:1.8}small{display:block;overflow-wrap:anywhere}.review-row{border:1px solid var(--line);border-radius:10px;padding:16px;margin:12px 0}.review-row p{font-size:12px;line-height:1.8;overflow-wrap:anywhere}.review-row header span{color:#b7791f;font-size:12px}.review-form{background:var(--bg);padding:18px;border:1px solid var(--line);border-radius:10px;margin:15px 0}.review-form label{display:flex;flex-direction:column;gap:6px;margin:14px 0}.error{color:var(--red)}summary{cursor:pointer;font-size:12px;color:var(--muted)}.table-scroll{overflow:auto}table{border-collapse:collapse;width:100%;font-size:12px;margin-top:12px}th,td{text-align:left;padding:10px;border-bottom:1px solid var(--line);min-width:110px}th{color:var(--muted);font-weight:400}.empty{text-align:center;color:var(--muted);padding:28px}
</style>
