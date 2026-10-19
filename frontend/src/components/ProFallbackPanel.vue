<script setup>
import {ref,reactive,computed,onMounted,onUnmounted} from 'vue'
import {api} from '../api'
import {createLatestRequest} from '../latestRequest'
defineProps({settings:Boolean,compact:Boolean})
const detailDialog=ref(null)
const fetchError=ref('')
const state=ref(null),form=reactive({account_ids:[],group_ids:[]}),password=ref(''),groups=ref([]),accounts=ref([]),search=ref(''),busy=ref(false),error=ref(''),notice=ref(''),dirty=ref(false),loaded=ref(false)
const request=createLatestRequest(),clock=ref(Date.now()),clockOffset=ref(0)
let timer,disposed=false,revision=0
const filtered=computed(()=>accounts.value.filter(a=>(a.name+' '+a.id+' '+a.type).toLowerCase().includes(search.value.toLowerCase())))
const selectedGroups=c=>[...(c.group_ids??(c.group_id?[c.group_id]:[]))]
const groupOptions=computed(()=>[...groups.value,...form.group_ids.filter(id=>!groups.value.some(g=>g.id===id)).map(id=>({id,name:'已保存分组'}))])
const phases={scheduled:'禁补时段：开启兜底',active:'目标：开启兜底',off:'目标：关闭兜底',disabled:'兜底未启用',unknown:'保持当前调度'}
const fmt=t=>t&&!String(t).startsWith('0001')?new Date(t).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'尚未核验'
const stale=computed(()=>!state.value?.checked_at||String(state.value.checked_at).startsWith('0001')||clock.value+clockOffset.value-Date.parse(state.value.checked_at)>1000*(Number(state.value?.config?.check_seconds||10)+50))
const unresolved=computed(()=>state.value?.accounts?.filter(a=>a.error||a.observed==null||a.observed!==a.desired)||[])
const pending=computed(()=>state.value?.config?.enabled?state.value.config.account_ids.filter(id=>!state.value.accounts.some(a=>a.id===id&&!a.old_connection)).length:0)
const cardStatus=computed(()=>{
 if(fetchError.value)return '读取异常'
 if(!state.value)return '读取中…'
 if(state.value.error||(state.value.plan.phase!=='scheduled'&&state.value.monitor?.error)||state.value.plan.hold)return '核验异常'
 if(stale.value&&(state.value.config.enabled||state.value.accounts.length))return '待核验'
 if(pending.value||unresolved.value.length||state.value.accounts.some(a=>a.desired!==(!a.old_connection&&state.value.config.account_ids.includes(a.id)&&state.value.plan.active)))return '切换待确认'
 return {scheduled:'时段兜底已开启',active:'兜底已开启',off:'兜底已关闭',disabled:'未启用',unknown:'保持当前调度'}[state.value.plan.phase]||'待核验'
})
async function refresh(initial=false,background=false){
 if(disposed||(background&&busy.value))return
 const version=revision
 try{await request.run(signal=>api('/api/pro-restock/fallback',{signal}),data=>{if(disposed||version!==revision)return;state.value=data;fetchError.value='';clock.value=Date.now();const at=Date.parse(data.server_time);if(Number.isFinite(at))clockOffset.value=at-clock.value;if(initial){Object.assign(form,data.config);form.account_ids=[...(data.config.account_ids||[])];form.group_ids=selectedGroups(data.config);dirty.value=false;password.value=''}},{background})}catch(e){if(!disposed&&version===revision)fetchError.value=e.message}
}
async function action(fn){if(busy.value||disposed)return;busy.value=true;revision++;error.value='';notice.value='';try{await fn()}catch(e){if(!disposed)error.value=e.message}finally{busy.value=false}}
function input(preview=false){return {...form,enabled:preview?false:form.enabled,password:password.value,group_id:form.group_ids[0]||0,group_ids:[...form.group_ids],account_ids:[...form.account_ids]}}
function resetConnection(){groups.value=[];resetGroup();form.group_id=0;form.group_ids=[];password.value='';dirty.value=true}
function resetGroup(){accounts.value=[];form.account_ids=[];loaded.value=false;dirty.value=true}
async function loadGroups(){await action(async()=>{const data=await api('/api/pro-restock/fallback/groups',{method:'POST',body:input(true)});if(disposed)return;groups.value=data;notice.value='连接成功，已读取 OpenAI 分组。'})}
async function loadAccounts(){await action(async()=>{const data=await api('/api/pro-restock/fallback/accounts',{method:'POST',body:input(true)});if(disposed)return;accounts.value=data;loaded.value=true;notice.value='已读取 '+data.length+' 个 OpenAI API Key / OAuth 账号。读取列表不会切换调度。'})}
function selectVisible(){form.account_ids=[...new Set([...form.account_ids,...filtered.value.map(a=>a.id)])];dirty.value=true}
function remove(id){form.account_ids=form.account_ids.filter(value=>value!==id);dirty.value=true}
async function save(){await action(async()=>{
 const c=await api('/api/pro-restock/fallback',{method:'PUT',body:input()});if(disposed)return
 Object.assign(form,c);form.account_ids=[...(c.account_ids||[])];form.group_ids=selectedGroups(c);password.value='';dirty.value=false;await refresh();notice.value='兜底配置已保存。后台按禁补时段设置和主监控分组账号数量核验调度。'
})}
async function retry(){await action(async()=>{try{const data=await api('/api/pro-restock/fallback/retry',{method:'POST'});if(!disposed){state.value=data;notice.value='核验完成。'}}finally{await refresh()}})}
async function reload(){await action(async()=>{await refresh(true);groups.value=[];accounts.value=[];loaded.value=false})}
onMounted(async()=>{await refresh(true);if(!disposed)timer=setInterval(()=>{clock.value=Date.now();refresh(false,true)},5000)})
onUnmounted(()=>{disposed=true;revision++;clearInterval(timer);request.dispose()})
</script>
<template>
<article v-if="compact" class="fallback-card" aria-label="兜底调度"><span>兜底调度</span><strong>{{cardStatus}}</strong><p>{{state?phases[state.plan.phase]:'正在读取状态'}} · 已选 {{state?.config?.account_ids?.length||0}} 个</p><p :title="fetchError||state?.error||state?.monitor?.error||state?.plan?.reason">{{fetchError||state?.error||(state?.plan?.phase==='scheduled'?state.plan.reason:state?.monitor?.error)||(state?.monitor?'监控组 '+(state.monitor.count??'未知')+' 个账号 · '+(state.config.check_seconds||3)+' 秒检查':'等待监控数据')}}</p><button type="button" aria-haspopup="dialog" @click="detailDialog?.showModal()">查看兜底详情</button></article>
<component :is="compact?'dialog':'div'" ref="detailDialog" :class="{'fallback-dialog':compact}" :aria-label="compact?'兜底调度详情':undefined" @click="compact&&$event.target===detailDialog&&detailDialog.close()">
<article class="fallback-panel" aria-label="兜底调度">
 <button v-if="compact" type="button" class="close-detail" aria-label="关闭兜底详情" @click="detailDialog.close()">✕</button>
 <header><div><h2>兜底调度</h2><p>空组自动兜底；可在补货配置中开启完全禁补时段强制兜底</p></div><span class="badge">{{state?phases[state.plan.phase]:'读取状态中…'}}</span></header>
 <p v-if="error||fetchError" class="alert error" role="alert">{{error||fetchError}}</p><p v-if="notice" class="alert" role="status">{{notice}}</p>
 <template v-if="state">
  <p>{{state.plan.reason}}</p>
  <p v-if="state.monitor">主监控组：{{state.monitor.group_name||'未读取名称'}} #{{state.monitor.group_id||'未设置'}} · {{state.monitor.error||state.plan.hold?'最近成功采集':'当前采集'}}账号数：<b>{{state.monitor.count??'未知'}}</b> · 采集时间：{{fmt(state.monitor.at)}}</p>
  <p v-if="state.monitor?.error" class="alert warn">{{state.monitor.error}}。保留已有调度，失败不会当作空组；后台自动重试，也可立即重试。</p>
  <p v-if="state.error" class="alert error" role="alert">{{state.error}}</p>
  <p v-if="stale&&state.config.enabled" class="alert warn">尚未完成近期核验；上方为计划状态，不能视为账号已完成切换。</p>
  <p v-if="pending" class="alert warn">{{pending}} 个所选账号等待后台首次核验。</p>
  <p v-if="state.config.enabled">已选择 {{selectedGroups(state.config).length}} 个分组 · {{state.config.account_ids.length}} 个账号 · 兜底检查周期 {{state.config.check_seconds}} 秒；失败后等待同样秒数重试。</p>
  <p v-if="unresolved.length" class="alert warn">{{unresolved.length}} 个账号尚未确认目标状态或存在异常；后台继续核验。</p>
  <details v-if="state.accounts.length"><summary>查看逐账号调度状态（{{state.accounts.length}}）</summary><div class="table-wrap"><table><thead><tr><th>账号</th><th>目标</th><th>最近实际状态</th><th>核验时间</th><th>说明</th></tr></thead><tbody><tr v-for="(a,index) in state.accounts" :key="index"><td>{{a.name||'账号'}} #{{a.id}}<small v-if="a.old_connection">旧连接收尾</small></td><td>{{a.desired?'开启':'关闭'}}</td><td>{{a.observed==null?'未知':a.observed?'开启':'关闭'}}</td><td>{{fmt(a.checked_at)}}</td><td>{{a.error||'已确认'}}</td></tr></tbody></table></div></details>
  <div class="toolbar"><button type="button" :disabled="busy" @click="retry">立即核验／重试</button><span>核验以已保存配置为准</span></div>
  <form v-if="settings" @submit.prevent="save" @input="dirty=true">
   <fieldset :disabled="busy">
   <label class="check"><input v-model="form.enabled" type="checkbox">启用兜底调度</label>
   <p>沿用 Pro 推送的主 Sub2 连接和自动补货选定的监控组，通常按分组实际账号数量判断；若开启“禁补时段立即启用兜底调度”，仅完全禁补时段优先开启；允许卡台补货的时段及其余时间按空组判断。与补货总开关、观察模式独立；登录、开通、授权期间仍为空组就继续兜底。关闭此开关、取消选择或更换连接后，原账号调度会收尾关闭。</p>
   <div class="fields">
    <label>兜底 Sub2 地址<input v-model.trim="form.url" type="url" placeholder="https://sub2.example.com" :required="form.enabled" @change="resetConnection"></label>
    <label>管理员邮箱<input v-model.trim="form.email" type="email" autocomplete="off" :required="form.enabled" @change="resetConnection"></label>
    <label>管理员密码<input v-model="password" type="password" autocomplete="new-password" :placeholder="form.password_present?'已加密保存，留空保留':'请输入管理员密码'"></label>
    <div class="group-selection"><span>兜底分组 · 已选 {{form.group_ids.length}} 个</span><div class="group-list"><label v-for="g in groupOptions" :key="g.id" class="check"><input v-model="form.group_ids" type="checkbox" :value="g.id" @change="resetGroup">{{g.name}} · #{{g.id}}</label><p v-if="!groupOptions.length">先读取分组，再勾选一个或多个。</p></div></div>
   </div>
   <div class="toolbar"><button type="button" :disabled="busy" @click="loadGroups">连接检查／读取分组</button><button type="button" :disabled="busy||!form.group_ids.length" @click="loadAccounts">读取／刷新兜底账号</button></div>
   <p>管理员邮箱和密码用于连接这套 Sub2；下方为所选分组的账号合集，同一账号只显示和调度一次。API Key / OAuth 指账号类型。</p>
   <div class="selection"><div class="toolbar"><b>多选兜底账号 · 已选 {{form.account_ids.length}}</b><button type="button" :disabled="busy||!loaded" @click="selectVisible">选中搜索结果</button><button type="button" :disabled="busy" @click="form.account_ids=[];dirty=true">清空选择</button></div>
    <label>搜索账号<input v-model="search" type="search" placeholder="账号名称、ID 或类型"></label>
    <div class="account-list"><label v-for="a in filtered" :key="a.id" class="account check"><input v-model="form.account_ids" type="checkbox" :value="a.id"><span><b>{{a.name||'未命名'}} #{{a.id}}</b><small>{{a.type==='apikey'?'API Key':'OAuth'}} · {{a.status}} · 读取时调度{{a.schedulable?'开启':'关闭'}}</small></span></label></div>
    <p v-if="!filtered.length">{{loaded?'没有匹配的 OpenAI API Key / OAuth 账号':'读取账号列表后勾选；已保存的选择仍保留。'}}</p>
    <p v-for="id in form.account_ids.filter(id=>!accounts.some(a=>a.id===id))" :key="id">已选 #{{id}} · {{loaded?'列表中未找到，请取消或重新检查分组':'尚未读取详情'}} <button type="button" @click="remove(id)">取消选择</button></p>
   </div>
   <div class="fields"><label>兜底检查／失败重试周期（秒）<input v-model.number="form.check_seconds" type="number" min="2" max="300" step="1" required></label></div>
   <p>默认 3 秒，与“运行节奏”里的补货检查周期分开。未处于强制兜底时段时，发现空组开启所选兜底，发现账号已入组关闭，不等待整批流程完成或额度刷新。接口耗时和 Sub2 调度缓存也会影响切换速度，轮询无法保证绝对无间隙。组内存在禁用或无额度账号仍算有账号。</p>
   <footer><span>{{dirty?'有未保存的兜底配置':'兜底配置单独保存'}} · v{{form.version}}</span><div class="toolbar"><button type="button" :disabled="busy" @click="reload">重新载入</button><button class="primary" :disabled="busy" type="submit">{{busy?'处理中…':'保存兜底配置'}}</button></div></footer>
   </fieldset>
  </form>
 </template>
</article>
</component>
</template>
<style scoped>
.fallback-card{height:100%;box-sizing:border-box;background:#fff;border:1px solid var(--border,#dce7e9);border-radius:11px;padding:21px;min-width:0;color:var(--ink,#234752)}.fallback-card>span{font-size:12px;color:var(--muted,#708a94)}.fallback-card>strong{display:block;font-size:22px;font-weight:550;margin:21px 0 5px;overflow-wrap:anywhere}.fallback-card p{font-size:11px;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}.fallback-dialog{border:0;border-radius:13px;padding:0;width:min(850px,calc(100vw - 32px));max-height:85vh;color:var(--ink,#234752);background:#fff;box-shadow:0 24px 100px #12383d33}.fallback-dialog::backdrop{background:#102e3c77;backdrop-filter:blur(3px)}.fallback-dialog .fallback-panel{margin:0;position:relative;padding-top:50px}.close-detail{position:absolute;right:15px;top:12px}:global([data-theme=dark]) .fallback-card,:global([data-theme=dark]) .fallback-dialog{background:#192e38}@media(max-width:600px){.fallback-card{padding:15px}.fallback-card>strong{font-size:18px}}
.group-selection{margin:12px 0;font-size:12px}.group-list{max-height:180px;overflow:auto;border:1px solid var(--border,#dce7e9);border-radius:6px;padding:0 12px;margin-top:8px}
fieldset{border:0;padding:0;margin:0;min-width:0}
.fallback-panel{background:#fff;border:1px solid var(--border,#dce7e9);border-radius:11px;padding:21px;margin-bottom:18px;color:var(--ink,#234752);font-size:13px}header,footer,.toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}h2{font-size:15px;margin:0 0 8px}p{color:var(--muted,#708a94);line-height:1.8;margin:8px 0}button,input,select{font:inherit;border:1px solid var(--border,#dce7e9);border-radius:6px;background:white;color:inherit;padding:9px 12px}button{cursor:pointer}button:disabled{opacity:.5;cursor:default}.primary{background:#008786;color:white;border-color:#008786}.badge,.alert{background:#edf7f5;color:#31736e;border-radius:6px;padding:9px 12px}.warn{background:#fff8ec;color:#976f31}.error{background:#fff1f0;color:#a45148}.fields{display:grid;grid-template-columns:1fr 1fr;gap:16px 20px}label{display:block;margin:12px 0;font-size:12px}input:not([type=checkbox]),select{display:block;width:100%;box-sizing:border-box;min-width:0;margin-top:8px}.check{display:flex;gap:10px;align-items:center}.check input{accent-color:#008786;width:16px;height:16px;flex-shrink:0}.toolbar{justify-content:flex-start;margin:12px 0}.toolbar span,footer>span{color:var(--muted,#708a94);font-size:12px}.selection{border:1px solid var(--border,#dce7e9);padding:14px;border-radius:8px;margin:18px 0}.account-list{max-height:320px;overflow:auto;display:grid;grid-template-columns:1fr 1fr;gap:8px}.account{padding:10px;border:1px solid var(--border,#dce7e9);border-radius:6px;margin:0;overflow-wrap:anywhere;min-width:0}.account span{min-width:0}small{display:block;color:var(--muted,#708a94);margin-top:6px}footer{border-top:1px solid var(--border,#dce7e9);padding-top:12px}summary{cursor:pointer;padding:8px 0}.table-wrap{overflow:auto}table{width:100%;border-collapse:collapse;min-width:620px}th,td{padding:10px;border-bottom:1px solid var(--border,#dce7e9);text-align:left;max-width:300px;overflow-wrap:anywhere}th{font-size:12px}:focus-visible{outline:2px solid #008786;outline-offset:2px}:global([data-theme=dark]) .fallback-panel,:global([data-theme=dark]) .fallback-panel :is(button,input,select){background:#192e38}:global([data-theme=dark]) .fallback-panel .primary{background:#007b78}:global([data-theme=dark]) .fallback-panel :is(.badge,.alert){background:#193b38;color:#b4e4d9}:global([data-theme=dark]) .fallback-panel .warn{background:#3c3222;color:#efd3a1}:global([data-theme=dark]) .fallback-panel .error{background:#412a2b;color:#f3b4af}@media(max-width:650px){.fields,.account-list{grid-template-columns:1fr}.fallback-panel{padding:15px}}
</style>
