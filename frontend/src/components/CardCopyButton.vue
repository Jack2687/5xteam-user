<script setup>
import { onBeforeUnmount, ref } from 'vue'
import { Copy, Check } from 'lucide-vue-next'
import { api } from '../api'
import { copyText } from '../clipboard'
import { cardCopyText } from '../card-copy'

const props = defineProps({ issuer: String, cardId: String, secret: Object })
const emit = defineEmits(['error'])
const copying = ref(false), copied = ref(false)
let timer, disposed = false
onBeforeUnmount(() => { disposed = true; clearTimeout(timer) })
async function copyCard() {
  if (copying.value) return
  copying.value = true
  copied.value = false
  clearTimeout(timer)
  try {
    const secret = props.secret || await api(`/api/${props.issuer}/cards/${encodeURIComponent(props.cardId)}/private`, { method: 'POST', body: {}, cache: 'no-store' })
    if (disposed) return
    await copyText(cardCopyText(secret))
    if (disposed) return
    copied.value = true
    timer = setTimeout(() => { copied.value = false }, 2000)
  } catch (error) {
    if (!disposed) emit('error', error.message)
  } finally {
    copying.value = false
  }
}
</script>

<template>
  <button type="button" class="btn ghost compact card-copy-button" :disabled="copying" title="复制卡号----MM/YY----CVV" @click.stop="copyCard">
    <Check v-if="copied" :size="14"/><Copy v-else :size="14"/>
    {{ copying ? '复制中…' : copied ? '已复制' : '复制卡密' }}
  </button>
</template>

<style scoped>
.card-copy-button { white-space: nowrap; }
</style>
