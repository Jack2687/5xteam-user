import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, reactive, ref } from 'vue'

for (const file of ['ProRechargeDialog.vue', 'ProAutoDialog.vue']) {
 const source = readFileSync(new URL('../src/components/' + file, import.meta.url), 'utf8')
 const parsed = ts.createSourceFile(file, source.split('<script setup>')[1].split('</script>')[0], ts.ScriptTarget.Latest, true, ts.ScriptKind.JS)
 const methods = parsed.statements.filter(s => ts.isFunctionDeclaration(s) && ['loadCards','open','start','submit'].includes(s.name?.text)).map(s => s.getText(parsed)).join('\n')
 const providerState = parsed.statements.filter(s => ts.isVariableStatement(s) && s.declarationList.declarations.some(d => ['autoCardEnabled','autoCardLabel'].includes(d.name.getText(parsed)))).map(s => s.getText(parsed)).join('\n')
 for (const provider of ['ejh','nexovcc']) for (const auto of [false,true]) for (const available of [false,true]) {
  const calls=[]
  const settings=file==='ProAutoDialog.vue'?ref({}):reactive({})
  const ctx=vm.createContext({computed,props:{defaultPageSize:50},blockedCards:ref([]),occupancyMessage:ref(''),settings,cardID:ref(''),cards:ref([]),total:ref(0),page:ref(1),pageSize:ref(10),busy:ref(false),email:ref(''),order:ref(null),error:ref(''),opened:ref(false),state:ref({}),requestID:'',timer:0,generation:0,clearTimeout(){},schedule(){},emit(){},path:()=>'/auto-pro',payRequestID:()=> 'local-request',api:async(path,options)=>{
   calls.push({path,options})
   if(options) return {pro_auto:{status:'running'}}
   if(path==='/api/pro-settings')return {card_provider:provider,ejh_auto_open:provider==='ejh'?auto:!auto,nexovcc_auto_open:provider==='nexovcc'?auto:!auto}
   if(path==='/api/gptpay/settings')return {provider:'tokenseek',plan_code:'pro5',key_present:true}
   if(path.startsWith('/api/gptpay/cards')){
    assert.equal(new URL(path,'http://local').searchParams.get('card_provider'),'configured',file+' must filter the issuer before pagination')
    return {items:available?[{id:'existing'}]:[],total:available?1:0}
   }
   if(path.startsWith('/api/gptpay/orders'))return {items:[]}
   return {}
  }})
  vm.runInContext(providerState+'\n'+methods+'\nthis.run={open,loadCards,autoCardLabel,'+(file==='ProAutoDialog.vue'?'start':'submit')+'}',ctx)
  await ctx.run.open({email:'local@example.com'})
  assert.equal(ctx.run.autoCardLabel.value,provider==='nexovcc'?'环球':'EJH')
  assert.equal(ctx.cardID.value,auto?'':available?'existing':'')
  assert.ok(calls.some(c=>c.path==='/api/pro-settings'))
  await (ctx.run.start||ctx.run.submit)(false)
  assert.equal(calls.find(c=>c.options)?.options.body.card_id,auto?'':available?'existing':'')
  await ctx.run.loadCards()
  assert.equal(ctx.cardID.value,auto?'':available?'existing':'')
 }
 assert.ok(source.includes('无可用卡时从 ${autoCardLabel} 补卡'))
 if(file==='ProAutoDialog.vue') {
  const names=['active','resumable','retryCurrent','restartable','canStart']
  const state=ref({restock_retry_available:true,steps:{}})
  const ctx=vm.createContext({computed,state})
  const flags=parsed.statements.filter(s=>ts.isVariableStatement(s)&&s.declarationList.declarations.some(d=>names.includes(d.name.getText(parsed)))).map(s=>s.getText(parsed)).join('\n')
  vm.runInContext(flags+'\nthis.flags={retryCurrent,restartable,canStart}',ctx)
  assert.equal(ctx.flags.retryCurrent.value,true,'pre-workflow failure needs explicit retry')
  assert.equal(ctx.flags.restartable.value,true)
  assert.equal(ctx.flags.canStart.value,false,'claimed account must not appear as a new start')
  state.value.status='running'
  assert.equal(ctx.flags.retryCurrent.value,false)
  state.value={steps:{}}
  assert.equal(ctx.flags.retryCurrent.value,false)
  assert.equal(ctx.flags.canStart.value,true)
 }
}
const ejh=readFileSync(new URL('../src/components/EJHManagementView.vue',import.meta.url),'utf8')
for(const [code,label] of [['ecom','跨境电商'],['flight','机票'],['hotel','酒店'],['trv','非酒店机票']]) assert.ok(ejh.includes('<option value="'+code+'">'+code+' · '+label+'</option>'))
console.log('Passed: EJH/Nexovcc manual/auto selection, independent auto-open settings, pagination, request card IDs and official MCC labels')
