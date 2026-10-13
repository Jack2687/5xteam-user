import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'

function declarations(file, functions, variables) {
  const source = readFileSync(new URL('../src/components/' + file, import.meta.url), 'utf8')
  const script = source.split('<script setup>')[1].split('</script>')[0]
  const parsed = ts.createSourceFile(file, script, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS)
  return parsed.statements.filter(statement =>
    ts.isFunctionDeclaration(statement) && functions.includes(statement.name?.text) ||
    ts.isVariableStatement(statement) && statement.declarationList.declarations.some(item => variables.includes(item.name.getText(parsed))),
  ).map(statement => statement.getText(parsed)).join('\n')
}

const settingsContext = vm.createContext({ reactive: value => value })
vm.runInContext(declarations('ProManagementView.vue', ['connectionPayload'], ['defaults', 'settings']) + '\nthis.form = { settings, connectionPayload }', settingsContext)
const { settings, connectionPayload } = settingsContext.form
settings.vm2api.url = ' https://pro-vm.example '
settings.vm2api_key = 'unsaved-pro-key'
settings.vm2api.runtime_type = 'kvm'
settings.vm2api.vcpus = 4
const payload = connectionPayload('vm2api')
assert.equal(payload.vm2api.url, 'https://pro-vm.example')
assert.equal(payload.vm2api_key, 'unsaved-pro-key')
assert.equal(payload.vm2api.runtime_type, 'kvm')
assert.equal(payload.vm2api.vcpus, 4)
assert.equal(payload.cpa_key, undefined)
assert.equal(payload.sub2_password, undefined)

const picker = declarations('VM2ProxyPicker.vue', ['cancel', 'refresh'], ['rows', 'loading', 'loaded', 'error', 'controller'])
for (const scope of ['pro', 'team']) {
  const calls = []
  const props = { scope, active: true, enabled: true, disabled: false, url: ' https://vm.example ', managementKey: '' }
  const context = vm.createContext({ props, AbortController, ref: value => ({ value }), api: async (path, options) => { calls.push({ path, options }); return [] } })
  vm.runInContext(picker + '\nthis.refresh = refresh', context)
  await context.refresh()
  assert.equal(calls.length, 1)
  assert.equal(calls[0].path, scope === 'pro' ? '/api/pro-settings/vm2api/proxies' : '/api/push-settings/vm2api/proxies')
  assert.equal(calls[0].options.body.vm2api.url, 'https://vm.example')
  if (scope === 'pro') {
    assert.equal(calls[0].options.body.vm2api_key, '', 'Blank key must let the Pro endpoint load its own saved credential')
    assert.equal(calls[0].options.body.vm2api.key, undefined)
  } else assert.equal(calls[0].options.body.vm2api.key, '')
  props.active = false
  await context.refresh()
  assert.equal(calls.length, 1, 'Inactive provider must not read proxies')
}
console.log('Passed: Pro unsaved VM settings, runtime/spec payload, separate proxy/key scopes and inactive provider')

const groupPicker = declarations('VM2GroupPicker.vue', ['cancel', 'refresh'], ['rows', 'loading', 'loaded', 'error', 'controller'])
for (const scope of ['pro', 'team']) {
  const calls = []
  const props = { scope, active: true, disabled: false, url: ' https://vm.example ', managementKey: 'edited-key' }
  const context = vm.createContext({ props, AbortController, ref: value => ({ value }), api: async (path, options) => { calls.push({ path, options }); return [{ id: 'pool_aaaaaaaa' }] } })
  vm.runInContext(groupPicker + '\nthis.refresh = refresh', context)
  await context.refresh()
  assert.equal(calls.length, 1)
  assert.equal(calls[0].path, scope === 'pro' ? '/api/pro-settings/vm2api/groups' : '/api/push-settings/vm2api/groups')
  assert.equal(calls[0].options.body.vm2api.url, 'https://vm.example')
  assert.equal(scope === 'pro' ? calls[0].options.body.vm2api_key : calls[0].options.body.vm2api.key, 'edited-key')
  props.active = false
  await context.refresh()
  assert.equal(calls.length, 1)
}
settings.vm2api.group_ids = ['pool_aaaaaaaa', 'pool_bbbbbbbb']
assert.deepEqual(connectionPayload('vm2api').vm2api.group_ids, ['pool_aaaaaaaa', 'pool_bbbbbbbb'])
console.log('Passed: Team/Pro group loading, unsaved credentials and multiple group IDs')
