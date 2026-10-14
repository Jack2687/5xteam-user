<script setup>
import { computed } from 'vue'
import { returnStatus, returnStatusLabels } from '../ejh'
const props = defineProps({ state: { type: String, default: 'unopened' } })
const status = computed(() => returnStatus(props.state))
</script>
<template><span class="return-status" :class="'return-status--' + status" :data-return-state="status"><i aria-hidden="true"></i><slot>{{ returnStatusLabels[status] }}</slot></span></template>
<style scoped>
.return-status{display:inline-flex;align-items:center;gap:6px;padding:5px 9px;border-radius:6px;font-size:11px;line-height:1.4;font-weight:600;white-space:nowrap;background:var(--surface-2);color:var(--muted)}
.return-status i{height:6px;width:6px;flex-shrink:0;border-radius:50%;background:currentColor}
.return-status--opened{color:var(--blue);background:var(--blue-bg)}
.return-status--waiting,.return-status--unverified,.return-status--unmatched{color:var(--amber);background:var(--amber-bg)}
.return-status--returned{color:var(--green-strong);background:var(--green-bg)}
</style>
