<script setup>
import { computed } from 'vue'
import { currencyOptions } from '../ejh-currencies'
const props = defineProps({ modelValue: { type: String, default: '' } })
const emit = defineEmits(['update:modelValue'])
const options = computed(() => currencyOptions(props.modelValue))
</script>

<template>
  <select :value="modelValue" @change="emit('update:modelValue', $event.target.value)">
    <option value="">请选择币种</option>
    <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
  </select>
</template>
