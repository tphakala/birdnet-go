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
    themes: ["light", "dark", "dim", "nord"],
  },
}

