<script setup>
import { computed, ref } from 'vue'
import { api } from '../api'
import { paymentPolicy } from '../payment-only'
const props=defineProps({issuer:String,cardId:String,data:Object,disabled:Boolean})
const emit=defineEmits(['saved'])
const open=ref(false),busy=ref(false),error=ref(''),mode=ref('normal'),amount=ref(''),note=ref(''),move=ref(''),revision=ref(0)
const policy=computed(()=>paymentPolicy(props.data,props.cardId))
function edit(){const p=policy.value;mode.value=p?.mode||'normal';amount.value=p?.amount_usd||'';note.value=p?.note||'';revision.value=p?.revision||0;move.value='';error.value='';open.value=true}
async function save(){if(busy.value)return;busy.value=true;error.value='';try{await api(`/api/${props.issuer}/cards/${encodeURIComponent(props.cardId)}/payment-policy`,{method:'PUT',body:{mode:mode.value,amount_usd:amount.value,note:note.value,revision:revision.value,external_reclassified_usd:move.value}});open.value=false;emit('saved')}catch(e){error.value=e.message}finally{busy.value=false}}
</script>
<template>
 <button class="text-link" :disabled="disabled" @click="edit">{{policy?.mode==='payment_only'?'只付款 · 无需回款':'设置回款方式'}}</button>
 <Teleport to="body"><div v-if="open" class="payment-policy-overlay" @click.self="!busy&&(open=false)"><form class="payment-policy-panel" @submit.prevent="save">
  <h3>卡片回款方式</h3><p>作用于本卡关联的付款记录。保留真实余额与流水，Pro 合并、删除和 API 商户流程不变。</p>
  <label>回款方式<select v-model="mode" :disabled="busy"><option value="normal">正常回款</option><option value="payment_only">只付款，无需回款</option></select></label>
  <template v-if="mode==='payment_only'"><label>付款本金备用金额（USD）<input v-model="amount" inputmode="decimal" pattern="[0-9]{1,12}(\.[0-9]{1,2})?" placeholder="留空，优先使用实际付款记录" :disabled="busy"/></label><p>仅在付款资料不足时使用，填写累计原始本金，已识别的退款会自动扣减。不要填写开卡额度或包含手续费的金额。</p>
   <label v-if="issuer==='ejh'&&policy?.mode!=='payment_only'">其中已登记为平台外转出（USD）<input v-model="move" inputmode="decimal" pattern="[0-9]{1,12}(\.[0-9]{1,2})?" placeholder="未登记留空；已登记金额会从原项目移出" :disabled="busy"/></label>
  </template>
  <label>用途 / 修改说明<textarea v-model="note" :required="mode==='payment_only'" maxlength="500" :disabled="busy"/></label>
  <p v-if="error" class="danger-text" role="alert">{{error}}</p><footer><button type="button" class="btn ghost" :disabled="busy" @click="open=false">取消</button><button class="btn primary" :disabled="busy">{{busy?'保存中…':'保存'}}</button></footer>
 </form></div></Teleport>
</template>
<style scoped>
.payment-policy-overlay{position:fixed;inset:0;z-index:10050;background:#0006;display:grid;place-items:center;padding:20px}.payment-policy-panel{background:var(--surface,#fff);color:var(--text,#222);border-radius:16px;padding:24px;width:min(520px,100%);max-height:90vh;overflow:auto}.payment-policy-panel h3{margin-top:0}.payment-policy-panel p{font-size:12px;line-height:1.7;color:var(--muted,#666)}.payment-policy-panel label{display:grid;gap:8px;margin:16px 0;font-size:13px}.payment-policy-panel input,.payment-policy-panel select,.payment-policy-panel textarea{padding:10px;border:1px solid var(--line,#ddd);border-radius:8px;background:var(--bg,#fff);color:inherit}.payment-policy-panel footer{display:flex;justify-content:flex-end;gap:12px}
</style>
