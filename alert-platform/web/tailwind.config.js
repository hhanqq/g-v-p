/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        accent: "rgb(var(--c-accent) / <alpha-value>)",
        bg: "rgb(var(--c-bg) / <alpha-value>)",
        card: "rgb(var(--c-card) / <alpha-value>)",
        border: "rgb(var(--c-border) / <alpha-value>)",
        muted: "rgb(var(--c-muted) / <alpha-value>)",
        fg: "rgb(var(--c-fg) / <alpha-value>)",
      },
    },
  },
  plugins: [],
};
