// Use the existing single-account ownership endpoint for every selected mailbox.
// Bounded workers keep large, cross-page selections from flooding the server.
export async function moveMailAccountsToPro(emails, request, onProgress = () => {}) {
  const targets = [...new Set(emails.map(email => String(email || '').trim().toLowerCase()).filter(Boolean))]
  const results = new Array(targets.length)
  let next = 0, completed = 0
  const worker = async () => {
    while (next < targets.length) {
      const index = next++, email = targets[index]
      try {
        await request(`/api/mail/accounts/${encodeURIComponent(email)}/management-scope`, { method: 'PUT', body: { scope: 'pro' } })
        results[index] = { email, success: true }
      } catch (error) {
        results[index] = { email, success: false, error: error?.message || '移入失败' }
      }
      onProgress(++completed, targets.length)
    }
  }
  await Promise.all(Array.from({ length: Math.min(3, targets.length) }, worker))
  return results
}
