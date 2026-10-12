<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RefreshCw } from 'lucide-vue-next'
import { api } from '../api'

const props = defineProps({
  enabled: Boolean, ids: { type: Array, default: () => [] }, active: Boolean, disabled: Boolean,
  url: { type: String, default: '' }, managementKey: { type: String, default: '' },
})
const emit = defineEmits(['update:enabled', 'update:ids'])
const rows = ref([])
const query = ref('')
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
let controller
function cancel() { controller?.abort(); controller = null; loading.value = false }
function available(p) { return p.enabled && !p.blocked_reason && !['dead', 'fail'].includes(p.status) && Number.isInteger(p.bound_count) && p.bound_count >= 0 && Number.isInteger(p.bind_limit) && p.bound_count < p.bind_limit }
function state(p) { return !p.enabled || p.status === 'dead' ? '未启用' : p.blocked_reason ? (p.blocked_reason === 'ipv6_disabled' ? 'IPv6 已关闭' : p.blocked_reason) : p.status === 'fail' ? '检测失败' : p.bound_count == null || p.bind_limit == null ? '缺少绑定数' : p.bound_count >= p.bind_limit ? '绑定已满' : '可用' }
function address(p) { return p.scheme === 'local' || p.id === 'px-local' ? '服务器本地出口' : `${p.scheme || 'socks5'}://${p.host.includes(':') ? `[${p.host}]` : p.host}:${p.port}` }
function urlKey(value) { try { return new URL(value.trim()).href.replace(/\/+$/, '') } catch { return value.trim().replace(/\/+$/, '') } }
const selected = computed(() => new Set((props.ids || []).map(String)))
const visible = computed(() => rows.value.filter(p => `${p.label} ${p.host} ${p.port} ${p.id}`.toLowerCase().includes(query.value.trim().toLowerCase())))
const missing = computed(() => loaded.value ? [...selected.value].filter(id => !rows.value.some(p => p.id === id)) : [])
function toggle(id, checked) { const ids = new Set(selected.value); checked ? ids.add(String(id)) : ids.delete(String(id)); emit('update:ids', [...ids]) }
function selectVisible() { emit('update:ids', [...new Set([...selected.value, ...visible.value.filter(available).map(p => p.id)])]) }
async function refresh() {
  if (!props.active || !props.enabled || props.disabled) return
  cancel()
  const request = new AbortController()
  controller = request
  loading.value = true; error.value = ''
  try {
    const result = await api('/api/push-settings/vm2api/proxies', { method: 'POST', body: { vm2api: { url: props.url.trim(), key: props.managementKey } }, signal: request.signal })
    if (request.signal.aborted) return
    rows.value = result || []; loaded.value = true
  } catch (e) { if (!request.signal.aborted) error.value = e.message }
  finally { if (controller === request) { loading.value = false; controller = null } }
}
watch(() => props.url, (value, old) => {
  cancel(); rows.value = []; loaded.value = false; error.value = ''
  if (urlKey(value) !== urlKey(old) && old.trim()) emit('update:ids', [])
})
watch(() => props.managementKey, () => { cancel(); rows.value = []; loaded.value = false; error.value = '' })
watch(() => [props.active, props.enabled, props.disabled], () => {
  cancel()
  if (props.active && props.enabled && props.url) void refresh()
}, { immediate: true })
onBeforeUnmount(cancel)
</script>

<template>
  <div class="proxy-picker">
    <label class="proxy-toggle"><input type="checkbox" :checked="enabled" :disabled="disabled" @change="emit('update:enabled', $event.target.checked)" /><strong>绑定代理</strong><span>已选 {{ selected.size }} 个</span></label>
    <p v-if="!enabled">默认使用 vm2api 的 local 本地代理。</p>
    <template v-else>
      <p v-if="!active">启用 vm2api 后可读取代理列表。</p>
      <div class="proxy-actions"><input v-model="query" aria-label="搜索 vm2api 代理" placeholder="搜索代理名称 / 地址 / ID" /><button class="btn ghost compact" type="button" :disabled="disabled || !active || loading" @click="refresh"><RefreshCw :size="14" :class="{ spin: loading }" />{{ loading ? '读取中…' : '刷新代理' }}</button></div>
      <div class="proxy-actions"><button class="btn ghost compact" type="button" :disabled="disabled || loading || !active || !visible.some(available)" @click="selectVisible">选择筛选结果</button><button class="btn ghost compact" type="button" :disabled="disabled || !selected.size" @click="emit('update:ids', [])">清空选择</button></div>
      <p v-if="error" class="proxy-error" role="alert">{{ error }}</p>
      <div v-if="rows.length" class="proxy-list">
        <label v-for="p in visible" :key="p.id" class="proxy-row">
          <input type="checkbox" :checked="selected.has(p.id)" :disabled="disabled || (!available(p) && !selected.has(p.id))" @change="toggle(p.id, $event.target.checked)" />
          <span><strong>{{ p.label || (p.id === 'px-local' ? 'local 本地代理' : p.id) }}</strong><small>{{ address(p) }} · {{ p.id }}</small><small>{{ state(p) }}</small></span>
          <b>{{ p.bound_count ?? '—' }} / {{ p.bind_limit ?? '—' }} <small>个槽位</small></b>
        </label>
        <p v-if="!visible.length">没有匹配的代理</p>
      </div>
      <p v-else-if="loaded">没有可选的 vm2api 代理</p>
      <p v-else-if="!loading && !error">点击“刷新代理”读取列表和绑定槽位数。</p>
      <div v-for="id in missing" :key="id" class="proxy-missing">已选代理 {{ id }} 已删除或不可见 <button class="btn ghost compact" type="button" :disabled="disabled" @click="toggle(id, false)">取消选择</button></div>
      <p v-if="!selected.size">未选择代理，默认使用 vm2api 的 local 本地代理。</p>
      <p v-else>推送时优先选择绑定槽位最少的可用代理，并列时随机；所选代理均不可用或已满时停止推送。</p>
      <p>计数包含已暂停的槽位。设置仅影响新槽位，重试和重登沿用原代理。</p>
    </template>
  </div>
</template>

<style scoped>
.proxy-picker{grid-column:1/-1;border-top:1px solid var(--border,#d8e0eb);padding:14px 0;margin:12px 0;min-width:0}
.proxy-toggle,.proxy-actions{display:flex;align-items:center;gap:9px;flex-wrap:wrap}.proxy-toggle span{margin-left:auto;font-size:12px}
.proxy-picker p{font-size:12px;line-height:1.65;opacity:.85;margin:8px 0}.proxy-actions{margin:8px 0}.proxy-actions>input{flex:1;min-width:130px;width:0;padding:8px;border:1px solid var(--border,#d8e0eb);border-radius:6px;background:transparent;color:inherit}
.proxy-list{max-height:300px;overflow:auto}.proxy-row{display:flex;gap:9px;align-items:center;border-top:1px solid var(--border,#d8e0eb);padding:10px 0}.proxy-row>span{flex:1;min-width:0}.proxy-row strong,.proxy-row small{display:block;overflow-wrap:anywhere}.proxy-row small{font-size:11px;opacity:.75}.proxy-row b{text-align:right;font-size:14px;white-space:nowrap}.proxy-error{color:#d94848}.proxy-missing{font-size:12px;display:flex;align-items:center;justify-content:space-between;gap:8px}
</style>
