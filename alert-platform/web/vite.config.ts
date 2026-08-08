import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// base ДОЛЖЕН совпадать с тем, как страница реально смонтирована снаружи:
// nginx проксирует /console/* на admin-console без изменения пути, а
// FastAPI отдаёт SPA под /app — итоговый внешний путь /console/app/.
// Та же грабля с относительными путями за реверс-прокси, что уже не раз
// ловили в проекте (личный кабинет, консоль сценариев) — здесь фиксируем
// на уровне сборки, а не рантайма.
export default defineConfig({
  base: "/console/app/",
  plugins: [react()],
  server: {
    proxy: {
      // Приложение везде обращается к /console/api/... (совпадает с тем,
      // как это выглядит в проде за nginx) — в dev просто срезаем префикс
      // /console перед проксированием на локальный admin-console.
      "/console/api": {
        target: "http://127.0.0.1:8090",
        rewrite: (path) => path.replace(/^\/console/, ""),
      },
    },
  },
});
