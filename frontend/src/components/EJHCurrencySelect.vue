<script setup>
import { computed, ref } from 'vue'
import { searchCurrencyOptions } from '../ejh-currencies'
defineOptions({ inheritAttrs: false })
const props = defineProps({ modelValue: { type: String, default: '' }, label: { type: String, default: '币种' } })
const emit = defineEmits(['update:modelValue'])
const query = ref('')
const filtered = computed(() => searchCurrencyOptions(props.modelValue, query.value))
const options = computed(() => filtered.value.options)
function choose(event) { emit('update:modelValue', event.target.value); query.value = '' }
</script>

<template>
 <span class="currency-select">
  <input v-model="query" type="search" :aria-label="'搜索' + label" :disabled="$attrs.disabled" placeholder="搜索代码 / 中文名称 / 国家地区" autocomplete="off" @keydown.enter.prevent @keydown.esc.stop="query = ''" />
  <select v-bind="$attrs" :aria-label="label" :value="modelValue" @change="choose">
    <option value="">请选择币种</option>
    <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
  </select>
  <small v-if="query" role="status">{{ filtered.matchCount ? '找到 ' + filtered.matchCount + ' 个币种，请选择' : '没有匹配币种，已保留原选择' }}</small>
 </span>
</template>
<style scoped>
.currency-select{display:grid;gap:8px;min-width:0;width:100%}.currency-select input,.currency-select select{width:100%;min-width:0;max-width:100%}.currency-select input{font-size:12px}.currency-select small{font-size:11px;color:var(--muted);line-height:1.6;white-space:normal}
</style>
