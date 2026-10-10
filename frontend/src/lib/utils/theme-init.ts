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
    var theme = localStorage.getItem('theme') ||
                  (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
    document.documentElement.setAttribute('data-theme', theme);
    document.documentElement.setAttribute('data-theme-controller', theme);
  })();
  (function() {
    try {
      var scheme = localStorage.getItem('color-scheme') || 'blue';
      // Stored JSON-encoded ("amber") by the app; a plain value is accepted too
      try { scheme = JSON.parse(scheme); } catch (e) {}
      var valid = ['blue','forest','amber','violet','rose','custom'];
      if (valid.indexOf(scheme) === -1) scheme = 'blue';
      document.documentElement.setAttribute('data-scheme', scheme);
      if (scheme === 'custom') {
        var raw = localStorage.getItem('custom-scheme-colors');
        if (raw) {
          var c = JSON.parse(raw);
          if (c.primary) {
            var s = document.documentElement.style;
            s.setProperty('--custom-primary', c.primary);
            s.setProperty('--custom-accent', c.accent || c.primary);
            function lum(hex) {
              var n = parseInt(hex.replace('#',''), 16);
              function lin(v) { v /= 255; return v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); }
              return 0.2126 * lin((n >> 16) & 255) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255);
            }
            var ct = function(hex) { return lum(hex) > 0.179 ? '#020617' : '#ffffff'; };
            var pc = ct(c.primary), ac = ct(c.accent || c.primary);
            s.setProperty('--custom-primary-content', pc);
            s.setProperty('--custom-accent-content', ac);
            s.setProperty('--custom-primary-shade', pc === '#ffffff' ? 'black' : 'white');
            s.setProperty('--custom-accent-shade', ac === '#ffffff' ? 'black' : 'white');
          }
        }
      }
    } catch(e) {}
  })();`;
}

// For use in Svelte app initialization
export function initializeTheme(): void {
  const stored = localStorage.getItem('theme');
  let theme: 'light' | 'dark' = 'light';

  if (stored === 'dark' || stored === 'light') {
    theme = stored;
  } else if (window.matchMedia?.('(prefers-color-scheme: dark)').matches) {
    theme = 'dark';
  }

  document.documentElement.setAttribute('data-theme', theme);
  document.documentElement.setAttribute('data-theme-controller', theme);
  localStorage.setItem('theme', theme);
}
