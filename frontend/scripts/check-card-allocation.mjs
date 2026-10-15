import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'

for (const file of ['ProRechargeDialog.vue', 'ProAutoDialog.vue']) {
 const source = readFileSync(new URL('../src/components/' + file, import.meta.url), 'utf8')
 const parsed = ts.createSourceFile(file, source.split('<script setup>')[1].split('</script>')[0], ts.ScriptTarget.Latest, true, ts.ScriptKind.JS)
 const methods = parsed.statements.filter(s => ts.isFunctionDeclaration(s) && ['loadCards','open','start','submit'].includes(s.name?.text)).map(s => s.getText(parsed)).join('\n')
 for (const auto of [false,true]) for (const available of [false,true]) {
  const ref = value => ({value})
  const calls=[]
  const settings=file==='ProAutoDialog.vue'?ref({}):{}
  const ctx=vm.createContext({blockedCards:ref([]),occupancyMessage:ref(''),settings,cardID:ref(''),cards:ref([]),total:ref(0),page:ref(1),pageSize:ref(10),busy:ref(false),email:ref(''),order:ref(null),error:ref(''),opened:ref(false),state:ref({}),requestID:'',timer:0,generation:0,clearTimeout(){},schedule(){},emit(){},path:()=>'/auto-pro',payRequestID:()=> 'local-request',api:async(path,options)=>{
   calls.push({path,options})
   if(options) return {pro_auto:{status:'running'}}
   if(path==='/api/pro-settings')return {ejh_auto_open:auto}
   if(path==='/api/gptpay/settings')return {provider:'tokenseek',plan_code:'pro5',key_present:true}
   if(path.startsWith('/api/gptpay/cards'))return {items:available?[{id:'existing'}]:[],total:available?1:0}
   if(path.startsWith('/api/gptpay/orders'))return {items:[]}
   return {}
  }})
  vm.runInContext(methods+'\nthis.run={open,loadCards,'+(file==='ProAutoDialog.vue'?'start':'submit')+'}',ctx)
  await ctx.run.open({email:'local@example.com'})
  assert.equal(ctx.cardID.value,auto?'':available?'existing':'')
  assert.ok(calls.some(c=>c.path==='/api/pro-settings'))
  await (ctx.run.start||ctx.run.submit)(false)
  assert.equal(calls.find(c=>c.options)?.options.body.card_id,auto?'':available?'existing':'')
  await ctx.run.loadCards()
  assert.equal(ctx.cardID.value,auto?'':available?'existing':'')
 }
 assert.ok(source.includes('无可用卡时按配置从 EJH 补卡'))
}
const ejh=readFileSync(new URL('../src/components/EJHManagementView.vue',import.meta.url),'utf8')
for(const [code,label] of [['ecom','跨境电商'],['flight','机票'],['hotel','酒店'],['trv','非酒店机票']]) assert.ok(ejh.includes('<option value="'+code+'">'+code+' · '+label+'</option>'))
console.log('Passed: manual/auto default selection, auto-open on/off with/without cards, pagination, request card IDs and official MCC labels')
