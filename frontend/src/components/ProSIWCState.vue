<script setup>
import {computed, ref} from 'vue'
import {api} from '../api'
const props=defineProps({account:{type:Object,required:true}})
const emit=defineEmits(['updated'])
const busy=ref(false),error=ref('')
const label=computed(()=>({queued:'等待转入',authorizing:'获取凭证',pushing:'推送中',warming:'等待运行确认',active:'SIWC 运行中',failed:'转入失败',ended:'SIWC 已退出'}[props.account.pro_siwc?.status]||'SIWC'))
async function act(action){if(action==='end'&&!window.confirm('结束转入会停止此账号在 SIWC 的服务并解除补货暂停，原推送超时继续生效。确定？'))return;busy.value=true;try{await api(`/api/pro-siwc/accounts/${encodeURIComponent(props.account.email)}`,{method:'POST',body:{action}});emit('updated')}catch(e){error.value=e.message}finally{busy.value=false}}
</script>
<template><div v-if="account.pro_siwc" class="siwc-state"><strong>{{label}}</strong><small v-if="account.pro_siwc.status==='warming'">推送后等待 10 秒及监控确认</small><details v-if="error||account.pro_siwc.error"><summary>查看原因</summary>{{error||account.pro_siwc.error}}</details><template v-if="!['active','ended'].includes(account.pro_siwc.status)"><button type="button" :disabled="busy" @click="act('retry')">重试</button><button type="button" :disabled="busy" @click="act('end')">结束转入</button></template></div></template>
<style scoped>.siwc-state{font-size:11px;margin-top:7px;color:var(--text-2)}.siwc-state small{display:block}.siwc-state details{max-width:240px;white-space:normal;overflow-wrap:anywhere}.siwc-state button{font-size:11px;margin:6px 8px 0 0;color:var(--green-strong)}</style>
