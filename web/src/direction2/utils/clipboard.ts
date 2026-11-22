export async function copyText(text: string, visibleField?: HTMLTextAreaElement): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch (error) {
      if (!visibleField) throw error;
    }
  }
  const previousFocus = document.activeElement;
  const field = visibleField ?? document.createElement('textarea');
  if (!visibleField) {
    field.value = text;
    field.readOnly = true;
    field.style.cssText = 'position:fixed;opacity:0';
    document.body.append(field);
  }
  try {
    if (!field.isConnected || field.value !== text) throw new Error('Copy field changed');
    field.focus({ preventScroll: true });
    field.select();
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
