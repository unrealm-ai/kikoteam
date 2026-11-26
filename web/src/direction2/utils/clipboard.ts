export async function copyText(
  text: string,
  visibleField?: HTMLInputElement | HTMLTextAreaElement | null
): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // Fall back to DOM selection + execCommand when Clipboard API is unavailable or blocked.
    }
  }
  const previousFocus = document.activeElement;
  const field = visibleField ?? document.createElement('textarea');
  if (!visibleField) {
    field.value = text;
    field.readOnly = true;
    field.style.cssText = 'position:fixed;top:0;left:0;opacity:0;pointer-events:none';
    const mountParent =
      (previousFocus instanceof Element && previousFocus.closest('.n-modal, .n-drawer, [role="dialog"]')) ||
      document.body;
    mountParent.append(field);
  }
  try {
    if (!field.isConnected || field.value !== text) throw new Error('Copy field changed');
    field.focus({ preventScroll: true });
    field.select();
    field.setSelectionRange?.(0, text.length);
    if (visibleField && (document.activeElement !== field || field.selectionStart !== 0 || field.selectionEnd !== text.length)) {
      throw new Error('Copy field could not be selected');
    }
    if (!document.execCommand('copy')) throw new Error('Unable to copy text');
  } finally {
    if (!visibleField) {
      field.remove();
      if (previousFocus instanceof HTMLElement) previousFocus.focus({ preventScroll: true });
    }
  }
}
