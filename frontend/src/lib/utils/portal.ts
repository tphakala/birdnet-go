/**
 * Svelte action that portals an element to a target container (default: document.body).
 * Useful for dropdowns, tooltips, and popups that need to escape stacking contexts
 * created by parent transforms, filters, or overflow clipping.
 */
export function portal(
  node: HTMLElement,
  target: HTMLElement = document.body
): { destroy: () => void } {
  target.appendChild(node);

  return {
    destroy() {
      // Only remove if still attached to the target
      if (node.parentNode === target) {
        target.removeChild(node);
      }
    },
  };
}
