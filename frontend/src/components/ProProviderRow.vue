<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '../api'
import { catalogProPlans, paymentCountries, paymentCountryLabel, payProviderName, tokenseekCatalog } from '../gptpay'

const props = defineProps({ row: { type: Object, required: true }, selected: String, backup: String, savedProvider: String, savedBackup: String, savedVersion: Number, disabled: Boolean })
const emit = defineEmits(['role'])
const expanded = ref(false), search = ref(''), catalog = ref(null), loading = ref(false), error = ref('')
const savedURL = ref(props.row.url)
let timer, controller, generation = 0, disposed = false
const name = computed(() => payProviderName(props.row.provider))
const role = computed(() => props.selected === props.row.provider ? 'primary' : props.backup === props.row.provider ? 'backup' : 'disabled')
const savedRole = computed(() => props.savedProvider === props.row.provider ? 'primary' : props.savedBackup === props.row.provider ? 'backup' : 'disabled')
const plans = computed(() => catalogProPlans(catalog.value, props.row.provider))
const groups = computed(() => ['普通订阅', 'Plus 升级', 'Codex 点数'].map(label => ({ label, plans: plans.value.filter(p => p.category === label) })).filter(g => g.plans.length))
const countries = computed(() => paymentCountries(props.row.provider, catalog.value, props.row.plan_code))
const filteredCountries = computed(() => {
  const terms = search.value.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)
  return countries.value.filter(c => terms.every(term => `${c.name} ${c.code} ${c.currency || ''}`.toLocaleLowerCase().includes(term)))
})
const country = computed(() => countries.value.find(c => c.code === props.row.country))
const plan = computed(() => plans.value.find(p => p.code === props.row.plan_code))
const credentialsChanged = computed(() => !!props.row.api_key.trim() || props.row.url.trim().replace(/\/$/, '') !== savedURL.value?.replace(/\/$/, ''))
const selectionWarning = computed(() => catalog.value && (!country.value || !plan.value?.available))
const pickerDisabled = computed(() => props.disabled || loading.value || (props.row.provider === 'cmsnav' && !catalog.value))

async function loadCatalog() {
  clearTimeout(timer); controller?.abort()
  const id = ++generation
  catalog.value = null; error.value = ''; loading.value = false
  if (disposed) return
  if (credentialsChanged.value || (props.row.provider !== 'cmsnav' && !props.row.key_present)) {
    error.value = '保存地址和密钥后可读取账户报价。'; return
  }
  loading.value = true
  const request = controller = new AbortController()
  try {
    const provider = props.row.provider, params = new URLSearchParams({ provider })
    const result = await api(provider === 'tokenseek' ? '/api/gptpay/account?provider=tokenseek' : '/api/gptpay/catalog?' + params, { signal: request.signal })
    if (!disposed && id === generation) catalog.value = provider === 'tokenseek' ? tokenseekCatalog(result) : result
  } catch (e) {
    if (!disposed && id === generation && !request.signal.aborted) error.value = e.message
  } finally {
    if (!disposed && id === generation) loading.value = false
  }
}
watch(() => props.savedVersion, () => { savedURL.value = props.row.url })
watch(() => [props.row.url, props.row.api_key, props.row.key_present, props.savedVersion], () => {
  clearTimeout(timer); controller?.abort(); generation++
  catalog.value = null; error.value = ''; loading.value = false
  timer = setTimeout(loadCatalog, 250)
}, { immediate: true })
function changePlan() {
  props.row.credit_quantity = props.row.plan_code === 'codex_point' ? props.row.credit_quantity || 250 : 0
}
onBeforeUnmount(() => { disposed = true; clearTimeout(timer); controller?.abort(); generation++ })
</script>

<template>
  <tr class="provider-row" :class="{ chosen: role === 'primary', standby: role === 'backup' }" :data-provider="row.provider">
    <td class="provider-identity">
      <strong>{{ name }}</strong>
      <small class="provider-url" :title="row.url">{{ row.url }}</small>
      <small>{{ row.api_key ? '密钥待保存' : row.key_present ? '密钥已配置' : '尚未配置密钥' }}<template v-if="['cmsnav','xupay'].includes(row.provider)"> · {{ row.terms_accepted ? '条款已确认' : '条款未确认' }}</template></small>
    </td>
    <td class="provider-plan">
      <select v-model="row.plan_code" :aria-label="name + ' 开通套餐'" :disabled="pickerDisabled" @change="changePlan">
        <optgroup v-for="group in groups" :key="group.label" :label="group.label"><option v-for="option in group.plans" :key="option.code" :value="option.code" :disabled="catalog && !option.available">{{ option.label }}</option></optgroup>
      </select>
      <label v-if="row.plan_code === 'codex_point'" class="points"><span>点数数量</span><input v-model.number="row.credit_quantity" type="number" min="250" max="250000" step="250" required :disabled="disabled" :aria-label="name + ' Codex 点数数量'" /></label>
      <small v-if="row.plan_code.endsWith('_up')">仅限已订阅 Plus 的账号</small>
      <small v-if="row.plan_code === 'codex_point'">需已有有效订阅；250 的倍数</small>
    </td>
    <td class="provider-country">
      <input v-if="countries.length > 8" v-model="search" type="search" :aria-label="name + ' 搜索支付国家'" placeholder="搜索国家 / 币种" :disabled="pickerDisabled" @keydown.enter.prevent />
      <select v-model="row.country" :aria-label="name + ' 支付国家'" :disabled="pickerDisabled" required>
        <option v-if="!filteredCountries.some(c => c.code === row.country)" :value="row.country">当前：{{ country ? `${country.name} · ${country.code}` : paymentCountryLabel(row.country) }}</option>
        <option v-for="option in filteredCountries" :key="option.code" :value="option.code">{{ option.name }} · {{ option.code }}{{ option.currency ? ' · ' + option.currency : '' }}</option>
      </select>
      <small v-if="search && !filteredCountries.length">没有匹配项，当前选择保留</small>
      <small v-if="country?.currency">结算币种 {{ country.currency }}</small>
    </td>
    <td class="provider-state">
      <select :value="role" :disabled="disabled" :aria-label="name + ' 启用状态'" @change="emit('role', $event.target.value)">
        <option value="primary">主启用</option><option value="backup" :disabled="role === 'primary'">备启用</option><option value="disabled" :disabled="role === 'primary'">未启用</option>
      </select>
      <small v-if="role !== savedRole">待保存生效</small>
    </td>
    <td class="provider-actions"><button class="btn ghost compact" type="button" :aria-expanded="expanded" :aria-controls="'provider-detail-' + row.provider" @click="expanded = !expanded">{{ expanded ? '收起' : '配置' }}</button></td>
  </tr>
  <tr v-if="expanded" class="provider-detail"><td colspan="5">
    <div :id="'provider-detail-' + row.provider" class="provider-detail-body">
      <div class="provider-fields">
        <label class="field"><span>API 地址</span><input v-model="row.url" required :disabled="disabled" :aria-label="name + ' API 地址'" /></label>
        <label class="field"><span>API Key</span><input v-model="row.api_key" type="password" autocomplete="new-password" :disabled="disabled" :aria-label="name + ' API Key'" :placeholder="row.key_present ? '已保存，留空保留原密钥' : '填写此供应商的 API Key'" /></label>
      </div>
      <label v-if="['cmsnav','xupay'].includes(row.provider)" class="provider-terms"><input v-model="row.terms_accepted" type="checkbox" :disabled="disabled" :aria-label="name + ' 接受条款'" /><span>我已阅读并接受 <a href="https://openai.com/policies/terms-of-use/" target="_blank" rel="noopener noreferrer">OpenAI 使用条款</a>，用于 {{ name }} 开通。</span></label>
      <p v-if="row.provider === 'xupay'">使用序付开发者中心的 API Key；银行卡沿用 Space 所选卡片，账单由序付后台抽取。</p>
      <div class="provider-catalog" aria-live="polite">
        <span v-if="loading">正在读取国家与套餐报价…</span>
        <span v-else-if="error" class="danger-text">{{ error }} 当前已选配置保留。</span>
        <span v-else-if="catalog">已读取 {{ countries.length }} 个支付地区。<span v-if="selectionWarning" class="danger-text">当前套餐或地区不在可用目录，请核对。</span></span>
        <button type="button" class="btn ghost compact" :disabled="disabled || loading" @click="loadCatalog">刷新国家与套餐</button>
        <button v-if="row.provider === 'tokenseek' && plan" class="btn ghost compact" type="button" :disabled="pickerDisabled" @click="row.country = plan.defaultCountry">使用套餐默认国家（{{ plan.defaultCountry }}）</button>
      </div>
    </div>
  </td></tr>
</template>

<style scoped>
.provider-row td{padding:16px 12px;vertical-align:top;white-space:normal}.provider-row.chosen{background:var(--green-bg)}.provider-row.standby{background:var(--blue-bg)}
.provider-row small{display:block;color:var(--muted);font-size:11px;line-height:1.6;margin-top:6px}.provider-identity strong{font-size:13px}.provider-url{max-width:240px;overflow-wrap:anywhere}
.provider-row select,.provider-row input:not([type=radio]){width:100%;min-width:0;padding:9px 10px;border:1px solid var(--line);border-radius:7px;background:var(--surface);color:var(--text);font-size:12px}.provider-identity{width:28%;min-width:185px}.provider-plan{width:27%;min-width:210px}.provider-country{width:25%;min-width:185px}.provider-country input{margin-bottom:6px}
.provider-state{min-width:114px}.chosen .provider-state{color:var(--green-strong);font-weight:600}
.provider-actions{width:70px}.provider-actions button{white-space:nowrap}.points{display:block;font-size:11px;margin-top:9px}.points input{margin-top:5px}
.provider-detail>td{padding:0;white-space:normal}.provider-detail-body{padding:18px 20px 20px;border-bottom:1px solid var(--line);background:var(--surface-2)}.provider-fields{display:grid;grid-template-columns:1fr 1fr;gap:16px}.provider-detail-body .field{margin:0}
.provider-terms{display:flex;align-items:flex-start;gap:8px;font-size:12px;line-height:1.7;margin-top:16px}.provider-terms input{width:15px;height:15px;margin-top:3px;flex-shrink:0}.provider-terms a{color:var(--green-strong);text-decoration:underline}
.provider-detail-body p,.provider-catalog{font-size:12px;color:var(--muted);line-height:1.7;margin-top:12px}.provider-catalog{display:flex;align-items:center;flex-wrap:wrap;gap:12px}
@media(max-width:720px){.provider-fields{grid-template-columns:1fr}}
</style>
