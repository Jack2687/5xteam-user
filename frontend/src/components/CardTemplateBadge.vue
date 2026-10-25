<script setup>
defineProps({origin:{type:Object,required:true}})
</script>
<template><div class="card-template"><span :class="['template-name',origin.platform]">{{origin.name}}</span><small>{{origin.platform.toUpperCase()}}</small></div></template>
<style scoped>
.card-template{display:grid;gap:5px;justify-items:start;margin:6px 0;min-width:110px}.template-name{font-size:12px;padding:4px 8px;border-radius:5px;background:var(--blue-bg);color:var(--blue);overflow-wrap:anywhere}.template-name.cc{background:var(--amber-bg);color:var(--amber)}small{font-size:10px;color:var(--muted)}
</style>
