/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    './templates/*.html',
    './static/index.html',
    './views/*.html',
    './views/*/*.html',
    './views/*/*/*.html',
    './assets/*.js',
    // Svelte frontend paths
    './frontend/src/**/*.{svelte,js,ts}',
    './frontend/index.html',
  ],
  safelist: [
    'bg-red-500',
    'bg-green-500',
    'bg-orange-400',
    'text-3xl',
    'lg:text-4xl',
    // Responsive grid classes for Svelte
    'md:grid-cols-2',
    'md:grid-cols-3',
    'lg:grid-cols-3',
    'lg:grid-cols-4',
    'gap-3',
    'gap-4',
    'gap-6',
  ],
  theme: {
    screens: {
      'xs': '400px',
      'sm': '640px',
      'md': '768px',
      'lg': '1024px',
      'xl': '1280px',
      '2xl': '1536px',
    },
  },
  plugins: [require("daisyui")],
  daisyui: {
    themes: [
      {
        light: {
          "primary": "#2563eb",          // Blue for primary actions
          "primary-content": "#ffffff",  // Pure white text for primary buttons
          "secondary": "#4b5563",        // Gray for secondary elements
          "accent": "#0284c7",           // Sky blue for accents
          "neutral": "#1f2937",          // Dark gray for neutral text
          "base-100": "#ffffff",         // White background
          "base-200": "#f3f4f6",         // Light gray background
          "base-300": "#e5e7eb",         // Slightly darker background
          // Light theme surface colors
          "--surface-100": "#ffffff",     // Top level background (lightest)
          "--surface-200": "#f8fafc",     // Secondary surface level
          "--surface-300": "#f1f5f9",     // Tertiary surface level
          "--surface-400": "#e2e8f0",     // Quaternary surface level
          "--border-100": "#e2e8f0",      // Primary border color
          "--border-200": "#cbd5e1",      // Secondary border color
          "--hover-overlay": "rgba(0,0,0,0.05)",  // Hover state overlay

          "info": "#0ea5e9",             // Info blue
          "success": "#22c55e",          // Success green
          "warning": "#f59e0b",          // Warning yellow
          "error": "#ef4444",            // Error red
          "error-content": "#ffffff",    // White text for error buttons
          
          "--rounded-box": "0.5rem",     // Border radius for cards
          "--rounded-btn": "0.3rem",     // Border radius for buttons
          "--rounded-badge": "0.25rem",  // Border radius for badges
          
          "--animation-btn": "0.25s",    // Button click animation duration
          "--animation-input": "0.2s",   // Input focus animation duration
          
          "--btn-text-case": "normal",   // Normal text case for buttons
          "--navbar-padding": "0.75rem",  // Navbar padding
          "--border-btn": "1px",         // Button border width
        },
        dark: {
          "primary": "#3b82f6",          // Bright blue for primary actions
          "primary-content": "#020617",  // Pure white text for primary buttons
          "secondary": "#6b7280",        // Medium gray for secondary elements
          "accent": "#0369a1",           // Darker sky blue for accents
          "neutral": "#d1d5db",          // Light gray for neutral text
          "base-100": "#1f2937",         // Dark background
          "base-200": "#111827",         // Darker background
          "base-300": "#0f172a",         // Darkest background
          // Dark theme surface colors
          "--surface-100": "#1f2937",     // Top level background (darkest)
          "--surface-200": "#262f3f",     // Secondary surface level
          "--surface-300": "#2d3748",     // Tertiary surface level
          "--surface-400": "#374151",     // Quaternary surface level
          "--border-100": "#334155",      // Primary border color
          "--border-200": "#475569",      // Secondary border color
          "--hover-overlay": "rgba(255,255,255,0.05)",  // Hover state overlay

          "info": "#0284c7",             // Info blue
          "success": "#16a34a",          // Success green
          "warning": "#d97706",          // Warning yellow
          "error": "#dc2626",            // Error red
          "error-content": "#020617",    // White text for error buttons
          
          "--rounded-box": "0.5rem",     // Border radius for cards
          "--rounded-btn": "0.3rem",     // Border radius for buttons
          "--rounded-badge": "0.25rem",  // Border radius for badges
          
          "--animation-btn": "0.25s",    // Button click animation duration
          "--animation-input": "0.2s",   // Input focus animation duration
          
          "--btn-text-case": "normal",   // Normal text case for buttons
          "--navbar-padding": "0.75rem",  // Navbar padding
          "--border-btn": "1px",         // Button border width
        },
      },
    ],
  },
}