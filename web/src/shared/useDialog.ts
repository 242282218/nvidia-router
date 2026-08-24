import { onBeforeUnmount, watch, type Ref } from 'vue'

import { lockBodyScroll, unlockBodyScroll } from './useScrollLock'
import { useFocusTrap } from './useFocusTrap'

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
  let isLocked = false

  useFocusTrap(open, panel, onClose)

  watch(open, (isOpen) => {
    if (isOpen) {
      if (!isLocked) {
        lockBodyScroll()
        isLocked = true
      }
    } else {
      if (isLocked) {
        unlockBodyScroll()
        isLocked = false
      }
    }
  }, { immediate: true })

  onBeforeUnmount(() => {
    if (isLocked) {
      unlockBodyScroll()
      isLocked = false
    }
  })
}
