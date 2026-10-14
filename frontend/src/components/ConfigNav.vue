<script setup>
defineProps({ modelValue: String, items: Array, label: { type: String, default: '配置分组' } })
defineEmits(['update:modelValue'])
</script>
<template>
  <nav class="config-nav ops-config-nav" :aria-label="label">
    <button v-for="(item, index) in items" :key="item[0]" type="button" :class="{ active: modelValue === item[0] }" :aria-current="modelValue === item[0] ? 'true' : undefined" @click="$emit('update:modelValue', item[0])">
      <span class="config-nav-index ops-config-nav-index">{{ String(index + 1).padStart(2, '0') }}</span><span><strong>{{ item[1] }}</strong><small>{{ item[2] }}</small></span>
    </button>
    <p>切换仅浏览分组，修改后请保存。</p>
  </nav>
</template>
<style src="../config-layout.css"></style>
