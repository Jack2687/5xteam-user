import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import vm from 'node:vm'
import {ref,computed,reactive} from 'vue'

const script=name=>readFileSync(new URL(`../src/components/${name}.vue`,import.meta.url),'utf8').split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm,'')
const calls=[]
const props={selected:[{email:'selected@example.com'}],query:'query fixture',mergeState:'deleted'}
const ctx=vm.createContext({ref,computed,defineProps:()=>props,defineEmits:()=>()=>{},defineExpose(){},downloadFile:async(...args)=>calls.push(args)})
vm.runInContext(script('ProTransferDialog')+';globalThis.view={open,scope,exportFormat,exportJSON};',ctx)
ctx.view.open()
await ctx.view.exportJSON()
assert.deepEqual(JSON.parse(JSON.stringify(calls[0][2].body)),{format:'credentials',emails:['selected@example.com']})
ctx.view.scope.value='all'
await ctx.view.exportJSON()
assert.deepEqual(JSON.parse(JSON.stringify(calls[1][2].body)),{format:'credentials',all:true,query:'query fixture',merge_state:'deleted'})
ctx.view.exportFormat.value='json'
await ctx.view.exportJSON()
assert.equal(calls[2][1],'pro-accounts.json')
assert.equal(calls[2][2].body.format,'json')

let downloaded
const single=vm.createContext({reactive,computed,onBeforeUnmount(){},defineExpose(){},defineEmits:()=>()=>{}})
vm.runInContext(script('AccountCredentialsDialog')+';globalThis.view={credentialDialog,exportAccountText};',single)
single.capture=(...args)=>downloaded=args
vm.runInContext('downloadJSON=(...args)=>capture(...args)',single)
Object.assign(single.view.credentialDialog,{loaded:true,email:'one@example.com',gptPassword:'password',totpSecret:'secret',original:{gptPassword:'password',totpSecret:'secret',accessToken:'',refreshToken:'',chatgptSession:'',accountID:''}})
single.view.exportAccountText()
assert.equal(downloaded[1],'one@example.com----password----secret\r\n')
assert.equal(downloaded[2],true)
single.view.credentialDialog.gptPassword='unsaved'
downloaded=null
single.view.exportAccountText()
assert.equal(downloaded,null,'unsaved credentials exported')
console.log('Passed: selected/all filtered TXT, legacy JSON and single-account credential export without AT/RT')
