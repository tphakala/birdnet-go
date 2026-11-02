import type { Attachment } from 'svelte/attachments';

/**
 * Attachment for content that can disappear while it holds focus, such as an error
 * alert whose Retry succeeded, that was dismissed, or that a background refresh cleared.
 * Removing the focused element drops focus to the document body; when that happens, focus
 * moves to `target()` instead, the first one that can take it when several are given.
 *
 * The node counts as holding focus from a focusin, or a pointerdown (Safari and iPadOS do
 * not focus a clicked button, so the user's place is where they pressed), until focus
 * moves to another element. Focus that left for nothing (a click on plain page content)
 * still counts, so removal then moves focus to the target, which is near where the user
 * was.
 */
export function keepFocusOnRemoval(
  target: () => Array<HTMLElement | null | undefined>
): Attachment<HTMLElement> {
  return node => {
    let holdsFocus = false;
    const onFocusIn = () => (holdsFocus = true);
    const onFocusOut = (event: FocusEvent) => {
      if (event.relatedTarget instanceof Node && !node.contains(event.relatedTarget)) {
        holdsFocus = false;
      }
    };
    node.addEventListener('focusin', onFocusIn);
    node.addEventListener('pointerdown', onFocusIn);
    node.addEventListener('focusout', onFocusOut);
    return () => {
      node.removeEventListener('focusin', onFocusIn);
      node.removeEventListener('pointerdown', onFocusIn);
      node.removeEventListener('focusout', onFocusOut);
      if (!holdsFocus) return;
      // Svelte runs this after the node left the document
      queueMicrotask(() => {
        if (document.activeElement && document.activeElement !== document.body) return;
        for (const el of target()) {
          if (!el?.isConnected) continue;
          el.focus();
          if (document.activeElement === el) return;
        }
      });
    };
  };
}
