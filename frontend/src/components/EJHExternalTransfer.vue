<script setup>
import { computed, nextTick, ref } from 'vue'
import { Pencil } from 'lucide-vue-next'
import { api } from '../api'
import { formatTime } from '../utils'

const props = defineProps({ ledger: { type: Object, required: true }, disabled: Boolean, masked: Boolean })
const emit = defineEmits(['saved'])
const editing = ref(false), saving = ref(false), draft = ref(''), note = ref(''), error = ref(''), amountInput = ref(null)
const valid = computed(() => (draft.value.trim() === '' || /^\d{1,12}(\.\d{1,2})?$/.test(draft.value.trim())) && Array.from(note.value.trim()).length <= 500)
async function edit() {
  if (props.disabled || saving.value) return
  draft.value = props.ledger.external_transfer_amount || ''
  note.value = props.ledger.external_transfer_note || ''
  error.value = ''; editing.value = true
  await nextTick(); amountInput.value?.focus()
}
async function save() {
  if (props.disabled || saving.value || !valid.value) return
  saving.value = true; error.value = ''
  try {
    const result = await api('/api/ejh/external-transfer', { method: 'PUT', body: { amount: draft.value.trim(), note: note.value.trim() } })
    emit('saved', result); editing.value = false
  } catch (e) { error.value = e.message }
  finally { saving.value = false }
}
</script>

<template>
 <section class="external-transfer" aria-label="平台外转出">
  <div class="transfer-heading"><span>平台外转出 · 累计</span><button v-if="!editing" type="button" :disabled="disabled||saving" aria-label="修改平台外转出 USD" @click="edit"><Pencil :size="12"/>修改</button></div>
  <form v-if="editing" @submit.prevent="save">
   <label class="amount-input"><input ref="amountInput" v-model="draft" type="text" inputmode="decimal" maxlength="15" placeholder="累计外部消费金额" aria-label="平台外转出金额 USD" :disabled="saving" :aria-invalid="!valid"/><span>USD</span></label>
   <textarea v-model="note" rows="2" maxlength="500" placeholder="备注：资金用途（选填）" aria-label="平台外转出备注" :disabled="saving"/>
   <small>填写累计总额，不是本次增加额；填 0 或留空可清除扣减。</small>
   <small v-if="!valid" class="danger-text">金额须非负、最多两位小数；备注最多 500 字。</small>
   <p v-if="error" role="alert" class="danger-text">{{error}}</p>
   <div class="transfer-actions"><button class="btn primary compact" type="submit" :disabled="disabled||saving||!valid">{{saving?'保存中…':'保存'}}</button><button class="btn ghost compact" type="button" :disabled="saving" @click="editing=false">取消</button></div>
  </form>
  <template v-else>
   <div class="transfer-amount"><strong>{{masked?'---':ledger.external_transfer_amount||'0.00'}}</strong><span>USD</span></div>
   <small>平台外消费，从清算成本扣除；仅记账，不发起转账。</small>
   <p v-if="!masked&&ledger.external_transfer_note" class="transfer-note">{{ledger.external_transfer_note}}</p>
   <small v-if="!masked&&ledger.external_transfer_updated_at">{{formatTime(ledger.external_transfer_updated_at)}} 更新</small>
  </template>
 </section>
</template>

<style scoped>
.external-transfer{margin-top:18px;padding-top:14px;border-top:1px solid var(--line);font-variant-numeric:tabular-nums}.transfer-heading{display:flex;align-items:center;justify-content:space-between;gap:8px;color:var(--muted);font-size:12px}.transfer-heading button{display:inline-flex;align-items:center;gap:4px;color:var(--green-strong);font-size:11px}.transfer-heading button:disabled{opacity:.5}.transfer-amount{display:flex;align-items:baseline;gap:6px;margin:9px 0 6px}.transfer-amount strong{font-size:21px;font-weight:600;overflow-wrap:anywhere}.transfer-amount span,.amount-input span{font-size:11px;color:var(--muted)}.external-transfer small{display:block;font-size:10px;line-height:1.6;color:var(--muted)}.external-transfer form{margin-top:10px}.amount-input{display:flex;align-items:center;gap:8px}.amount-input input{width:100%;min-width:0;padding:8px 10px;font-size:16px}.external-transfer textarea{width:100%;box-sizing:border-box;margin:8px 0;resize:vertical;font-size:12px;padding:8px 10px}.transfer-actions{display:flex;gap:8px;margin-top:10px}.external-transfer p{font-size:11px;line-height:1.6;margin:6px 0;overflow-wrap:anywhere}.external-transfer .danger-text{color:var(--red)}.transfer-note{white-space:pre-wrap;color:var(--muted)}
</style>
