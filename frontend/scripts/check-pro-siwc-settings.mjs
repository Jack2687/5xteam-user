import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
const source=readFileSync(new URL('../src/components/ProManagementView.vue',import.meta.url),'utf8').split('<script setup>')[1].split('</script>')[0]
const parsed=ts.createSourceFile('view.js',source,ts.ScriptTarget.Latest,true,ts.ScriptKind.JS)
const names=new Set(['defaults','settings','pushModeDrafts','normalizeSection','applySettings','switchPushMode','syncGroupNames','connectionPayload','saveSettings'])
const declarations=parsed.statements.filter(s=>ts.isFunctionDeclaration(s)&&names.has(s.name?.text)||ts.isVariableStatement(s)&&s.declarationList.declarations.some(d=>names.has(d.name.getText(parsed)))).map(s=>s.getText(parsed)).join('\n')
const calls=[]
let fail=false
const ctx=vm.createContext({reactive:v=>v,busy:{value:''},sub2Models:{value:''},sub2Groups:{value:[]},cpaGroups:{value:[]},groupErrors:{},setMessage:(text,type)=>{ctx.message={text,type}},updateCountdown:()=>{},api:async(path,options)=>{
 calls.push({path,options})
 if(path.includes('/variant'))return {auth_mode:'siwc',url:'',email:'',proxy_ids:null,account_concurrency:10}
 if(fail)throw Error('save failed')
 return {provider:'sub2',sub2:{...options.body.sub2,models:null,group_names:null},cpa:{},post_merge_group_ids:null}
}})
vm.runInContext(declarations+'\nthis.view={settings,switchPushMode,saveSettings,connectionPayload,applySettings}',ctx)
const v=ctx.view
v.settings.sub2.url='https://ordinary.test';v.settings.sub2.group_ids=[7];v.settings.sub2.proxy_ids=[9];v.settings.sub2_password='ordinary'
await v.switchPushMode({target:{value:'siwc'}})
assert.equal(v.settings.sub2.group_ids.length,0)
assert.equal(v.settings.sub2.proxy_ids.length,0)
v.settings.sub2.url='https://siwc.test';v.settings.sub2.email='admin@example.test';v.settings.sub2.group_ids=[2];v.settings.sub2_password='siwc'
assert.equal(v.connectionPayload('sub2').sub2.auth_mode,'siwc')
await v.switchPushMode({target:{value:''}})
assert.equal(v.settings.sub2.group_ids.join(','),'7')
assert.equal(v.settings.sub2.proxy_ids.join(','),'9')
assert.equal(v.settings.sub2_password,'ordinary')
await v.switchPushMode({target:{value:'siwc'}})
assert.equal(v.settings.sub2.group_ids.join(','),'2')
fail=true
await v.saveSettings()
assert.equal(ctx.busy.value,'');assert.equal(ctx.message.type,'error');assert.equal(v.settings.sub2_password,'siwc')
fail=false
await v.saveSettings()
assert.equal(ctx.message.type,'success');assert.equal(ctx.busy.value,'')
assert.equal(v.settings.sub2.models.length,0);assert.equal(v.settings.sub2.group_names.length,0);assert.equal(v.settings.post_merge_group_ids.length,0)
// A client-side preparation error must also release the save button.
ctx.sub2Models.value=null
await v.saveSettings()
assert.equal(ctx.busy.value,'');assert.equal(ctx.message.type,'error')
console.log('SIWC sparse configuration, switch drafts, connection mode and save recovery passed')
