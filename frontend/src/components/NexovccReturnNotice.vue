<script setup>
defineProps({notice:{type:Object,default:null}})
</script>
<template>
 <small v-if="notice" class="return-notice" :title="notice.detail">{{notice.text}}</small>
</template>
<style scoped>
.return-notice{display:block;color:#91651d;font-size:10px;line-height:1.6;margin-top:7px;max-width:260px;white-space:normal}
</style>
