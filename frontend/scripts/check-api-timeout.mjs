import assert from 'node:assert/strict'
import http from 'node:http'
import { api } from '../src/api.js'

const server = http.createServer((req, res) => {
  if (req.url === '/stall') return
  res.setHeader('Content-Type', 'application/json')
  if (req.url === '/body-stall') { res.write('{"ok":'); return }
  if (req.url === '/failure') { res.writeHead(409); res.end(JSON.stringify({ok:false,error:'conflict'})); return }
  res.end(JSON.stringify({ok:true,data:{method:req.method}}))
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const base = `http://127.0.0.1:${server.address().port}`
try {
  for (const path of ['/stall', '/body-stall']) {
    await assert.rejects(api(base+path, {timeoutMs:100}), /读取数据超时/)
  }
  const controller = new AbortController()
  const cancelled = api(base+'/stall', {signal:controller.signal,timeoutMs:1000})
  controller.abort()
  await assert.rejects(cancelled, error => !error.message.includes('读取数据超时'))
  await assert.rejects(api(base+'/failure', {timeoutMs:1000}), error => error.status === 409 && error.message === 'conflict')
  assert.deepEqual(await api(base+'/', {timeoutMs:1000}), {method:'GET'})
  assert.deepEqual(await api(base+'/', {method:'POST',body:{}}), {method:'POST'})
  console.log('Passed: real HTTP header/body timeout, navigation cancellation, status preservation, normal reads and unchanged writes')
} finally {
  server.closeAllConnections()
  await new Promise(resolve => server.close(resolve))
}
