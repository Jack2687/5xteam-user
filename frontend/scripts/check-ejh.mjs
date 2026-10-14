import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import { returnTypes, operationStates, caseSummary, summarizeCardBalances, returnStatus, returnStatusLabels, cardHasCase, cardMatchesSearch, readCardView, saveCardView, ejhCardEntry } from '../src/ejh.js'
const source=readFileSync(new URL('../src/components/EJHManagementView.vue',import.meta.url),'utf8')
const script=source.split('<script setup>')[1].split('</script>')[0]
const parsed=ts.createSourceFile('ejh.js',script,ts.ScriptTarget.Latest,true,ts.ScriptKind.JS)
const methods=parsed.statements.filter(s=>ts.isFunctionDeclaration(s)&&['action','show','cardAction','openCards'].includes(s.name?.text)).map(s=>s.getText(parsed)).join('\n')
function fixture(result){
 const calls=[],message={text:'',type:''},openForm={count:3,request_id:'',input:{cardAmount:'20',cardCurrency:'USD'}},detail={value:null}
 const ctx=vm.createContext({message,openForm,detail,busy:{value:false},operationStates,window:{confirm:()=>true,prompt:()=> '20'},crypto:{randomUUID:()=> 'fixture-id'},load:async()=>{},api:async(path,options)=>{calls.push({path,options});return result}})
 vm.runInContext(methods+'\nthis.methods={cardAction,openCards}',ctx)
 return {ctx,calls,message,openForm,detail}
}
const card={id:'fixture',remote:{cardCurrency:'USD'}}
for(const action of ['refresh','balance']){const f=fixture({status:'1'});await f.ctx.methods.cardAction(card,action);assert.equal(f.message.type,'success','Issuer status is not an operation failure')}
{const f=fixture({status:'unknown',error:'fixture unknown'});await f.ctx.methods.cardAction(card,'increase');assert.equal(f.message.type,'error');assert.equal(f.calls.length,1)}
{const f=fixture({status:'unknown'});await f.ctx.methods.openCards();assert.equal(f.calls.length,1,'Batch must stop at unknown result');assert.equal(f.openForm.request_id,'fixture-id','Keep request ID after uncertainty')}
{const f=fixture({status:'completed'});await f.ctx.methods.openCards();assert.equal(f.calls.length,3);assert.equal(f.openForm.request_id,'')}
{const f=fixture({cardNo:'fixture'});await f.ctx.methods.cardAction(card,'details');assert.equal(f.detail.value.data.cardNo,'fixture')}
assert.deepEqual([...returnTypes],['2','5','16'])
assert.ok(caseSummary({email:'fixture@example.com',state:'returned'}).includes('已回款'))
assert.ok(!returnTypes.has('17'),'Card closure is not an account refund')
console.log('Passed: EJH refresh, balance, detail, unknown-result stop, batch idempotency and return labels')

const snapshot = (balance, currency='USD', extra={}) => ({balance,balance_at:'2026-10-14T00:00:00Z',remote:{cardCurrency:currency},...extra})
const summary = summarizeCardBalances([snapshot('0.1'),snapshot('0.2'),snapshot('0'),snapshot('-0.05'),snapshot('20','EUR'),snapshot('7','USD',{balance_error:'timeout'}),snapshot('', 'USD', {balance_at:null}),snapshot('bad'),snapshot('10','???')])
assert.deepEqual(summary.totals,[{currency:'EUR',count:1,amount:'20.00'},{currency:'USD',count:5,amount:'7.25'}])
assert.equal(summary.missing,3);assert.equal(summary.failed,1);assert.equal(summary.known,6)
assert.equal(summarizeCardBalances([snapshot('999999999999.999999'),snapshot('0.000001')]).totals[0].amount,'1000000000000.00')
assert.deepEqual(summarizeCardBalances([]).totals,[])
const balanceMethods=parsed.statements.filter(s=>ts.isFunctionDeclaration(s)&&['action','show','load','refreshAllBalances','queryAccountBalance'].includes(s.name?.text)).map(s=>s.getText(parsed)).join('\n')
for(const stop of [false,true]){
 const calls=[],allCards=[{id:'one',remote:{orderNo:'one'}},{id:'two',remote:{orderNo:'two'}}]
 const ctx=vm.createContext({message:{text:'',type:''},busy:{value:false},disposed:false,data:{value:{cards:allCards}},balanceProgress:{value:null},balanceFailures:{value:[]},api:async(path,options)=>{calls.push({path,options});if(path==='/api/ejh/dashboard')return {cards:allCards};if(path.includes('/one/')){if(stop)ctx.disposed=true;throw Error('fixture card failure')}return {}}})
 vm.runInContext(balanceMethods+'\nthis.run=refreshAllBalances;this.load=load',ctx)
 await ctx.load();assert.equal(calls.length,1,'Reading saved snapshots must not query supplier balances')
 await ctx.run()
 assert.equal(calls.filter(c=>c.path==='/api/ejh/balance').length,1)
 assert.equal(calls.filter(c=>c.options?.body?.action==='balance').length,stop?1:2,'Refresh must include all cards, continue on failure, stop when leaving')
 assert.equal(ctx.balanceProgress.value,null);assert.equal(ctx.balanceFailures.value.length,1)
}
console.log('Passed: precise multi-currency balances, negative/zero/missing values, cached failure fallback, manual-only refresh, all-card iteration and navigation cancellation')

const cases=[{card_id:'alias',ejh_card_id:'canonical',email:'User@example.com'},{card_id:'other',email:'user@example.com'}]
assert.equal(cardHasCase({id:'canonical'},cases[0]),true)
assert.equal(cardMatchesSearch({id:'canonical'},cases,' USER@EXAMPLE.COM '),true)
assert.equal(cardMatchesSearch({id:'other'},cases,'user@example.com','canonical'),false)
assert.equal(cardMatchesSearch({id:'unrelated'},cases,'user@example.com'),false)
assert.equal(cardMatchesSearch({id:'canonical',remote:{cardNo:'****4242'}},cases,'4242'),true)
assert.equal(ejhCardEntry({email:'a',activation_card:{card_name:'EJH',card_last4:'4242'}}),null)
assert.deepEqual(ejhCardEntry({email:'a',activation_card:{ejh_card_id:'canonical'}}),{email:'a',cardID:'canonical'})
const storage={values:new Map(),getItem(k){return this.values.get(k)},setItem(k,v){this.values.set(k,v)}}
assert.equal(readCardView(storage),'cards')
saveCardView(storage,'list');assert.equal(readCardView(storage),'list')
saveCardView(storage,'cards');assert.equal(readCardView(storage),'cards')
saveCardView(storage,'invalid');assert.equal(readCardView(storage),'cards')
const blocked={getItem(){throw Error('blocked')},setItem(){throw Error('blocked')}}
assert.equal(readCardView(blocked),'cards');saveCardView(blocked,'list')
assert.equal(returnStatus('bogus'),'unknown')
for(const state of ['unopened','opened','waiting','returned','unverified','unmatched'])assert.ok(returnStatusLabels[returnStatus(state)])
for(const [file,component] of [['ProFinancialCell.vue','ReturnStatusBadge'],['ProGPTPayPanel.vue','ReturnCaseLine'],['EJHManagementView.vue','ReturnStatusBadge']]) {
 const view=readFileSync(new URL('../src/components/'+file,import.meta.url),'utf8')
 assert.ok(view.includes('<'+component),'Shared colors missing in '+file)
}
assert.ok(source.includes("cardView==='cards'"));assert.ok(source.includes('class="ejh-card-table"'))
console.log('Passed: canonical/alias card lookup, account-email search, exact navigation, non-EJH exclusion, persistent view selection and shared status badges')
