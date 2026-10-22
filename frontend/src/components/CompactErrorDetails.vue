<script setup>
import { computed } from 'vue'
const props = defineProps({ messages: { type: Array, default: () => [] }, label: { type: String, default: '异常详情' } })
const items = computed(() => [...new Set(props.messages.map(v => String(v || '').trim()).filter(Boolean))])
</script>
<template>
  <details v-if="items.length" class="compact-error-details">
    <summary>{{ label }}<span v-if="items.length > 1">（{{ items.length }}）</span></summary>
    <div><p v-for="text in items" :key="text">{{ text }}</p></div>
  </details>
</template>
<style scoped>
.compact-error-details{width:100%;max-width:320px;min-width:0;font-size:11px;color:var(--red);white-space:normal}
summary{cursor:pointer;line-height:1.6;width:fit-content}
.compact-error-details>div{max-height:160px;overflow:auto;margin-top:4px;padding:6px 8px;background:var(--surface-2);border:1px solid var(--line);border-radius:4px}
p{margin:0;white-space:pre-wrap;overflow-wrap:anywhere;word-break:break-word;line-height:1.6}p+p{margin-top:6px}
</style>
