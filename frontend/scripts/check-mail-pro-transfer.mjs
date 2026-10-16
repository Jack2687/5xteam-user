import assert from 'node:assert/strict'
import { moveMailAccountsToPro } from '../src/mailProTransfer.js'

let active = 0, peak = 0
const calls = [], progress = []
const request = async (path, options) => {
  active++; peak = Math.max(peak, active)
  calls.push({ path, options })
  await new Promise(resolve => setTimeout(resolve, 5))
  active--
  if (path.includes('failed%40')) throw new Error('账号已删除')
}
const emails = [' FIRST@example.com ', 'first@example.com', '', 'second@example.com', 'failed@example.com', 'fourth@example.com', 'fifth@example.com']
const result = await moveMailAccountsToPro(emails, request, (done, total) => progress.push([done, total]))
assert.equal(result.length, 5)
assert.equal(calls.length, 5)
assert.equal(result.filter(item => item.success).length, 4)
assert.deepEqual(result[2], { email: 'failed@example.com', success: false, error: '账号已删除' })
assert.deepEqual(result.map(item => item.email), ['first@example.com', 'second@example.com', 'failed@example.com', 'fourth@example.com', 'fifth@example.com'])
assert.equal(peak, 3)
assert.deepEqual(progress, [[1,5],[2,5],[3,5],[4,5],[5,5]])
for (const call of calls) {
  assert.match(call.path, /^\/api\/mail\/accounts\/[^/]+\/management-scope$/)
  assert.deepEqual(call.options, { method: 'PUT', body: { scope: 'pro' } })
}
assert.deepEqual(await moveMailAccountsToPro([], () => { throw new Error('must not request all') }), [])
assert.deepEqual(await moveMailAccountsToPro(['first@example.com'], async () => {}), [{ email:'first@example.com', success:true }])
console.log('mail Pro transfer: selection snapshot, deduplication, bounded concurrency, failure isolation, endpoint reuse and empty selection passed')
