import {createApp,h,ref} from 'vue'
import EJHManagementView from '../src/components/EJHManagementView.vue'
import EJHReversalFee from '../src/components/EJHReversalFee.vue'
import '../src/styles.css'
const rate=ref(6),at=new Date().toISOString()
const make=(id,locked)=>({id,card_id:'fixture',confirmed:locked,trade:{orderNo:'fixture',tradeNo:id,tradeType:'2',tradeStatus:'1',cardAmount:'100',cardCurrency:'USD'},...(locked?{reversal_fee:{percent:6,locked_at:at}}:{})})
const trades=[make('completed',true),make('waiting',false)]
const fixture=window.feeFixture={requests:[],trades}
let settings={enabled:false,url:'https://www.ejhcard.com',app_key:'',default_max_accounts:3,single_active_account:true,sync_interval_seconds:300,page_refresh_seconds:30,alert_hours:24,reversal_fee_percent:6,templates:[]}
const reply=data=>new Response(JSON.stringify({ok:true,data}),{headers:{'Content-Type':'application/json'}})
window.fetch=async(path,options={})=>{
 const u=new URL(path,location.origin),body=options.body?JSON.parse(options.body):null
 if(u.origin!==location.origin||!u.pathname.startsWith('/api/'))throw Error('External request prohibited')
 fixture.requests.push({path:u.pathname,body,method:options.method||'GET'})
 if(u.pathname==='/api/ejh/settings'){
  if(options.method==='PUT'){settings={...body};rate.value=settings.reversal_fee_percent}
  return reply(settings)
 }
 if(u.pathname==='/api/ejh/dashboard')return reply({cards:[],cases:[],trades,operations:[],audit:[],sync:{},reversal_fee_percent:settings.reversal_fee_percent})
 throw Error('Unmocked '+u.pathname)
}
createApp({setup:()=>()=>h('div',[
 h('div',{id:'fee-completed'},[h('h2','已回款示例'),h(EJHReversalFee,{records:[trades[0]],feePercent:rate.value})]),
 h('div',{id:'fee-waiting'},[h('h2','待回款示例'),h(EJHReversalFee,{records:[trades[1]],feePercent:rate.value})]),
 h('div',{id:'fee-mixed'},[h('h2','合计示例'),h(EJHReversalFee,{records:trades,feePercent:rate.value})]),h(EJHManagementView)
])}).mount('#app')
