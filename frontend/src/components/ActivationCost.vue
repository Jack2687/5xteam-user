<script setup>
import { computed, ref, watch } from 'vue'
import { api } from '../api'
import { cardHasCase, tradeKinds } from '../ejh'
import { formatTime } from '../utils'
const props=defineProps({row:{type:Object,required:true},details:Boolean,editable:Boolean})
const emit=defineEmits(['updated'])
const updated=ref(null),open=ref(false),busy=ref(false),error=ref(''),trades=ref([]),selection=ref(''),evidence=ref('')
watch(()=>props.row,()=>{updated.value=null})
const current=computed(()=>updated.value||props.row)
const charge=computed(()=>current.value.charge)
const amount=(value,currency)=>value!==''&&value!=null&&currency ? value+' '+currency : '未提供'
const headline=computed(()=>!charge.value ? (current.value.card_type==='ejh'?'卡片开通金额：待关联流水':'卡片开通金额：暂无卡台流水') : (charge.value.type==='1'?'授权占用：':'清算刷卡：')+amount(charge.value.card_amount,charge.value.card_currency))
async function begin(){
 if(busy.value)return
 open.value=true;busy.value=true;error.value='';evidence.value='';trades.value=[];selection.value=current.value.original_trade_id||''
 try{const d=await api('/api/ejh/dashboard');trades.value=(d.trades||[]).filter(t=>cardHasCase({id:t.card_id},current.value)&&['1','4'].includes(t.trade.tradeType))}catch(e){error.value=e.message}finally{busy.value=false}
}
async function save(){
 if(busy.value)return
 busy.value=true;error.value=''
 try{
  await api('/api/ejh/cases/'+encodeURIComponent(current.value.id)+'/charge',{method:'PUT',body:{trade_id:selection.value,evidence:evidence.value}})
  const d=await api('/api/ejh/dashboard'),row=d.cases.find(c=>c.id===current.value.id)
  if(!row)throw new Error('关联已保存，但未读取到更新后的订单，请刷新页面')
  updated.value=row;emit('updated',row);open.value=false
 }catch(e){error.value=e.message}finally{busy.value=false}
}
</script>
<template><div class="activation-cost"><strong>{{headline}}</strong><small v-if="charge">{{tradeKinds[charge.type]}} · {{charge.status==='2'?'已入账':'待入账 / 未知'}}<span v-if="charge.type==='1'">（授权金额不代表最终清算）</span></small><template v-if="details"><p v-if="charge">交易金额 {{amount(charge.trade_amount,charge.trade_currency)}} · 结算金额 {{amount(charge.bill_amount,charge.bill_currency)}}<br>流水 {{charge.trade_no}} · {{charge.merchant||'商户未提供'}}<br>交易 {{charge.trade_time||'—'}} · 同步 {{formatTime(charge.synced_at)}}<br>关联依据：{{current.charge_evidence||'已有回款关联的原交易'}}</p><p v-else>同步卡台交易后，按业务凭据关联同卡授权或清算流水。不能仅凭相同金额或时间认定账号归属。</p><p v-if="current.credits">供应商 Credits：已扣 {{current.credits.charged}} · 预留 {{current.credits.reserved}} · 已释放 {{current.credits.released}}<br>供应商结算状态 {{current.credits.settlement||'未提供'}} · 更新 {{formatTime(current.credits.updated_at)}}</p><p>刷卡、交易、结算金额各为独立币种口径；授权与清算不相加。退款单独列出，Credits 不计入卡余额。</p></template><button v-if="editable&&current.card_type==='ejh'&&current.payment_status==='success'" type="button" class="btn ghost compact" @click.stop="begin">{{charge?'核对 / 更正刷卡流水':'关联刷卡流水'}}</button>
<Teleport to="body"><div v-if="open" class="modal-backdrop charge-overlay" @click.self="!busy&&(open=false)"><section class="modal charge-dialog" role="dialog" aria-modal="true" aria-label="关联开通刷卡流水"><h2>关联开通刷卡流水</h2><p>{{current.email}} · {{current.id}}</p><p>每笔开通仅保留一笔授权或清算依据。核实后可用清算替换授权，不能重复归属其他账号；有回款依赖时须先处理回款关联。</p><p v-if="error" role="alert" class="danger-text">{{error}}</p><p v-if="busy" role="status">正在读取或保存，请稍候…</p><form @submit.prevent="save"><label class="field"><span>同一张实体卡的授权 / 清算</span><select v-model="selection" :disabled="busy"><option value="">解除刷卡关联</option><option v-for="t in trades" :key="t.id" :value="t.id">{{t.trade.tradeNo}} · {{tradeKinds[t.trade.tradeType]}} · 刷卡 {{amount(t.trade.cardAmount,t.trade.cardCurrency)}} · {{t.trade.tradeTime}}</option></select></label><label class="field"><span>业务对应关系 / 更正依据</span><textarea v-model="evidence" minlength="3" required :disabled="busy"/></label><button class="btn primary" :disabled="busy">{{busy?'处理中…':'保存关联'}}</button><button type="button" class="btn ghost" :disabled="busy" @click="open=false">关闭</button></form></section></div></Teleport></div></template>
<style scoped>.activation-cost{font-size:12px;line-height:1.7;overflow-wrap:anywhere}.activation-cost strong,.activation-cost small{display:block}.activation-cost small,.activation-cost p{color:var(--muted)}.activation-cost p{margin:8px 0}.charge-overlay{z-index:12000}.charge-dialog{max-height:85vh;overflow:auto;width:min(800px,94vw)}.charge-dialog select{max-width:100%}</style>
