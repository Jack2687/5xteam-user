import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { ref, reactive, computed } from 'vue'
import { createLatestRequest } from '../src/latestRequest.js'

const tick = () => new Promise(resolve => setImmediate(resolve))
const source = readFileSync(new URL('../src/components/ProRestockView.vue', import.meta.url), 'utf8')
const code = source.split('<script setup>')[1].split('</script>')[0].replace(/^import .*$/gm, '')
function fixture() {
  const requests = [], timers = new Map()
  let mounted, unmounted, timerID = 0
  const context = vm.createContext({
    ref, reactive, computed, createLatestRequest, nextTick: tick,
    onMounted(fn) { mounted = fn }, onUnmounted(fn) { unmounted = fn },
    setInterval(fn) { const id = ++timerID; timers.set(id, fn); return id },
    clearInterval(id) { timers.delete(id) },
    api(url, options = {}) {
      let resolve, reject
      const promise = new Promise((ok, fail) => { resolve = ok; reject = fail })
      requests.push({ url, options, resolve, reject })
      return promise
    },
  })
  vm.runInContext(code + '\nglobalThis.exposed = {state, form, records, page, refresh, pause, evaluate, resetBreaker, loadRecords};', context)
  return { requests, timers, mounted: () => mounted(), unmounted: () => unmounted(), ...context.exposed }
}

// An initial request completing after navigation must not resurrect polling.
{
  const f = fixture()
  const mount = f.mounted()
  await tick()
  f.unmounted()
  f.requests[0].resolve({ config: { enabled: true, version: 1 } })
  await tick()
  for (const r of f.requests.slice(1)) r.resolve([])
  await mount
  assert.equal(f.timers.size, 0, 'unmounted page recreated polling')
  assert.equal(f.requests.length, 1, 'unmounted page started another request')
  assert.equal(f.state.value, null, 'unmounted page accepted an old response')
}

// A late GET cannot undo the state returned by a pause/evaluate/reset action.
for (const action of ['pause', 'evaluate', 'resetBreaker']) {
  const f = fixture()
  f.state.value = { config: { enabled: true, version: 1 } }
  const oldPoll = f.refresh()
  await tick()
  const old = f.requests[0]
  const operation = f[action]()
  await tick()
  const mutation = f.requests.find(r => r.options.method)
  assert.ok(mutation, 'missing action request')
  const config = { enabled: false, version: 2 }
  mutation.resolve(action === 'pause' ? config : { config })
  await tick()
  for (const r of f.requests) {
    if (r === old || r === mutation) continue
    r.resolve(r.url.includes('/records') ? [] : { config })
  }
  await tick()
  for (const r of f.requests.filter(r => r.url.includes('/records'))) r.resolve([])
  await operation
  old.resolve({ config: { enabled: true, version: 1 } })
  await tick()
  for (const r of f.requests.filter(r => r.url.includes('/records'))) r.resolve([])
  await oldPoll
  assert.equal(f.state.value.config.version, 2, `${action}: old response replaced new state`)
  if (action === 'pause') assert.equal(f.form.version, 2, 'pause did not refresh settings form')
  f.unmounted()
}

// Paging cannot be overwritten by an older page's records response.
{
  const f = fixture()
  const first = f.loadRecords()
  await tick()
  f.page.value = 2
  const second = f.loadRecords()
  await tick()
  f.requests[1].resolve([{ id: 'page-2' }])
  await second
  f.requests[0].resolve([{ id: 'page-1' }])
  await first
  assert.equal(f.records.value[0].id, 'page-2', 'old page replaced current records')
  f.unmounted()
}
console.log('Passed: restock unmount, pause/evaluate/reset ordering, and record paging')
