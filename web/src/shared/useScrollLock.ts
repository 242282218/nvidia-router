let lockCount = 0
let savedOverflow = ''

export function lockBodyScroll(): void {
  if (typeof globalThis.document === 'undefined' || !globalThis.document.body) return
  if (lockCount === 0) {
    savedOverflow = globalThis.document.body.style.overflow
    globalThis.document.body.style.overflow = 'hidden'
  }
  lockCount++
}

export function unlockBodyScroll(): void {
  if (typeof globalThis.document === 'undefined' || !globalThis.document.body) return
  lockCount = Math.max(0, lockCount - 1)
  if (lockCount === 0) {
    globalThis.document.body.style.overflow = savedOverflow
  }
}
