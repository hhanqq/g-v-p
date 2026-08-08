/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        accent: "#2563eb",
        bg: "#0f172a",
        card: "#1e293b",
        border: "#334155",
        muted: "#94a3b8",
      },
    },
  },
  plugins: [],
};
