<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RefreshCw } from 'lucide-vue-next'
import { api } from '../api'

const props = defineProps({
  scope: { type: String, default: 'team' },
  ids: { type: Array, default: () => [] }, active: Boolean, disabled: Boolean,
  url: { type: String, default: '' }, managementKey: { type: String, default: '' },
})
const emit = defineEmits(['update:ids'])
const rows = ref([])
const query = ref('')
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
let controller
function cancel() { controller?.abort(); controller = null; loading.value = false }
function urlKey(value) { try { return new URL(value.trim()).href.replace(/\/+$/, '') } catch { return value.trim().replace(/\/+$/, '') } }
const selected = computed(() => new Set((props.ids || []).map(String)))
const visible = computed(() => rows.value.filter(g => `${g.name} ${g.id}`.toLowerCase().includes(query.value.trim().toLowerCase())))
const missing = computed(() => loaded.value ? [...selected.value].filter(id => !rows.value.some(g => g.id === id)) : [])
function toggle(id, checked) { const ids = new Set(selected.value); checked ? ids.add(id) : ids.delete(id); emit('update:ids', [...ids]) }
function selectVisible() { emit('update:ids', [...new Set([...selected.value, ...visible.value.filter(g => g.enabled).map(g => g.id)])]) }
async function refresh() {
  if (!props.active || props.disabled) return
  cancel()
  const request = new AbortController()
  controller = request
  loading.value = true; error.value = ''
  try {
    const pro = props.scope === 'pro'
    const result = await api(pro ? '/api/pro-settings/vm2api/groups' : '/api/push-settings/vm2api/groups', { method: 'POST', body: pro ? { vm2api: { url: props.url.trim() }, vm2api_key: props.managementKey } : { vm2api: { url: props.url.trim(), key: props.managementKey } }, signal: request.signal })
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
watch(() => props.active, () => { cancel(); if (props.active && props.url) void refresh() }, { immediate: true })
onBeforeUnmount(cancel)
</script>

<template>
  <div class="vm-group-picker">
    <div class="group-actions"><strong>账号分组（多选）</strong><span>已选 {{ selected.size }} 个</span><button class="btn ghost compact" type="button" :disabled="disabled || !active || loading" @click="refresh"><RefreshCw :size="14" :class="{ spin: loading }" />{{ loading ? '读取中…' : '读取分组' }}</button></div>
    <p v-if="!active">启用 vm2api 后可读取分组。</p>
    <p v-if="error" class="group-error" role="alert">{{ error }}</p>
    <template v-if="rows.length">
      <div class="group-actions"><input v-model="query" aria-label="搜索 vm2api 分组" placeholder="搜索分组名称 / ID" /><button class="btn ghost compact" type="button" :disabled="disabled || !active" @click="selectVisible">选择筛选结果</button><button class="btn ghost compact" type="button" :disabled="disabled || !selected.size" @click="emit('update:ids', [])">清空</button></div>
      <div class="group-list"><label v-for="g in visible" :key="g.id"><input type="checkbox" :checked="selected.has(g.id)" :disabled="disabled || (!g.enabled && !selected.has(g.id))" @change="toggle(g.id, $event.target.checked)" /><span>{{ g.name }} <small>{{ g.enabled ? '' : '（已停用）' }} · {{ g.account_count }} 个账号</small></span></label></div>
    </template>
    <p v-else-if="loaded">暂无分组，请先在 vm2api 的“密钥”或“虚拟机”页面创建账号分组。</p>
    <p v-for="id in missing" :key="id" class="group-error">分组 {{ id }} 已删除或不可见 <button class="btn ghost compact" type="button" :disabled="disabled" @click="toggle(id, false)">取消选择</button></p>
    <p>{{ selected.size ? '新账号会同时加入所有已选分组；所选分组不存在或已停用时停止推送。' : '不选分组：保持全局调度，不加入自定义分组。' }}</p>
    <p>仅影响新推送；已绑定账号的重试、重登和清理沿用原绑定。</p>
  </div>
</template>

<style scoped>
.vm-group-picker{grid-column:1/-1;border-top:1px solid var(--border,#d8e0eb);padding:14px 0;margin:12px 0;min-width:0}.group-actions{display:flex;align-items:center;gap:9px;flex-wrap:wrap;margin:8px 0}.group-actions>input{flex:1;min-width:130px;padding:8px;border:1px solid var(--border,#d8e0eb);border-radius:6px;background:transparent;color:inherit}.vm-group-picker p{font-size:12px;line-height:1.65;opacity:.85;margin:8px 0}.group-list{max-height:260px;overflow:auto;display:flex;flex-wrap:wrap;gap:12px}.group-list label{display:flex;gap:7px;align-items:center}.group-list small{opacity:.75}.group-error{color:#d94848}
</style>
