<script setup>
import { onMounted, onBeforeUnmount, reactive, ref } from 'vue'
import { Bell, Plus, Trash2, Send, RefreshCw } from 'lucide-vue-next'
import { api } from '../api'
import { formatTime } from '../utils'
import MessageBar from './MessageBar.vue'

const config=reactive({enabled:false,restock:true,merge_start:true,merge_result:true,bots:[]})
const eventOptions=[
 {value:'restock',label:'自动补货',hint:'实际安排补货时'},
 {value:'pro_start',label:'开始开通',hint:'登录完成，提交开通订单时'},
 {value:'pro_pushed',label:'推送完成',hint:'开通后成功推送时'},
 {value:'pro_failed',label:'全自动失败',hint:'登录、开通、授权或推送失败时'},
 {value:'merge_start',label:'触发合并',hint:'账号进入合并流程时'},
 {value:'merge_result',label:'合并结果',hint:'成功移出，或四步中任一步失败时'},
 {value:'ejh_open',label:'EJH 开卡',hint:'开卡成功、失败或结果待核实时'},
]
const ready=ref(false),busy=ref(false),testing=ref(''),logs=ref([]),message=reactive({text:'',type:'success'})
let disposed=false,timer
function show(text,type='success'){message.text=text;message.type=type}
async function load(){
 try{const result=await api('/api/notifications');if(disposed)return;Object.assign(config,result.settings);logs.value=result.logs;ready.value=true}
 catch(e){show(e.message,'error')}
}
async function readLogs(){try{const result=await api('/api/notifications');if(!disposed)logs.value=result.logs}catch(e){if(!disposed)show(e.message,'error')}}
function addBot(){config.bots.push({id:crypto.randomUUID(),name:'',enabled:true,webhook:'',secret:'',clear_secret:false,events:eventOptions.map(event=>event.value)})}
async function save(){
 busy.value=true
 try{const result=await api('/api/notifications',{method:'PUT',body:config});Object.assign(config,result);show('通知配置已保存')}
 catch(e){show(e.message,'error')}finally{busy.value=false}
}
async function test(bot){
 testing.value=bot.id;show('正在发送测试通知…','info')
 try{await api('/api/notifications/test',{method:'POST',body:bot});show('测试通知已发送，请查看飞书群')}
 catch(e){show(e.message,'error')}finally{testing.value='';await readLogs()}
}
onMounted(async()=>{await load();if(!disposed)timer=setInterval(readLogs,15000)})
onBeforeUnmount(()=>{disposed=true;clearInterval(timer)})
</script>

<template>
 <div class="notifications-page">
  <header class="notification-heading"><div><h1><Bell :size="24"/>通知</h1><p>每个机器人独立选择补货、开通和合并通知。</p></div><button class="btn primary" :disabled="!ready||busy||!!testing" @click="save">{{busy?'保存中…':'保存配置'}}</button></header>
  <MessageBar :message="message"/>
  <p v-if="!ready" class="muted">{{message.text?'通知配置读取失败，请刷新页面重试。':'正在读取通知配置…'}}</p>
  <template v-else>
   <section class="notification-panel"><header><h2>通知总开关</h2><label class="check"><input v-model="config.enabled" type="checkbox" :disabled="busy||!!testing"/>启用通知</label></header></section>
   <section class="notification-panel"><header><div><h2>飞书机器人 <small>{{config.bots.length}} 个</small></h2><p>勾选各机器人接收的通知，保存后生效。</p></div><button class="btn ghost" :disabled="busy||!!testing||config.bots.length>=20" @click="addBot"><Plus :size="15"/>添加机器人</button></header>
    <div class="bot-grid"><article v-for="(bot,index) in config.bots" :key="bot.id" class="bot-card">
     <div class="bot-heading"><label class="check"><input v-model="bot.enabled" type="checkbox" :disabled="busy||!!testing"/>启用此机器人</label><button class="icon-button" :disabled="busy||!!testing" aria-label="删除机器人" @click="config.bots.splice(index,1)"><Trash2 :size="16"/></button></div>
     <label>机器人名称<input v-model="bot.name" maxlength="60" placeholder="例如：Space 运维通知" :disabled="busy||!!testing"/></label>
     <label>Webhook 地址<input v-model="bot.webhook" type="password" autocomplete="new-password" :placeholder="bot.webhook_present?'已保存，留空不修改':'https://open.feishu.cn/open-apis/bot/v2/hook/…'" :disabled="busy||!!testing"/></label>
     <label>签名密钥（Secret）<input v-model="bot.secret" type="password" autocomplete="new-password" :placeholder="bot.secret_present?'已保存，留空不修改':'可选：机器人开启签名校验时填写'" :disabled="busy||!!testing||bot.clear_secret"/></label>
     <label v-if="bot.secret_present" class="check"><input v-model="bot.clear_secret" type="checkbox" :disabled="busy||!!testing"/>清除已保存签名密钥</label>
     <fieldset class="bot-events" :disabled="busy||!!testing"><legend>接收通知 <small>{{bot.events?.length||0}} / {{eventOptions.length}}</small></legend>
      <div class="notification-events"><label v-for="event in eventOptions" :key="event.value"><input v-model="bot.events" type="checkbox" :value="event.value"/><span>{{event.label}}<small>{{event.hint}}</small></span></label></div>
      <p v-if="!bot.events?.length" class="panel-note">未选择通知，此机器人不会接收业务通知。</p>
     </fieldset>
     <button class="btn ghost" :disabled="busy||!!testing||!bot.name||(!bot.webhook&&!bot.webhook_present)" @click="test(bot)"><Send :size="14"/>{{testing===bot.id?'发送中…':'发送测试通知'}}</button>
    </article></div>
    <p v-if="!config.bots.length" class="empty">还没有机器人，点击“添加机器人”开始配置。</p>
    <p class="panel-note">原机器人保留原有通知勾选，可自行开启新增类型。测试通知立即发送到对应飞书群；通知失败不影响业务。</p>
   </section>
   <section class="notification-panel"><header><h2>最近发送 <small>最近 50 条</small></h2><button class="btn ghost" @click="readLogs"><RefreshCw :size="14"/>刷新</button></header><div class="log-table"><table><thead><tr><th>时间</th><th>机器人</th><th>通知</th><th>结果</th></tr></thead><tbody><tr v-for="log in logs" :key="log.id"><td>{{formatTime(log.at)}}</td><td>{{log.bot}}</td><td>{{log.title}}</td><td><span :class="log.status==='sent'?'sent':'failed'">{{log.status==='sent'?'已发送':'发送失败'}}</span><small v-if="log.error" class="log-error">{{log.error}}</small></td></tr><tr v-if="!logs.length"><td colspan="4" class="empty">暂无发送记录</td></tr></tbody></table></div></section>
  </template>
 </div>
</template>

<style scoped>
.notifications-page{display:grid;gap:20px;max-width:1250px}.notification-heading,.notification-panel>header,.bot-heading{display:flex;align-items:center;justify-content:space-between;gap:16px}.notification-heading h1{display:flex;align-items:center;gap:10px;font-size:25px;margin:0 0 8px}.notification-heading p,.notification-panel p,.muted{color:var(--muted);font-size:12px}.notification-panel{background:var(--surface);border:1px solid var(--line);border-radius:12px;padding:22px}.notification-panel h2{font-size:15px;margin:0}.notification-panel h2 small{font-size:11px;color:var(--muted);font-weight:400;margin-left:8px}.bot-events{border:0;border-top:1px solid var(--line);padding:14px 0 0;margin:0;min-width:0}.bot-events legend{font-size:12px;padding:0 8px 0 0}.bot-events legend small{color:var(--muted);margin-left:6px}.notification-events{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}.notification-events>label{display:flex;align-items:flex-start;gap:9px;font-size:13px}.notification-events small{display:block;line-height:1.7;color:var(--muted);font-size:11px;margin-top:4px}.check{display:flex!important;align-items:center;gap:8px;font-size:12px!important}.check input,.notification-events input{width:auto;accent-color:var(--green-strong)}.bot-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:18px;margin-top:18px}.bot-card{display:grid;align-content:start;min-width:0;gap:16px;padding:18px;border:1px solid var(--line);border-radius:10px}.bot-card>label{display:grid;gap:8px;font-size:12px}.bot-card input:not([type=checkbox]){width:100%;border:1px solid var(--line);border-radius:7px;padding:10px;background:var(--bg);color:var(--text);min-width:0}.btn{display:inline-flex;align-items:center;justify-content:center;gap:7px}.bot-card>.btn{justify-self:start}.panel-note{margin:20px 0 0;line-height:1.7}.empty{text-align:center;color:var(--muted);padding:24px}.log-table{overflow:auto;margin-top:18px}table{width:100%;border-collapse:collapse;text-align:left;font-size:12px}th{color:var(--muted);font-weight:400;background:var(--bg)}th,td{padding:12px;border-bottom:1px solid var(--line);white-space:nowrap}.sent{color:var(--green-strong)}.failed{color:var(--red)}.log-error{display:block;max-width:400px;white-space:normal;color:var(--muted);margin-top:5px;line-height:1.6}@media(max-width:760px){.notification-events,.bot-grid{grid-template-columns:1fr}.notification-panel{padding:16px}.notification-heading{align-items:flex-start}.notification-panel>header{flex-wrap:wrap}}
</style>
