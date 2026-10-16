<script setup>
import { computed } from 'vue'
import { cardUsage } from '../card-status'
import StatusPill from './StatusPill.vue'
const props = defineProps({ card: { type: Object, required: true }, compact: Boolean })
const usage = computed(() => cardUsage(props.card))
</script>

<template>
  <span class="payment-card-status" :title="compact?usage.detail:undefined"><StatusPill :tone="usage.tone">{{ usage.label }}</StatusPill><small v-if="!compact">{{ usage.detail }}</small></span>
</template>

<style scoped>
.payment-card-status { display: inline-flex; flex-direction: column; align-items: flex-start; gap: 5px; }
.payment-card-status small { color: var(--muted); font-size: 11px; white-space: normal; }
</style>
