<script setup>
import {ref,reactive,computed,onMounted,onUnmounted} from 'vue'
import {api} from '../api'
import {createLatestRequest} from '../latestRequest'
defineProps({settings:Boolean})
const state=ref(null),form=reactive({account_ids:[]}),password=ref(''),groups=ref([]),accounts=ref([]),search=ref(''),busy=ref(false),error=ref(''),notice=ref(''),dirty=ref(false),loaded=ref(false)
const request=createLatestRequest(),clock=ref(Date.now()),clockOffset=ref(0)
let timer,disposed=false,revision=0
const filtered=computed(()=>accounts.value.filter(a=>(a.name+' '+a.id+' '+a.type).toLowerCase().includes(search.value.toLowerCase())))
const phases={active:'目标：开启兜底',off:'目标：关闭兜底',disabled:'兜底未启用',unknown:'保持当前调度'}
const fmt=t=>t&&!String(t).startsWith('0001')?new Date(t).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'尚未核验'
const stale=computed(()=>!state.value?.checked_at||String(state.value.checked_at).startsWith('0001')||clock.value+clockOffset.value-Date.parse(state.value.checked_at)>1000*(Number(state.value?.config?.check_seconds||10)+50))
const unresolved=computed(()=>state.value?.accounts?.filter(a=>a.error||a.observed==null||a.observed!==a.desired)||[])
const pending=computed(()=>state.value?.config?.enabled?state.value.config.account_ids.filter(id=>!state.value.accounts.some(a=>a.id===id&&!a.old_connection)).length:0)
async function refresh(initial=false,background=false){
 if(disposed||(background&&busy.value))return
 const version=revision
 try{await request.run(signal=>api('/api/pro-restock/fallback',{signal}),data=>{if(disposed||version!==revision)return;state.value=data;clock.value=Date.now();const at=Date.parse(data.server_time);if(Number.isFinite(at))clockOffset.value=at-clock.value;if(initial){Object.assign(form,data.config);form.account_ids=[...(data.config.account_ids||[])];dirty.value=false;password.value=''}},{background})}catch(e){if(!disposed&&version===revision)error.value=e.message}
}
async function action(fn){if(busy.value||disposed)return;busy.value=true;revision++;error.value='';notice.value='';try{await fn()}catch(e){if(!disposed)error.value=e.message}finally{busy.value=false}}
function input(preview=false){return {...form,enabled:preview?false:form.enabled,password:password.value,account_ids:[...form.account_ids]}}
function resetConnection(){groups.value=[];resetGroup();form.group_id=0;password.value='';dirty.value=true}
function resetGroup(){accounts.value=[];form.account_ids=[];loaded.value=false;dirty.value=true}
async function loadGroups(){await action(async()=>{const data=await api('/api/pro-restock/fallback/groups',{method:'POST',body:input(true)});if(disposed)return;groups.value=data;notice.value='连接成功，已读取 OpenAI 分组。'})}
async function loadAccounts(){await action(async()=>{const data=await api('/api/pro-restock/fallback/accounts',{method:'POST',body:input(true)});if(disposed)return;accounts.value=data;loaded.value=true;notice.value='已读取 '+data.length+' 个 OpenAI API Key / OAuth 账号。读取列表不会切换调度。'})}
function selectVisible(){form.account_ids=[...new Set([...form.account_ids,...filtered.value.map(a=>a.id)])];dirty.value=true}
function remove(id){form.account_ids=form.account_ids.filter(value=>value!==id);dirty.value=true}
async function save(){await action(async()=>{
 const c=await api('/api/pro-restock/fallback',{method:'PUT',body:input()});if(disposed)return
 Object.assign(form,c);form.account_ids=[...(c.account_ids||[])];password.value='';dirty.value=false;await refresh();notice.value='兜底配置已保存。后台按主监控分组账号数量独立核验调度。'
})}
async function retry(){await action(async()=>{try{const data=await api('/api/pro-restock/fallback/retry',{method:'POST'});if(!disposed){state.value=data;notice.value='核验完成。'}}finally{await refresh()}})}
async function reload(){await action(async()=>{await refresh(true);groups.value=[];accounts.value=[];loaded.value=false})}
onMounted(async()=>{await refresh(true);if(!disposed)timer=setInterval(()=>{clock.value=Date.now();refresh(false,true)},5000)})
onUnmounted(()=>{disposed=true;revision++;clearInterval(timer);request.dispose()})
</script>
<template>
<article class="fallback-panel" aria-label="空组兜底调度">
 <header><div><h2>空组兜底调度</h2><p>主监控分组为空时开启兜底，有账号时关闭 · 全天独立运行</p></div><span class="badge">{{state?phases[state.plan.phase]:'读取状态中…'}}</span></header>
 <p v-if="error" class="alert error" role="alert">{{error}}</p><p v-if="notice" class="alert" role="status">{{notice}}</p>
 <template v-if="state">
  <p>{{state.plan.reason}}</p>
  <p v-if="state.monitor">主监控组：{{state.monitor.group_name||'未读取名称'}} #{{state.monitor.group_id||'未设置'}} · {{state.monitor.error||state.plan.hold?'最近成功采集':'当前采集'}}账号数：<b>{{state.monitor.count??'未知'}}</b> · 采集时间：{{fmt(state.monitor.at)}}</p>
  <p v-if="state.monitor?.error" class="alert warn">{{state.monitor.error}}。保留已有调度，失败不会当作空组；后台自动重试，也可立即重试。</p>
  <p v-if="state.error" class="alert error" role="alert">{{state.error}}</p>
  <p v-if="stale&&state.config.enabled" class="alert warn">尚未完成近期核验；上方为计划状态，不能视为账号已完成切换。</p>
  <p v-if="pending" class="alert warn">{{pending}} 个所选账号等待后台首次核验。</p>
  <p v-if="state.config.enabled">已选择 {{state.config.account_ids.length}} 个账号 · 兜底检查周期 {{state.config.check_seconds}} 秒；失败后等待同样秒数重试。</p>
  <p v-if="unresolved.length" class="alert warn">{{unresolved.length}} 个账号尚未确认目标状态或存在异常；后台继续核验。</p>
  <details v-if="state.accounts.length"><summary>查看逐账号调度状态（{{state.accounts.length}}）</summary><div class="table-wrap"><table><thead><tr><th>账号</th><th>目标</th><th>最近实际状态</th><th>核验时间</th><th>说明</th></tr></thead><tbody><tr v-for="(a,index) in state.accounts" :key="index"><td>{{a.name||'账号'}} #{{a.id}}<small v-if="a.old_connection">旧连接收尾</small></td><td>{{a.desired?'开启':'关闭'}}</td><td>{{a.observed==null?'未知':a.observed?'开启':'关闭'}}</td><td>{{fmt(a.checked_at)}}</td><td>{{a.error||'已确认'}}</td></tr></tbody></table></div></details>
  <div class="toolbar"><button type="button" :disabled="busy" @click="retry">立即核验／重试</button><span>核验以已保存配置为准</span></div>
  <form v-if="settings" @submit.prevent="save" @input="dirty=true">
   <fieldset :disabled="busy">
   <label class="check"><input v-model="form.enabled" type="checkbox">启用空组兜底调度</label>
   <p>沿用 Pro 推送的主 Sub2 连接和自动补货选定的监控组，只判断分组实际账号数量。与补货总开关、观察模式、每日禁补时段独立；登录、开通、授权期间仍为空组就继续兜底。关闭此开关、取消选择或更换连接后，原账号调度会收尾关闭。</p>
   <div class="fields">
    <label>兜底 Sub2 地址<input v-model.trim="form.url" type="url" placeholder="https://sub2.example.com" :required="form.enabled" @change="resetConnection"></label>
    <label>管理员邮箱<input v-model.trim="form.email" type="email" autocomplete="off" :required="form.enabled" @change="resetConnection"></label>
    <label>管理员密码<input v-model="password" type="password" autocomplete="new-password" :placeholder="form.password_present?'已加密保存，留空保留':'请输入管理员密码'"></label>
    <label>兜底分组<select v-model.number="form.group_id" @change="resetGroup"><option :value="0">请选择一个分组</option><option v-if="form.group_id&&!groups.some(g=>g.id===form.group_id)" :value="form.group_id">已保存分组 #{{form.group_id}}</option><option v-for="g in groups" :key="g.id" :value="g.id">{{g.name}} · #{{g.id}}</option></select></label>
   </div>
   <div class="toolbar"><button type="button" :disabled="busy" @click="loadGroups">连接检查／读取分组</button><button type="button" :disabled="busy||!form.group_id" @click="loadAccounts">读取／刷新兜底账号</button></div>
   <p>管理员邮箱和密码用于连接这套 Sub2；下方 API Key / OAuth 指账号类型，不需填写账号密钥。</p>
   <div class="selection"><div class="toolbar"><b>多选兜底账号 · 已选 {{form.account_ids.length}}</b><button type="button" :disabled="busy||!loaded" @click="selectVisible">选中搜索结果</button><button type="button" :disabled="busy" @click="form.account_ids=[];dirty=true">清空选择</button></div>
    <label>搜索账号<input v-model="search" type="search" placeholder="账号名称、ID 或类型"></label>
    <div class="account-list"><label v-for="a in filtered" :key="a.id" class="account check"><input v-model="form.account_ids" type="checkbox" :value="a.id"><span><b>{{a.name||'未命名'}} #{{a.id}}</b><small>{{a.type==='apikey'?'API Key':'OAuth'}} · {{a.status}} · 读取时调度{{a.schedulable?'开启':'关闭'}}</small></span></label></div>
    <p v-if="!filtered.length">{{loaded?'没有匹配的 OpenAI API Key / OAuth 账号':'读取账号列表后勾选；已保存的选择仍保留。'}}</p>
    <p v-for="id in form.account_ids.filter(id=>!accounts.some(a=>a.id===id))" :key="id">已选 #{{id}} · {{loaded?'列表中未找到，请取消或重新检查分组':'尚未读取详情'}} <button type="button" @click="remove(id)">取消选择</button></p>
   </div>
   <div class="fields"><label>兜底检查／失败重试周期（秒）<input v-model.number="form.check_seconds" type="number" min="2" max="300" step="1" required></label></div>
   <p>默认 3 秒，与“运行节奏”里的补货检查周期分开。发现空组立即开启所选兜底，发现账号已入组立即关闭，不等待整批流程完成或额度刷新。接口耗时和 Sub2 调度缓存也会影响切换速度，轮询无法保证绝对无间隙。组内存在禁用或无额度账号仍算有账号。</p>
   <footer><span>{{dirty?'有未保存的兜底配置':'兜底配置单独保存'}} · v{{form.version}}</span><div class="toolbar"><button type="button" :disabled="busy" @click="reload">重新载入</button><button class="primary" :disabled="busy" type="submit">{{busy?'处理中…':'保存兜底配置'}}</button></div></footer>
   </fieldset>
  </form>
 </template>
</article>
</template>
<style scoped>
fieldset{border:0;padding:0;margin:0;min-width:0}
.fallback-panel{background:#fff;border:1px solid var(--border,#dce7e9);border-radius:11px;padding:21px;margin-bottom:18px;color:var(--ink,#234752);font-size:13px}header,footer,.toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}h2{font-size:15px;margin:0 0 8px}p{color:var(--muted,#708a94);line-height:1.8;margin:8px 0}button,input,select{font:inherit;border:1px solid var(--border,#dce7e9);border-radius:6px;background:white;color:inherit;padding:9px 12px}button{cursor:pointer}button:disabled{opacity:.5;cursor:default}.primary{background:#008786;color:white;border-color:#008786}.badge,.alert{background:#edf7f5;color:#31736e;border-radius:6px;padding:9px 12px}.warn{background:#fff8ec;color:#976f31}.error{background:#fff1f0;color:#a45148}.fields{display:grid;grid-template-columns:1fr 1fr;gap:16px 20px}label{display:block;margin:12px 0;font-size:12px}input:not([type=checkbox]),select{display:block;width:100%;box-sizing:border-box;min-width:0;margin-top:8px}.check{display:flex;gap:10px;align-items:center}.check input{accent-color:#008786;width:16px;height:16px;flex-shrink:0}.toolbar{justify-content:flex-start;margin:12px 0}.toolbar span,footer>span{color:var(--muted,#708a94);font-size:12px}.selection{border:1px solid var(--border,#dce7e9);padding:14px;border-radius:8px;margin:18px 0}.account-list{max-height:320px;overflow:auto;display:grid;grid-template-columns:1fr 1fr;gap:8px}.account{padding:10px;border:1px solid var(--border,#dce7e9);border-radius:6px;margin:0;overflow-wrap:anywhere;min-width:0}.account span{min-width:0}small{display:block;color:var(--muted,#708a94);margin-top:6px}footer{border-top:1px solid var(--border,#dce7e9);padding-top:12px}summary{cursor:pointer;padding:8px 0}.table-wrap{overflow:auto}table{width:100%;border-collapse:collapse;min-width:620px}th,td{padding:10px;border-bottom:1px solid var(--border,#dce7e9);text-align:left;max-width:300px;overflow-wrap:anywhere}th{font-size:12px}:focus-visible{outline:2px solid #008786;outline-offset:2px}:global([data-theme=dark]) .fallback-panel,:global([data-theme=dark]) .fallback-panel :is(button,input,select){background:#192e38}:global([data-theme=dark]) .fallback-panel .primary{background:#007b78}:global([data-theme=dark]) .fallback-panel :is(.badge,.alert){background:#193b38;color:#b4e4d9}:global([data-theme=dark]) .fallback-panel .warn{background:#3c3222;color:#efd3a1}:global([data-theme=dark]) .fallback-panel .error{background:#412a2b;color:#f3b4af}@media(max-width:650px){.fields,.account-list{grid-template-columns:1fr}.fallback-panel{padding:15px}}
</style>
