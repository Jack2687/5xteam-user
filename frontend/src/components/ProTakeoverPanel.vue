<script setup>
import {computed, reactive, ref, watch, onUnmounted} from 'vue'
import {api} from '../api'
const props=defineProps({data:Object,compact:Boolean,pushSettings:Object,monitorGroup:Number})
const emit=defineEmits(['saved'])
const form=reactive({groups:[]}),password=ref(''),dirty=ref(false),busy=ref(false),error=ref(''),notice=ref('')
const groups=ref([]),groupsLoaded=ref(false),groupsLoading=ref(false),groupsError=ref('')
let disposed=false
const phases={idle:'各自运行',preparing:'提前准备',active:'接管中',recovering:'等待 EJH 恢复',released:'撤回临时分组'}
const state=computed(()=>props.data?.state||{})
const active=computed(()=>!!state.value.epoch&&state.value.phase!=='idle')
const title=computed(()=>active.value?(phases[state.value.phase]||'交接中'):(form.enabled?'已启用 · 等待接管时段':'未启用'))
const options=computed(()=>{
 const current=groups.value.map(g=>({...g}))
 for(const id of form.groups||[])if(!current.some(g=>g.id===id))current.push({id,name:groupsLoaded.value?'已失效，请取消选择':'已选分组（待刷新）',missing:groupsLoaded.value})
 return current
})
watch(()=>[props.data?.config,props.pushSettings],([c])=>{
 if(c&&!dirty.value&&(!form.version||c.version>=form.version)){
  const initial=c.version===1&&!c.groups?.length
  Object.assign(form,c,{groups:[...(initial?(props.pushSettings?.group_ids||[]):c.groups||[])]})
 }
},{immediate:true})
const format=t=>t&&!String(t).startsWith('0001')?new Date(t).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'—'
const input=()=>({...form,groups:[...(form.groups||[])],password:password.value})
function connectionChanged(){dirty.value=true}
async function action(fn){if(busy.value)return;busy.value=true;error.value='';notice.value='';try{await fn()}catch(e){if(!disposed)error.value=e.message}finally{busy.value=false}}
async function loadGroups(){
 if(groupsLoading.value)return
 groupsLoading.value=true;groupsError.value=''
 try{
  const data=await api('/api/pro-restock/groups')
  if(disposed)return
  groups.value=data;groupsLoaded.value=true
 }catch(e){if(!disposed)groupsError.value=e.message}finally{groupsLoading.value=false}
}
watch(()=>[props.compact,form.role],([compact,role])=>{if(!compact&&role==='receiver')loadGroups()},{immediate:true})
async function test(){await action(async()=>{
 const peer=await api('/api/pro-takeover/test',{method:'POST',body:input()})
 if(disposed)return
 notice.value=`连接成功 · 对方${peer.role==='receiver'?'接受接管':'提供接管'}${peer.enabled?' · 已启用':' · 尚未启用'}。${dirty.value?'请先保存配置。':'可点击启用接管。'}`
})}
function applySaved(data){Object.assign(form,data,{groups:[...(data.groups||[])]});password.value='';dirty.value=false;emit('saved')}
async function save(){await action(async()=>{
 const data=await api('/api/pro-takeover/config',{method:'PUT',body:input()})
 if(disposed)return
 applySaved(data)
 notice.value=data.enabled?'配置已保存，接管保持启用。':'配置已保存，尚未启用。两边保存好后分别点击“启用接管”。'
})}
async function toggleEnabled(){await action(async()=>{
 if(dirty.value){error.value='请先保存修改，再启用或停用接管';return}
 const data=await api('/api/pro-takeover/enabled',{method:'POST',body:{version:form.version,enabled:!form.enabled}})
 if(disposed)return
 applySaved(data)
 notice.value=data.enabled?'接管已启用，后台按所选时段交接。':'接管已停用；已有临时分组保留，待 EJH 接替就绪后撤回。'
})}
onUnmounted(()=>{disposed=true})
</script>

<template>
 <div v-if="compact&&(data?.config?.enabled||active)" class="takeover-strip" :class="{warning:state.error}">
  <b>跨项目接管 · {{title}}</b>
  <span>{{data?.config?.role==='provider'?'环球支援':'EJH 交接'}} · 分组 {{(state.groups?.length?state.groups:data?.config?.groups||[]).map(id=>'#'+id).join('、')||'由对方指定'}} · 临时挂组 {{state.covered||0}} 个</span>
  <span v-if="state.phase==='recovering'">EJH 就绪 {{state.own_ready||0}} 个 · 确认 {{state.ready_count||0}}/{{data?.config?.return_confirmations||3}}</span>
  <small v-if="state.error">{{state.error}}</small>
 </div>
 <article v-else-if="!compact" class="takeover-panel" aria-label="跨项目接管配置">
  <header><div><h2>跨项目接管</h2><p>仅接管勾选的禁补时段；结束后等 EJH 接替就绪，再撤回环球临时分组。</p></div><b class="phase">{{title}}</b></header>
  <p v-if="error" role="alert" class="message danger">{{error}}</p><p v-if="notice" role="status" class="message">{{notice}}</p>
  <form @submit.prevent="save" @input="dirty=true">
   <p>先保存两边配置，再分别启用。保存无需对方在线。</p>
   <div class="columns">
    <div>
     <label>本项目角色<select v-model="form.role" :disabled="busy||active||form.enabled" @change="connectionChanged"><option value="receiver">接受接管 · EJH 项目</option><option value="provider">提供接管 · 环球项目</option></select></label>
     <label>对方 Space 地址<input v-model.trim="form.peer_url" type="url" placeholder="http://服务器地址:端口" :disabled="busy||active||form.enabled" @input="connectionChanged"></label>
     <div class="columns"><label>对方登录账号<input v-model.trim="form.username" autocomplete="off" :disabled="busy||active||form.enabled" @input="connectionChanged"></label><label>对方登录密码<input v-model="password" type="password" autocomplete="new-password" :placeholder="form.password_present?'已保存，留空不修改':'填写对方后台密码'" :disabled="busy"></label></div>
     <button type="button" :disabled="busy||!form.peer_url||!form.username" @click="test">{{busy?'处理中…':'测试连接'}}</button>
     <p>两边连接同一个 Sub2。先分别保存角色与账密，再测试连接、启用。</p>
    </div>
    <div v-if="form.role==='receiver'">
     <div class="group-header"><b>接管分组 · 可多选</b><button type="button" :disabled="groupsLoading||busy" @click="loadGroups">{{groupsLoading?'读取中…':'刷新分组'}}</button></div>
     <p v-if="groupsError" role="alert" class="danger">{{groupsError}}</p>
     <div class="group-list">
      <label v-for="g in options" :key="g.id" class="check" :class="{danger:g.missing}"><input v-model="form.groups" type="checkbox" :value="g.id" :disabled="busy">{{g.name}} · #{{g.id}}<small v-if="g.id===monitorGroup">监控组</small></label>
      <span v-if="!options.length">{{groupsLoading?'正在读取分组…':'暂无可选分组，请检查 Pro 推送的 Sub2 连接后刷新。'}}</span>
     </div>
     <p>已选 {{form.groups.length}} 个。名称从 Sub2 刷新，首次默认带入推送分组，可自行调整。须包含本项目监控组，不能与环球常驻推送组重叠。</p>
     <p>环球已有账号和新号保留原推送分组，追加勾选的分组。接管进行中修改选择，下次接管生效。</p>
     <div class="columns"><label>提前准备（分钟）<input v-model.number="form.lead_minutes" type="number" min="0" max="120" required :disabled="busy"></label><label>切回至少就绪（个）<input v-model.number="form.return_min_accounts" type="number" min="1" max="1000" required :disabled="busy"></label><label>切回连续确认（次）<input v-model.number="form.return_confirmations" type="number" min="1" max="20" required :disabled="busy"></label></div>
     <p>在每日禁补时段逐条勾选“环球接管”。时段结束后 EJH 恢复补货，环球保留现有支援；EJH 完成推送、本批次结束，且监控连续确认可调度后撤组。默认至少 1 个、连续 3 次，失败继续保留支援。</p>
    </div>
    <div v-else><label>接管至少就绪（个）<input v-model.number="form.support_min_accounts" type="number" min="1" max="1000" required :disabled="busy"></label><label>连接检查周期（秒）<input v-model.number="form.check_seconds" type="number" min="5" max="60" required :disabled="busy"></label><p>分组和时段由接受方指定。接管时统一监控常驻组与接管组，账号与消耗按 ID 去重，沿用本项目全部补货规则。</p><p>时段结束后按自己的分组补货，现有支援等 EJH 就绪后再撤回。连接异常保留临时分组，撤回时保留原有分组。</p></div>
   </div>
   <details v-if="active||state.error" class="details"><summary>交接进度</summary><p>临时挂组 {{state.covered||0}} 个 · 本地在途 {{state.pending||0}} · 对方在途 {{state.peer_pending||0}}</p><p>EJH 自有容量 {{state.own_capacity||0}} · 当前需求 {{state.demand||0}} · 连续确认 {{state.ready_count||0}} 次</p><p>最后连接 {{format(state.contact_at)}} · 时段结束 {{format(state.end)}}</p><p v-if="state.error" class="danger">{{state.error}}</p></details>
   <footer><span>{{dirty?'有未保存修改':'不改变每日 EJH 定点合并'}}</span><div class="actions"><button type="submit" :disabled="busy" class="primary">{{busy?'处理中…':'保存配置'}}</button><button type="button" :disabled="busy||dirty||!form.version" @click="toggleEnabled">{{form.enabled?'停用接管':'启用接管'}}</button></div></footer>
  </form>
 </article>
</template>

<style scoped>
.takeover-panel{margin-top:18px;padding:22px;border:1px solid #dbe4ee;border-radius:14px;background:#fff;color:#233247}.takeover-panel header,.group-header,.takeover-panel footer{display:flex;align-items:center;justify-content:space-between;gap:16px}.takeover-panel h2{margin:0;font-size:17px}.takeover-panel p,.takeover-panel footer span{color:#64748b;font-size:12px;line-height:1.7}.phase{font-size:12px;background:#edf6ff;border-radius:16px;padding:5px 12px;white-space:nowrap}.columns{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.takeover-panel label{display:flex;flex-direction:column;gap:6px;font-size:13px;margin:12px 0}.takeover-panel label.check{flex-direction:row;align-items:center}.takeover-panel input:not([type=checkbox]),.takeover-panel select{width:100%;box-sizing:border-box;border:1px solid #cbd5e1;border-radius:7px;padding:9px;background:#fff}.takeover-panel button{padding:8px 13px;border:1px solid #cbd5e1;border-radius:7px;background:#fff;font-size:12px;cursor:pointer}.takeover-panel button:disabled{opacity:.5;cursor:wait}.takeover-panel .primary{background:#2563eb;color:#fff;border-color:#2563eb}.actions{display:flex;gap:10px}.group-list small{color:#64748b;margin-left:auto}.group-list{max-height:170px;overflow:auto;border:1px solid #e2e8f0;padding:8px 12px;border-radius:8px;font-size:12px}.group-list .check{margin:7px 0}.message,.details{padding:10px 14px;background:#f0f7ff;border-radius:8px}.danger{color:#b91c1c!important}.takeover-panel footer{border-top:1px solid #e2e8f0;padding-top:15px;margin-top:15px}.takeover-strip{display:flex;align-items:center;gap:16px;flex-wrap:wrap;padding:12px 16px;border:1px solid #bfdbfe;background:#eff6ff;border-radius:10px;margin:12px 0;font-size:12px;color:#334155}.takeover-strip.warning{background:#fff7ed;border-color:#fed7aa}.takeover-strip small{flex-basis:100%;color:#b45309}@media(max-width:850px){.columns{grid-template-columns:1fr}}
</style>
