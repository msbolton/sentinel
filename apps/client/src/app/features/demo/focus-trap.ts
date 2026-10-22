/** Keeps Tab and Shift+Tab inside `root` (a dialog) by wrapping from the last focusable to the first and back. */
export function trapTab(event: KeyboardEvent, root: HTMLElement | null | undefined): void {
  if (event.key !== 'Tab' || !root) return;
  const items = Array.from(root.querySelectorAll<HTMLElement>('button:not([disabled]), a[href]'));
  if (items.length === 0) return;
  const first = items[0];
  const last = items[items.length - 1];
  const active = document.activeElement;
  if (event.shiftKey && active === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && active === last) {
    event.preventDefault();
    first.focus();
  }
}
