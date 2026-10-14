import { nextTick, ref } from 'vue'

// Presentation state only: navigating a section must not change saved settings.
export function useConfigSections(initial) {
  const configSection = ref(initial)
  let revealing = false
  function revealConfigInvalid(event) {
    if (revealing) return
    event.preventDefault()
    revealing = true
    const section = event.target.closest('[data-config-section]')
    if (section) configSection.value = section.dataset.configSection
    nextTick(() => {
      event.target.focus()
      event.target.reportValidity()
      revealing = false
    })
  }
  return { configSection, revealConfigInvalid }
}
