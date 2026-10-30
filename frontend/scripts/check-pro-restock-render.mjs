import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import * as Vue from 'vue'
import { renderToString } from '@vue/server-renderer'
import { compileScript, parse } from '@vue/compiler-sfc'
import { transformWithEsbuild } from 'vite'

// Execute the real template before the first async response. Script-only request
// tests cannot detect a template dereferencing state while it is still null.
const file = new URL('../src/components/ProRestockView.vue', import.meta.url)
const source = readFileSync(file, 'utf8')
const modules = { vue: Vue }
for (const [, id] of source.matchAll(/^import .* from ['"]([^'"]+)['"]/gm)) {
  if (id === 'vue') continue
  modules[id] = id.endsWith('.vue')
    ? { default: { render: () => null }, __esModule: true }
    : await import(new URL(id + '.js', file))
}
const { descriptor } = parse(source)
const compiled = compileScript(descriptor, { id: 'restock-render-check', inlineTemplate: true })
const { code } = await transformWithEsbuild(compiled.content, 'restock-render-check.js', { format: 'cjs', loader: 'js' })
const module = { exports: {} }
vm.runInNewContext(code, {
  module, exports: module.exports, require: id => {
    assert.ok(modules[id], 'Unexpected template dependency: ' + id)
    return modules[id]
  },
  console,
})
const app = Vue.createSSRApp(module.exports.default)
app.config.errorHandler = error => { throw error }
const html = await renderToString(app)
assert.match(html, /正在读取补货状态/)
for (const title of ['监控大屏', '补货配置', '兜底配置', '决策与记录']) assert.ok(html.includes(title))
assert.ok(!html.includes('pro-fallback-card-slot'), 'Data-dependent content rendered before state arrived')
console.log('Passed: restock initial template renders without data and keeps all tab navigation available')
