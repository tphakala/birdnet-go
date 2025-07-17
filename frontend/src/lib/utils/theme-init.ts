/* eslint-disable @typescript-eslint/no-unnecessary-condition */
/**
 * Theme initialization script
 * This should be included in the HTML head to prevent theme flash
 *
 * Usage in your HTML template:
 * <script>
 *   // Paste this code directly in a script tag in the head
 *   (function() {
 *     const theme = localStorage.getItem('theme') ||
 *                   (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
 *     document.documentElement.setAttribute('data-theme', theme);
 *     document.documentElement.setAttribute('data-theme-controller', theme);
 *   })();
 * </script>
 */

// This function can be used if you're building the script dynamically
export function getThemeInitScript(): string {
  return `(function() {
    const theme = localStorage.getItem('theme') || 
                  (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
    document.documentElement.setAttribute('data-theme', theme);
    document.documentElement.setAttribute('data-theme-controller', theme);
  })();`;
}

// For use in Svelte app initialization
export function initializeTheme(): void {
  const stored = localStorage.getItem('theme');
  let theme: 'light' | 'dark' = 'light';

  if (stored === 'dark' || stored === 'light') {
    theme = stored;
  } else if (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) {
    theme = 'dark';
  }

  document.documentElement.setAttribute('data-theme', theme);
  document.documentElement.setAttribute('data-theme-controller', theme);
  localStorage.setItem('theme', theme);
}
