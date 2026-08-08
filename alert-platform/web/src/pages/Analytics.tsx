import { PageHeader, StagePlaceholder } from "../components/ui";

export default function Analytics() {
  return (
    <div>
      <PageHeader title="Аналитика" subtitle="Нагрузка, нарушения SLA, проблемные объекты" />
      <StagePlaceholder
        title="Нарушения SLA и топ проблемных объектов — этап 2"
        description="Полная аналитика поверх правил SLA (раздел «SLA») и истории оборудования. Базовые операционные метрики уже доступны на существующем дашборде администратора."
      />
      <a
        href="/console/dashboard"
        className="mt-4 inline-block rounded-md bg-accent px-4 py-2 text-sm text-white"
      >
        Открыть текущий дашборд →
      </a>
    </div>
  );
}
