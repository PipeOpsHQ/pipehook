/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./ui/templates/**/*.html"],
  theme: {
    extend: {
      colors: {
        brand: {
          300: "#93c5fd",
          400: "#60a5fa",
          500: "#3b82f6",
          600: "#2563eb"
        }
      }
    }
  },
  plugins: []
};
