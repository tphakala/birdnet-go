/**
 * Global styles that browser-mode tests need but do not load (the browser test
 * config builds no Tailwind CSS): Tailwind's sr-only rule, without which visually
 * hidden text renders and widens its control, and no transitions, so a style read
 * right after an interaction sees the end value instead of a mid-transition one.
 *
 * Returns the injected element; remove it after the test.
 */
export function injectTestStyles(): HTMLStyleElement {
  const style = document.createElement('style');
  style.textContent = [
    '.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border-width: 0; }',
    '* { transition: none !important; }',
  ].join('\n');
  document.head.appendChild(style);
  return style;
}
