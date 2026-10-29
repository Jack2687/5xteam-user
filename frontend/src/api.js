export async function api(path, options = {}) {
  const request = { ...options }
  const timeoutMs = request.timeoutMs
  delete request.timeoutMs
  const parentSignal = request.signal
  const controller = timeoutMs > 0 ? new AbortController() : null
  let timedOut = false, timer
  const abort = () => controller?.abort()
  if (controller) {
    request.signal = controller.signal
    if (parentSignal?.aborted) abort()
    else parentSignal?.addEventListener('abort', abort, { once: true })
    timer = setTimeout(() => { timedOut = true; controller.abort() }, timeoutMs)
  }
  try {
    if (request.body && typeof request.body !== 'string') request.body = JSON.stringify(request.body)
    const response = await fetch(path, {
      ...request,
      headers: { 'Content-Type': 'application/json', ...(request.headers || {}) },
    })
    let payload
    try {
      payload = await response.json()
    } catch {
      if (request.signal?.aborted) throw new Error('请求已取消')
      throw new Error(`服务响应异常（HTTP ${response.status}）`)
    }
    if (!response.ok || !payload.ok) { const error = new Error(payload.error || `请求失败（HTTP ${response.status}）`); error.status = response.status; throw error }
    return payload.data
  } catch (error) {
    if (timedOut) throw new Error('读取数据超时，请重试；已显示的数据会保留')
    throw error
  } finally {
    clearTimeout(timer)
    parentSignal?.removeEventListener('abort', abort)
  }
}

export async function downloadFile(path, fallbackName = 'download.json', options = {}) {
  const request = { ...options }
  if (request.body && typeof request.body !== 'string') request.body = JSON.stringify(request.body)
  const response = await fetch(path, {
    ...request,
    credentials: 'same-origin',
    cache: 'no-store',
    headers: request.body
      ? { 'Content-Type': 'application/json', ...(request.headers || {}) }
      : request.headers,
  })
  if (!response.ok) {
    let message = `请求失败 (${response.status})`
    try {
      const payload = await response.json()
      message = payload?.error || message
    } catch {}
    throw new Error(message)
  }
  const blob = await response.blob()
  return saveDownload(blob, response.headers, fallbackName)
}

export function saveDownload(blob, headers, fallbackName) {
  const disposition = headers.get('Content-Disposition') || ''
  const match = disposition.match(/filename="?([^";]+)"?/i)
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = match?.[1] || fallbackName
  document.body.appendChild(link)
  link.click()
  link.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  return { filename: link.download, headers }
}
