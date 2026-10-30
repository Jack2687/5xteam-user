import assert from 'node:assert/strict'
import { refreshEJHSync } from '../src/ejhSync.js'

function fixture(responses) {
  const calls = [], phases = []
  let stopped = false, time = 0
  return { calls, phases, stop: () => { stopped = true }, options: {
    now: () => time, delay: async () => { time += 1500 }, stopped: () => stopped,
    onProgress: text => phases.push(text),
    api: async (url, options) => {
      calls.push({ url, options })
      const value = responses.shift()
      if (value instanceof Error) throw value
      assert.ok(value, 'unexpected extra request')
      return value
    },
  } }
}
const task = status => ({ id: 'shared-task', status })
let f = fixture([task('queued'), task('running'), task('completed')])
assert.equal((await refreshEJHSync(f.options)).synced, true)
assert.equal(f.calls.filter(c => c.options.method === 'POST').length, 1)
assert.deepEqual(f.calls[0].options.body, { async: true })
assert.equal(f.calls[1].url, '/api/ejh/sync/shared-task')
assert.match(f.phases[0], /等待其他卡台/)
assert.match(f.phases[1], /正在更新/)

f = fixture([task('queued'), new Error('temporary network failure'), task('completed')])
assert.equal((await refreshEJHSync(f.options)).synced, true)
assert.equal(f.calls.filter(c => c.options.method === 'POST').length, 1, 'poll failure resubmitted the sync')

f = fixture([task('running'), { ...task('failed'), error: '卡片订单 fixture 交易更新失败：missing tradeInfos' }])
await assert.rejects(refreshEJHSync(f.options), /fixture.*missing tradeInfos/)
f = fixture([task('queued')]); f.options.delay = async () => f.stop()
assert.equal((await refreshEJHSync(f.options)).stopped, true)
assert.equal(f.calls.length, 1, 'stopped page continued polling')
f = fixture([task('queued'), Object.assign(new Error('任务已过期'), { status: 404 })])
await assert.rejects(refreshEJHSync(f.options), /已过期/)
assert.equal(f.calls.length, 2)
f = fixture([task('queued'), new Error('offline'), new Error('offline'), new Error('offline')])
await assert.rejects(refreshEJHSync(f.options), /offline/)
assert.equal(f.calls.length, 4, 'unbounded poll retries')
f = fixture([task('queued'), task('running')])
// The injected clock must be captured by reference for a deterministic deadline.
let now = 0
f.options.now = () => now; f.options.delay = async () => { now = 999999 }
await assert.rejects(refreshEJHSync(f.options), /等待超时/)
f = fixture([{ synced: true }]); assert.equal((await refreshEJHSync(f.options)).synced, true)
console.log('Passed: shared task polling, queued/running/completed status, real errors, transient reads, stop, expiry and bounded waits')
