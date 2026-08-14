import { nextTick, onBeforeUnmount, watch, type Ref } from 'vue'

// useDialog centralises the focus/escape/scroll-lock contract for modal dialogs
// so the per-feature dialog components don't each re-implement (and get wrong) a
// piece of it (design-aesthetics dialog: focus trap, Esc close, focus return,
// background scroll lock are all required, not optional).
//
// `open` is the caller's truth for whether the dialog is rendered; `onClose` is
// what Esc should invoke (typically emit('close'), so the parent flips `open`
// and the focus-return logic below fires on the resulting false). `panel` must
// point at the dialog's focusable root once it is mounted.
export function useDialog(open: Ref<boolean>, panel: Ref<HTMLElement | null>, onClose: () => void): void {
  let previouslyFocused: HTMLElement | null = null
  let savedOverflow = ''

  function focusableElements(root: HTMLElement): HTMLElement[] {
    return Array.from(
      root.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
      ),
    )
  }

  function trapFocus(event: KeyboardEvent): void {
    if (event.key !== 'Tab' || panel.value === null) return
    const focusable = focusableElements(panel.value).filter((el) => el.offsetParent !== null)
    if (focusable.length === 0) return
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    if (first === undefined || last === undefined) return
    if (event.shiftKey && document.activeElement === first) {
      last.focus()
      event.preventDefault()
    } else if (!event.shiftKey && document.activeElement === last) {
      first.focus()
      event.preventDefault()
    }
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') {
      onClose()
      return
    }
    trapFocus(event)
  }

  function lockScroll(): void {
    savedOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
  }

  function unlockScroll(): void {
    document.body.style.overflow = savedOverflow
  }

  watch(open, async (isOpen) => {
    if (isOpen) {
      previouslyFocused = (document.activeElement as HTMLElement) ?? null
      lockScroll()
      document.addEventListener('keydown', onKeydown)
      await nextTick()
      const panelEl = panel.value
      if (panelEl !== null) {
        const focusable = focusableElements(panelEl).filter((el) => el.offsetParent !== null)
        // Focus the first field, not a destructive confirm button (dialog
        // contract: destructive dialogs focus cancel instead).
        ;(focusable[0] ?? panelEl).focus()
      }
    } else {
      document.removeEventListener('keydown', onKeydown)
      unlockScroll()
      previouslyFocused?.focus()
      previouslyFocused = null
    }
  })

  onBeforeUnmount(() => {
    document.removeEventListener('keydown', onKeydown)
    unlockScroll()
  })
}
