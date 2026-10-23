import assert from 'node:assert/strict'
import vm from 'node:vm'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { ref } from 'vue'

const source = readFileSync(new URL('../src/components/ProManagementView.vue', import.meta.url), 'utf8')
const parsed = ts.createSourceFile('view.js', source.split('<script setup>')[1].split('</script>')[0], ts.ScriptTarget.Latest, true, ts.ScriptKind.JS)
const fn = parsed.statements.find(s => ts.isFunctionDeclaration(s) && s.name.text === 'toggleAutoDelete').getText(parsed)
const requests = [], messages = [], polls = []
const settings = { auto_delete_paused: false, quota_enabled: true }
const busy = ref(false)
const c = vm.createContext({ settings, deleteControlBusy: busy, deleteControlRevision: 0, settingsLoaded: ref(true),
  api: (url, input) => new Promise((resolve, reject) => requests.push({ url, input, resolve, reject })),
  setMessage: (...v) => messages.push(v), schedulePoll: v => polls.push(v) })
vm.runInContext(fn, c)
const stop = c.toggleAutoDelete()
await c.toggleAutoDelete()
assert.equal(requests.length, 1, 'rapid clicks must not submit conflicting toggles')
assert.equal(requests[0].url, '/api/pro-settings/auto-delete-control')
assert.equal(requests[0].input.body.paused, true)
requests[0].resolve({ auto_delete_paused: true }); await stop
assert.equal(settings.auto_delete_paused, true)
assert.equal(settings.quota_enabled, true)
assert.equal(busy.value, false)
const resume = c.toggleAutoDelete()
assert.equal(requests[1].input.body.paused, false)
requests[1].resolve({ auto_delete_paused: false }); await resume
assert.equal(requests.length, 2, 'resume must not call an account deletion or start API')
assert.equal(settings.auto_delete_paused, false)
assert.match(messages.at(-1)[0], /不直接执行删除/)
const failure = c.toggleAutoDelete()
requests[2].reject(new Error('save failed')); await failure
assert.equal(settings.auto_delete_paused, false, 'failed save must retain last confirmed state')
assert.equal(busy.value, false)
assert.equal(messages.at(-1)[1], 'error')
assert.equal(polls.length, 3)
console.log('Passed: independent automatic deletion pause/resume, duplicate clicks, failure recovery and no direct deletion on resume')
