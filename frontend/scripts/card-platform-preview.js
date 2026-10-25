import {createApp,h,ref} from 'vue'
import EJHManagementView from '../src/components/EJHManagementView.vue'
import NexovccManagementView from '../src/components/NexovccManagementView.vue'
import '../src/styles.css'
const at=new Date().toISOString()
const cards=['oai','cc'].map((platform,i)=>({id:platform,platform,template_name:platform.toUpperCase()+' test template',normal_pending:false,issuer_linked:true,created_at:at,balance:'20.00',balance_at:at,remote:{orderNo:platform,cardNo:'**** '+(1111+i),cardCurrency:'USD',status:'1',validityDate:'1299'},local:{name:platform.toUpperCase()+' test card',available:true,opened_accounts:0,account_limit:3}}))
const fixture=window.cardFixture={requests:[],cards}
const reply=data=>new Response(JSON.stringify({ok:true,data}),{headers:{'Content-Type':'application/json'}})
window.fetch=async(path,options={})=>{
 const u=new URL(path,location.origin),body=options.body?JSON.parse(options.body):null
 if(u.origin!==location.origin||!u.pathname.startsWith('/api/'))throw Error('External request prohibited')
 fixture.requests.push({path:u.pathname,body,method:options.method||'GET'})
 if(u.pathname.endsWith('/settings'))return reply({enabled:false,templates:['oai','cc'].map(platform=>({id:platform,platform,name:platform.toUpperCase()+' template',valid_days:30,cardCurrency:'USD',plan_code:'pro20'}))})
 if(u.pathname==='/api/ejh/dashboard')return reply({cards,cases:[],trades:[],operations:[],audit:[],sync:{},reversal_fee_percent:6})
 if(u.pathname==='/api/nexovcc/dashboard')return reply({cards:cards.map(c=>({card:{id:c.id,serial_no:c.id,platform:c.platform,template_name:c.template_name,normal_pending:c.normal_pending,status:'1',balance:'20.00',balance_at:at,last4:c.remote.cardNo,created_at:at},local:c.local})),cases:[],operations:[],groups:[],finance:{at},balance:{amount:'20.00'},products:[],sync:{},withdraw:{},control:{paused:false}})
 if(u.pathname.endsWith('/category')){const id=u.pathname.split('/').at(-2);const c=cards.find(c=>c.id===id);c.normal_pending=body.category==='normal_pending';return reply(c)}
 if(u.pathname.endsWith('/action'))return reply({synced:true})
 if(u.pathname==='/api/nexovcc/job')return reply({status:'idle'})
 throw Error('Unmocked '+u.pathname)
}
const page=ref('ejh')
createApp({setup:()=>()=>h('div',[h('button',{onClick:()=>page.value='ejh'},'测试 EJH'),h('button',{onClick:()=>page.value='nex'},'测试环球'),page.value==='ejh'?h(EJHManagementView):h(NexovccManagementView)])}).mount('#app')
