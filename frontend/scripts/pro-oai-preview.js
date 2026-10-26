import {createApp,h,ref} from 'vue'
import ProGPTPayPanel from '../src/components/ProGPTPayPanel.vue'
import ProManagementView from '../src/components/ProManagementView.vue'
import EJHManagementView from '../src/components/EJHManagementView.vue'
import EJHAccountReturnSummary from '../src/components/EJHAccountReturnSummary.vue'
import NexovccCaseTable from '../src/components/NexovccCaseTable.vue'
import NexovccProCasesDialog from '../src/components/NexovccProCasesDialog.vue'
import {normalizeOAISettings} from '../src/pro-disposition'
import '../src/styles.css'

const now=new Date().toISOString(), later=new Date(Date.now()+300000).toISOString()
const completed={status:'completed',steps:{invite:'completed',accept:'completed',login:'completed',transfer:'completed',submit:'completed',remove:'completed',groups:'completed',confirm:'completed'},started_at:now,submitted_at:now,accepted_at:now,completed_at:now,confirmed_at:now}
const fixture=window.oaiFixture={requests:[],settings:{provider:'sub2',sub2:{group_ids:[],models:[]},cpa:{},vm2api:{},auto_merge_enabled:false,auto_delete_enabled:false,auto_merchant_enabled:false,auto_merge_v2_enabled:true,merchant_key_present:true,deletion_check_interval_seconds:300,quota_used_threshold:90,quota_check_interval_seconds:120,merge_concurrency:2,concurrency:2,retry_count:2,retry_interval_seconds:3,target_seat_type:'default',target_admin_id:'fixture',max_unmerged:5,schedule_interval_seconds:120,purchase_windows:[],ejh_plan_templates:{},nexovcc_plan_templates:{},oai_confirmation:normalizeOAISettings()},pay:{provider:'tokenseek',url:'https://supplier.invalid',plan_code:'pro5',country:'US',key_present:true,session_timeout_minutes:30,card_account_limit:3,refresh_interval_seconds:60}}
fixture.accounts=['merge','deactivate','merchant','merge_v2'].map((mode,i)=>({email:`${mode}@example.com`,management_scope:'pro',updated_at:now,space_merged_once:mode==='merge',pro_invite_status:'completed',pro_accept_status:'completed',pro_transfer_status:mode==='merge'?'completed':'',pro_remove_status:mode==='merge'?'completed':'',pro_auto:{id:mode,status:'completed',stage:mode},pro_stage_progress:{steps:{login:'completed',recharge:'completed',oauth:'completed',push:'completed',quota:'completed',[mode]:'completed'}},...(mode==='merge_v2'?{pro_merge_v2:completed}:mode==='merchant'?{pro_merchant:{...completed,request_id:'fixture-request',retired_at:now}}:mode==='deactivate'?{pro_deactivation:completed}:{}),pro_oai_confirmations:{[mode]:{mode,status:i===0?'confirmed':i===1?'waiting':'disabled',completed_at:now,delay_seconds:300,...(i===0?{confirmed_at:now}:i===1?{not_before:later}:{})}}}))
for (const a of fixture.accounts) { const r=Object.values(a.pro_oai_confirmations)[0];Object.assign(r,{attempts:1,max_attempts:3,steps:Object.fromEntries(['wait','login','authorize','send','reply','confirm'].map(key=>[key,r.status==='confirmed'?'completed':'waiting']))});if(r.status==='confirmed')Object.assign(r,{checked_at:now,answer:'已确认 merge@example.com 的退款已发起，预计 5–10 个工作日。发票 TEST-0001。\n已成功\n<img src=x onerror="window.oaiXSS=true">',replies:[]}) }
fixture.cases=fixture.accounts.map((a,i)=>({id:`order-${i}`,email:a.email,card_id:'card',card_name:'测试卡',last4:'4242',card_type:'ejh',plan:'pro20',provider:'cmsnav',state:'waiting',payment_status:'success',created_at:now,opened_at:now,requested_at:now,returns:[],amounts:{},pro_oai_confirmations:a.pro_oai_confirmations}))
const reply=data=>new Response(JSON.stringify({ok:true,data}),{headers:{'Content-Type':'application/json'}})
window.fetch=async(path,options={})=>{
 const url=new URL(path,location.origin),body=options.body?JSON.parse(options.body):null
 if(url.origin!==location.origin||!url.pathname.startsWith('/api/'))throw Error('External request prohibited in fixture')
 fixture.requests.push({path:url.pathname,query:url.search,method:options.method||'GET',body})
 if(url.pathname.endsWith('/oai-confirmation')){const a=fixture.accounts.find(a=>a.email===decodeURIComponent(url.pathname.split('/')[3]));const r=a.pro_oai_confirmations[body.mode];r.status=body.action==='stop'?'stopped':'waiting';r.next_attempt_at=null;return reply(a)}
 if(url.pathname==='/api/pro-settings/automation'){fixture.settings=structuredClone(body.automation);fixture.pay=body.gptpay;return reply({automation:fixture.settings,gptpay:fixture.pay})}
 if(url.pathname==='/api/pro-settings')return reply(fixture.settings)
 if(url.pathname==='/api/gptpay/settings')return reply(fixture.pay)
 if(url.pathname==='/api/gptpay/account')return reply({plans:[{planCode:'pro5',successCredits:80,availableCountries:['US'],defaultCountry:'US'},{planCode:'pro20',successCredits:100,availableCountries:['US'],defaultCountry:'US'}]})
 if(url.pathname==='/api/gptpay/catalog')return reply({countries:[{code:'US',name:'美国',currency:'USD'}],creditPrices:{pro5x:80,pro20x:100},productAvailability:{pro5x:true,pro20x:true}})
 if(url.pathname==='/api/ejh/settings'||url.pathname==='/api/nexovcc/settings')return reply({enabled:false,templates:[],page_refresh_seconds:30})
 if(url.pathname==='/api/gptpay/card-cases'||url.pathname==='/api/pro-settings/sub2/groups')return reply([])
 if(url.pathname==='/api/pro-accounts'){
  const mode={merged:'merge',deleted:'deactivate',merchant:'merchant',merge_v2:'merge_v2'}[url.searchParams.get('merge_state')]
  const items=fixture.accounts.filter(a=>!mode||a.pro_auto.stage===mode)
  return reply({items,total:items.length,summary:{all:4,in_progress:0,merge_v2:1,merchant:1,deleted:1,merged:1,unmerged:0}})
 }
 if(url.pathname==='/api/ejh/dashboard'||url.pathname==='/api/nexovcc/dashboard')return reply({cards:[],cases:fixture.cases,trades:[],operations:[],audit:[],sync:{},finance:{},groups:[],reversal_fee_percent:6})
 throw Error('Unmocked request '+url.pathname)
}
const page=ref('settings'),version=ref(0),dialog=ref(null)
createApp({setup:()=>()=>h('div',[
 ...['settings','accounts','ejh','global','ejh-dialog'].map(key=>h('button',{onClick:()=>{page.value=key;version.value++}},`预览 ${key}`)),
 h('button',{onClick:()=>dialog.value.open({email:'deactivate@example.com'})},'环球账号回款弹窗'),
 h(NexovccProCasesDialog,{ref:dialog}),
 page.value==='settings'?h(ProGPTPayPanel,{key:version.value,mode:'settings',adminAccounts:[{id:'fixture',email:'admin@example.com',team_account_id:'team'}]}):
 page.value==='accounts'?h(ProManagementView,{key:version.value}):
 page.value==='ejh'?h(EJHManagementView,{key:version.value}):
 page.value==='global'?h(NexovccCaseTable,{rows:fixture.cases}):
 h('section',{class:'modal'},fixture.cases.map(row=>h(EJHAccountReturnSummary,{row})))
])}).mount('#app')
