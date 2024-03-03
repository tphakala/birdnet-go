/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    './templates/*.html',
    './static/index.html',
    './views/*.html',
    './views/*/*.html',
  ],
  safelist: [
    'bg-red-500',
    'bg-green-500',
    'bg-orange-400',
    'text-3xl',
    'lg:text-4xl',
  ],
  theme: {
    extend: {},
  },
  plugins: [require("daisyui")],
  daisyui: {
    themes: ["light", "dark", "dim", "nord"],
  },
}

