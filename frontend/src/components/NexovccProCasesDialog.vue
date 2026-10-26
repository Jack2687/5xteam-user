<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '../api'
import { formatTime } from '../utils'
import { returnStates } from '../nexovcc'
import { sortReturnCases } from '../nexovcc-case-details'
import NexovccCaseTable from './NexovccCaseTable.vue'
import NexovccLedgerDialog from './NexovccLedgerDialog.vue'
import Pagination from './Pagination.vue'
const props=defineProps({defaultPageSize:{type:Number,default:10}})
const emit=defineEmits(['updated','open-card'])
const opened=ref(false),busy=ref(false),loading=ref(false),error=ref(''),target=ref({}),data=ref({cards:[],cases:[],finance:{},groups:[]})
const edit=ref(null),ledger=ref(null),state=ref(''),page=ref(1),size=ref(props.defaultPageSize)
let generation=0, pollTimer
async function oaiUpdated(){await load();emit('updated')}
watch(opened,value=>{clearInterval(pollTimer);if(value)pollTimer=setInterval(()=>{if(!loading.value&&!busy.value)load(true)},5000)})
const rows=computed(()=>sortReturnCases(data.value.cases.filter(c=>(!target.value.email||c.email.toLowerCase()===target.value.email.toLowerCase())&&(!target.value.cardID||c.card_id===target.value.cardID)&&(!state.value||c.state===state.value))))
const visible=computed(()=>rows.value.slice((page.value-1)*size.value,page.value*size.value))
watch(state,()=>page.value=1)
watch(()=>props.defaultPageSize,v=>{size.value=v;page.value=1})
async function load(background=false){
 const gen=++generation;if(!background)loading.value=true;error.value=''
 try{const result=await api('/api/nexovcc/dashboard');if(gen===generation){data.value=result;page.value=Math.min(page.value,Math.max(1,Math.ceil(rows.value.length/size.value)))}}
 catch(e){if(gen===generation)error.value=e.message}
 finally{if(gen===generation)loading.value=false}
}
async function open(value){target.value=value;opened.value=true;edit.value=ledger.value=null;state.value='';page.value=1;data.value={cards:[],cases:[],finance:{},groups:[]};await load()}
function close(){if(busy.value)return;opened.value=false;edit.value=ledger.value=null;generation++;loading.value=false}
function openCard(row){close();emit('open-card',{provider:'nexovcc',cardID:row.card_id,email:row.email})}
function details({row,options={type:'trades'}}){const serial=data.value.cards.find(c=>c.card.id===row.card_id)?.card.serial_no||row.match?.group?.serial_no;if(!serial){error.value='卡片资料未同步，无法定位流水';return}ledger.value={...options,serial,group:options.scope==='card'?null:row.match?.group}}
function changeState(row){edit.value={type:'state',row,state:row.manual_state||row.state,reason:'',confirm:false};error.value=''}
function changeMatch(row){edit.value={type:'match',row,group_id:row.match?.group?.id||'',reason:'',confirm:false};error.value=''}
const groups=computed(()=>{
 if(!edit.value)return []
 const row=edit.value.row,serial=data.value.cards.find(c=>c.card.id===row.card_id)?.card.serial_no
 return (data.value.groups||[]).filter(g=>!g.non_pro&&!g.related_group_id&&g.valid&&Number(g.payment)>0&&g.serial_no===serial&&!data.value.cases.some(c=>c.id!==row.id&&c.match?.group?.id===g.id))
})
async function save(){
 const d=edit.value;if(!d||busy.value||!d.confirm)return
 busy.value=true;error.value=''
 try{
  const path='/api/nexovcc/cases/'+encodeURIComponent(d.row.id)
  const body=d.type==='state'?{state:d.state,reason:d.reason,revision:d.row.revision,confirm:true}:{group_id:d.group_id,reason:d.reason,confirm:true}
  await api(path+(d.type==='match'?'/match':''),{method:'PUT',body});edit.value=null;await load();emit('updated')
 }catch(e){error.value=e.message}finally{busy.value=false}
}
onBeforeUnmount(()=>{generation++;clearInterval(pollTimer)})
defineExpose({open})
async function tradePurposeUpdated(group){
 const index=data.value.groups.findIndex(g=>g.id===group.id)
 if(index>=0)data.value.groups[index]=group
 await load();emit('updated')
}
</script>
<template>
 <Teleport to="body"><div v-if="opened" class="modal-backdrop nex-pro-overlay" @click.self="close"><section class="modal nex-pro-cases" role="dialog" aria-modal="true" aria-label="环球开通与回款">
  <div class="modal-heading"><h2>{{target.email||'银行卡'}} · 环球开通与回款</h2><button class="btn ghost compact" :disabled="busy" @click="close">关闭</button></div>
  <p v-if="error" class="danger-text" role="alert">{{error}}</p>
  <p v-if="loading" role="status">正在读取回款与费用明细…</p>
  <p v-if="data.finance_error" class="danger-text">同步未完成，显示上次完整数据：{{data.finance_error}}</p>
  <div class="filters"><button :class="{selected:!state}" @click="state=''">全部</button><button v-for="(name,key) in returnStates" :key="key" :class="{selected:state===key}" @click="state=key">{{name}}</button><button :disabled="busy||loading" @click="load">刷新</button></div>
  <form v-if="edit" class="case-editor" @submit.prevent="save">
   <h3>{{edit.type==='state'?'调整回款状态':'关联同卡流水'}} · {{edit.row.email}}</h3>
   <label v-if="edit.type==='state'" class="field">回款状态<select v-model="edit.state" :disabled="busy"><option v-for="(name,key) in returnStates" :key="key" :value="key">{{name}}</option><option value="auto">恢复自动判断</option></select></label>
   <label v-else class="field">同卡付款流水<select v-model="edit.group_id" :disabled="busy" required><option value="">请选择已核实的流水</option><option v-for="g in groups" :key="g.id" :value="g.id">{{formatTime(g.at)}} · {{g.payment}} USD · 回款 {{g.returned}} · 授权 {{g.authorization}}</option></select></label>
   <label class="field">核实依据<textarea v-model="edit.reason" :minlength="edit.type==='state'?3:5" maxlength="500" :disabled="busy" required/></label>
   <label class="confirm"><input v-model="edit.confirm" type="checkbox" :disabled="busy" required/>已核实该账号实际付款 / 回款情况</label>
   <p class="hint">手动状态不改变真实金额；付款结果未知时不能用回款状态解除占用。</p>
   <div class="panel-actions"><button class="btn primary" :disabled="busy||!edit.confirm">{{busy?'保存中…':'保存'}}</button><button type="button" class="btn ghost" :disabled="busy" @click="edit=null">取消</button></div>
  </form>
  <NexovccCaseTable @updated="oaiUpdated" :data="data" v-else-if="!loading" compact :rows="visible" :cards="data.cards" :finance="data.finance" :running="busy" @state="changeState" @match="changeMatch" @details="details" @card="openCard"/>
  <Pagination v-if="!edit" :page="page" :page-size="size" :total="rows.length" @update:page="page=$event" @update:page-size="size=$event;page=1"/>
 </section></div></Teleport>
 <NexovccLedgerDialog v-if="opened&&ledger" :data="data" :options="ledger" :page-size="size" @close="ledger=null" @updated="tradePurposeUpdated"/>
</template>
<style scoped>
.nex-pro-overlay{z-index:11000}.nex-pro-cases{width:min(920px,95vw);max-height:88vh;overflow:auto}.modal-heading h2{font-size:17px;overflow-wrap:anywhere}.filters{display:flex;flex-wrap:wrap;gap:8px;margin:18px 0}.filters button{border:1px solid var(--line);border-radius:6px;background:none;color:var(--muted);padding:6px 12px;cursor:pointer;font-size:12px}.filters .selected{color:var(--green-strong);background:var(--green-soft)}.case-editor{border:1px solid var(--line);padding:20px;border-radius:8px}.case-editor h3{font-size:14px;overflow-wrap:anywhere}.case-editor .field{margin:16px 0}.confirm{display:flex;gap:8px;align-items:center;font-size:12px}.confirm input{width:auto}.hint{font-size:11px;color:var(--muted);margin:12px 0}.danger-text{overflow-wrap:anywhere;font-size:12px}
</style>
