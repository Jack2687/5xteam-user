import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'

const source = readFileSync(new URL('../src/components/QualitySettingsView.vue', import.meta.url), 'utf8')
const script = source.slice(source.indexOf('<script setup>') + '<script setup>'.length, source.indexOf('</script>'))
const parsed = ts.createSourceFile('quality.js', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS)
const names = new Set(['setForm', 'save', 'run'])
const declarations = parsed.statements.filter(statement => ts.isFunctionDeclaration(statement) && names.has(statement.name?.text)).map(statement => statement.getText(parsed)).join('\n')
const confirmations = []
const calls = []
const context = vm.createContext({
  form: {}, busy: { value: false }, initial: { value: null }, message: {}, isVM: { value: true }, emit() {},
  window: { confirm(message) { confirmations.push(message); return true } },
  api: async (path, options) => {
    calls.push({ path, options: structuredClone(options) })
    return { settings: structuredClone(options?.body || {}) }
  },
})
vm.runInContext(declarations, context)
context.setForm({ provider: 'vm2api', enabled: true, action: 'observe' })
await context.save()
assert.equal(calls[0].options.body.provider, 'vm2api')
assert.equal(calls[0].options.body.action, 'observe')
await context.run()
assert.match(confirmations.at(-1), /仅获取.*不执行踢出/)
// An unsaved change must not misrepresent the backend's saved action.
context.form.action = 'kick'
await context.run()
assert.match(confirmations.at(-1), /仅获取.*不执行踢出/)
await context.save()
context.form.action = 'observe'
await context.run()
assert.match(confirmations.at(-1), /强制踢出.*容器/)
assert.equal(calls.at(-1).path, '/api/quality/run')
context.isVM.value = false
context.setForm({ provider: 'sub2', enabled: true, action: 'groups', normal_group_ids: [1], degraded_group_ids: [2] })
await context.save()
await context.run()
assert.match(confirmations.at(-1), /Sub2.*切组/)
assert.equal(calls.at(-2).options.body.action, 'groups')
console.log('Passed: vm2api observe/kick persistence, confirmations use saved policy, and Sub2 group actions remain available')
