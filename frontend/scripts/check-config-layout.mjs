import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { nextTick } from 'vue'
import { useConfigSections } from '../src/useConfigSections.js'
for (const name of ['SettingsView','FreePipelineView','QualitySettingsView','AutoRotationView','ProManagementView']) {
 const path='frontend/src/components/'+name+'.vue'
 const source=readFileSync(new URL('../src/components/'+name+'.vue',import.meta.url),'utf8')
 const before=execFileSync('git',['show','HEAD:'+path],{encoding:'utf8'})
 const models=s=>[...s.matchAll(/v-model(?:[\w:.-]*)="([^"]+)"/g)].map(m=>m[1]).filter(m=>m!=='configSection').sort()
 assert.deepEqual(models(source),models(before),name+' must preserve all business bindings')
 assert.ok(source.includes('ops-config'))
 assert.ok(source.includes('@invalid.capture="revealConfigInvalid"'))
 assert.ok(source.includes('v-model="configSection"'))
}
const {configSection,revealConfigInvalid}=useConfigSections('first')
let focused=false, reported=false, prevented=false
const target={closest:()=>({dataset:{configSection:'second'}}),focus:()=>{focused=true},reportValidity:()=>{reported=true;revealConfigInvalid({target,preventDefault:()=>{throw Error('recursive validation')}})}}
revealConfigInvalid({target,preventDefault:()=>{prevented=true}})
assert.equal(configSection.value,'second')
await nextTick()
assert.ok(focused&&reported&&prevented)
console.log('Passed: unchanged business v-models, isolated navigation state, hidden-field focus and nonrecursive validation')
