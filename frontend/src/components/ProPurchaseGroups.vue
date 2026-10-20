<script setup>
import { computed, ref } from 'vue'

const props = defineProps({
  modelValue: { type: Array, default: () => [] },
  groups: { type: Array, default: () => [] },
  baseIds: { type: Array, default: () => [] },
  baseNames: { type: Array, default: () => [] },
  loading: Boolean, error: String, enabled: Boolean,
})
const emit = defineEmits(['update:modelValue', 'load', 'refresh'])
const search = ref('')
const name = id => props.groups.find(g => Number(g.id) === id)?.name || props.baseNames[props.baseIds.indexOf(id)] || `#${id}`
const selected = computed(() => props.modelValue.map(name).join('、'))
const effective = computed(() => [...new Set([...props.baseIds, ...props.modelValue])].map(name).join('、'))
const filtered = computed(() => props.groups.filter(g => `${g.name} ${g.id}`.toLowerCase().includes(search.value.trim().toLowerCase())))
function toggle(id, checked) {
  emit('update:modelValue', checked ? [...new Set([...props.modelValue, id])] : props.modelValue.filter(v => v !== id))
}
</script>

<template>
  <details class="purchase-groups" @toggle="event => { if (event.target.open && enabled) emit('load') }">
    <summary>额外推送分组 <span>{{ selected || '不额外添加' }}</span></summary>
    <div class="group-tools">
      <input v-model="search" type="search" placeholder="搜索 Sub2 分组" aria-label="搜索额外推送分组" :disabled="!enabled" />
      <button class="btn ghost compact" type="button" :disabled="loading || !enabled" @click="emit('refresh')">{{ loading ? '读取中…' : '刷新分组' }}</button>
      <button v-if="modelValue.length" class="btn ghost compact" type="button" @click="emit('update:modelValue', [])">清空额外选择</button>
    </div>
    <p v-if="!enabled" class="muted">请先在推送设置中选择并保存 Sub2。</p>
    <p v-else-if="error" class="danger-text" role="alert">{{ error }}</p>
    <div v-if="enabled" class="group-options">
      <label v-for="group in filtered" :key="group.id">
        <input type="checkbox" :checked="baseIds.includes(Number(group.id)) || modelValue.includes(Number(group.id))" :disabled="baseIds.includes(Number(group.id))" @change="toggle(Number(group.id), $event.target.checked)" />
        <span>{{ group.name }}</span><small>{{ baseIds.includes(Number(group.id)) ? '原分组' : `#${group.id}` }}</small>
      </label>
      <p v-if="!loading && !error && !filtered.length" class="muted">暂无匹配分组</p>
    </div>
    <p>实际推送：{{ effective || '沿用推送设置' }}。留空沿用原分组；所选分组会额外加入，同一账号只推送一次。</p>
  </details>
</template>

<style scoped>
.purchase-groups{min-width:0;margin:12px 0;border:1px solid var(--line);border-radius:6px;padding:10px 12px;font-size:12px}
summary{cursor:pointer;font-weight:600;overflow-wrap:anywhere}summary span{margin-left:10px;font-weight:400;color:var(--muted)}
.group-tools{display:flex;gap:8px;flex-wrap:wrap;margin-top:12px}.group-tools input{flex:1;min-width:140px;width:0;border:1px solid var(--line);border-radius:6px;background:var(--bg-elevated);color:var(--text);padding:8px}
.group-options{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:6px;max-height:180px;overflow:auto;margin-top:10px}
.group-options label{display:flex;align-items:center;gap:8px;padding:7px;min-width:0;cursor:pointer}.group-options input{width:16px;height:16px;min-height:16px;margin:0;flex-shrink:0}.group-options span{overflow-wrap:anywhere}.group-options small{margin-left:auto;white-space:nowrap;color:var(--muted)}
p{margin:10px 0 0;line-height:1.6;color:var(--muted);overflow-wrap:anywhere}p.danger-text{color:var(--danger,#dc2626)}
</style>
