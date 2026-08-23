import { nextTick, onBeforeUnmount, watch, type Ref } from 'vue'

const FOCUSABLE_SELECTOR = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

function isVisible(element: HTMLElement): boolean {
  const style = globalThis.getComputedStyle(element)
  return !element.hasAttribute('hidden') && style.display !== 'none' && style.visibility !== 'hidden'
}

function focusableElements(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(isVisible)
}

/** Keep keyboard focus inside an open surface and restore the element that opened it. */
export function useFocusTrap(open: Ref<boolean>, panel: Ref<HTMLElement | null>, onClose: () => void): void {
  let previouslyFocused: HTMLElement | null = null

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') {
      event.preventDefault()
      onClose()
      return
    }
    if (event.key !== 'Tab' || panel.value === null) return

    const focusable = focusableElements(panel.value)
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    if (first === undefined || last === undefined) return
    if (event.shiftKey && globalThis.document.activeElement === first) {
      last.focus()
      event.preventDefault()
    } else if (!event.shiftKey && globalThis.document.activeElement === last) {
      first.focus()
      event.preventDefault()
    }
  }

  watch(open, async (isOpen) => {
    if (isOpen) {
      previouslyFocused = globalThis.document.activeElement instanceof HTMLElement
        ? globalThis.document.activeElement
        : null
      globalThis.document.addEventListener('keydown', onKeydown)
      await nextTick()
      if (!open.value) return
      const panelElement = panel.value
      if (panelElement === null) return
      ;(focusableElements(panelElement)[0] ?? panelElement).focus()
      return
    }

    globalThis.document.removeEventListener('keydown', onKeydown)
    previouslyFocused?.focus()
    previouslyFocused = null
  })

  onBeforeUnmount(() => {
    globalThis.document.removeEventListener('keydown', onKeydown)
    previouslyFocused = null
  })
}
