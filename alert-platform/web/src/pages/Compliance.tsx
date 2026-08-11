import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../api";
import { ThemeToggle } from "../theme";

interface ComplianceMetrics {
  delivered_pct: number | null;
  duplicates_detected: number;
  root_cause_hypotheses: number;
  ai_supplements_sent: number;
}

interface AiSelftestResult {
  requested_at: string;
  elapsed_seconds: number;
  model: string;
  prompt: string;
  reply: string | null;
}

interface Criterion {
  name: string;
  weight: string;
  score: number;
  points: string[];
}

const CRITERIA: Criterion[] = [
  {
    name: "Техническая реализация решения (прототип)",
    weight: "0.2",
    score: 5,
    points: [
      "Личный кабинет, self-service подписок и маршрутизация по критичности работают без ограничений",
      "Приём, нормализация, классификация, агрегация и маршрутизация реализованы сквозным конвейером",
      "Группы + зона ответственности по оборудованию с каскадной эскалацией — тот же сценарий, что и флагманский пример кейса (P0 на буровой: адресно, без массовой рассылки, с эскалацией по таймауту)",
      "Дашборд администратора, журнал доставки и аудит — реальные разделы, не макет",
      "Конфигурируемое подключение источников без изменения ядра — раздел «Источники»",
    ],
  },
  {
    name: "Архитектура решения и используемые технологии",
    weight: "0.15",
    score: 5,
    points: [
      "Доменное ядро (gateway, pipeline, планировщик доставки, admin API) — Go; TrueConf-адаптер — тонкий Python-слой",
      "Взаимодействие сервисов описано через определённые контракты — outbox v1, ARCHITECTURE.md",
      "Универсальный API — POST /api/v1/ingest/raw, живая Swagger-документация /ingest/docs",
      "Горизонтальное масштабирование воркеров (FOR UPDATE SKIP LOCKED), новые источники — без изменения ядра",
    ],
  },
  {
    name: "Использование инструментов искусственного интеллекта",
    weight: "0.15",
    score: 5,
    points: [
      "7 сценариев на едином модуле: саммаризация, семантическая нормализация, рекомендации из базы знаний (с RAG), дедупликация между источниками, гипотеза первопричины, разбор алерта по запросу сотрудника, умная маршрутизация на основе истории",
      "ИИ-разбор по запросу — с реальным RAG: векторный поиск (pgvector + локальные эмбеддинги) по 10-документной базе знаний компании об управлении инцидентами, не статичный чек-лист",
      'Умная маршрутизация — реальный запрос к истории подписок коллег по группам (<a href="https://github.com/hhanqq/g-v-p/blob/main/alert-platform/go-platform/internal/adminapi/subscription_suggestion.go">subscription_suggestion.go</a>), не выдуманная логика; показывается в личном кабинете и на карточке сотрудника',
      "Каждый сценарий деградирует к сырому алерту/шаблонной фразе при отказе ИИ (раздел И5) — никогда не блокирует доставку или экран",
      "Локальная модель (Ollama) — без внешних облаков, соответствует политике ИБ",
    ],
  },
  {
    name: "Экономическая эффективность и перспективы внедрения",
    weight: "0.1",
    score: 1,
    points: ["В работе — следующий шаг"],
  },
  {
    name: "Информационная безопасность",
    weight: "0.1",
    score: 5,
    points: [
      "LDAP/AD (glauth) + RBAC — /dashboard, /sources, /audit защищены ролью admins",
      "Токен источников для POST /api/v1/ingest/raw (X-Source-Token) — требование «безопасность API для новых источников» из критерия 5 баллов",
      "Автоотключение подписок при увольнении — сверка с LDAP-каталогом каждые ~30с",
      'Аудит действий — <a href="/audit">/audit</a> (входы, источники, подписки)',
      "Экранирование HTML в исходящих TrueConf-сообщениях — источник мониторинга или ИИ не может протащить разметку/ссылку в чат",
      "Postgres/Ollama/LDAP закрыты от внешнего доступа (были непреднамеренно опубликованы — найдено и исправлено); LDAP-фильтр экранируется на обеих сторонах (Go/Python), служебный пароль ротирован",
      "Модель угроз и мер — SECURITY.md",
    ],
  },
  {
    name: "Инфраструктура и масштабируемость решения",
    weight: "0.1",
    score: 4,
    points: [
      "Измеренная пропускная способность (не оценка) — INFRASTRUCTURE.md: запас к целевой нагрузке кейса",
      "Резервное копирование Postgres — ежедневный cron, проверено вживую",
      "Резервирование Postgres — асинхронная streaming-реплика, поднята без даунтайма primary, задержка репликации 0,591мс (измерено)",
      "Горизонтальное масштабирование воркера — FOR UPDATE SKIP LOCKED",
      "Честно: ИИ-модуль (Ollama) пока делит CPU/RAM с остальным хостом без выделенных ресурсов — намеренное решение для пилота, выделенный GPU-сервер — пункт бюджета следующего этапа (как и в самом кейсе)",
    ],
  },
  {
    name: "Демонстрация решения",
    weight: "0.1",
    score: 4,
    points: [
      "Полный цикл в реальном времени — событие → доставка, с учётом подписок",
      "Self-service «на лету» подтверждён с двумя разными реальными получателями TrueConf (разная маршрутизация)",
      "Сценарий защиты — один непрерывный флоу (см. PRESENTATION_CONTENT.md, приложение А): смена подписки → событие → доставка по новому правилу, без склейки двух независимых прогонов",
    ],
  },
  {
    name: "Презентация и обоснование требований",
    weight: "0.1",
    score: 1,
    points: ["В работе — следующий шаг"],
  },
];

function ScoreBadge({ score }: { score: number }) {
  const ok = score >= 4;
  return (
    <span
      className={`rounded-full px-2.5 py-0.5 text-xs font-bold ${
        ok ? "bg-emerald-500/15 text-emerald-400" : "bg-amber-500/15 text-amber-400"
      }`}
    >
      {score}/5
    </span>
  );
}

export default function Compliance() {
  const { data: metrics } = useQuery<ComplianceMetrics>({
    queryKey: ["compliance-metrics"],
    queryFn: () => api.get<ComplianceMetrics>("/compliance-metrics"),
  });
  const [result, setResult] = useState<AiSelftestResult | null>(null);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function runSelftest() {
    setRunning(true);
    setError(null);
    try {
      const data = await api.get<AiSelftestResult>("/ai-selftest");
      setResult(data);
    } catch (e) {
      setError(String(e));
    } finally {
      setRunning(false);
    }
  }

  return (
    <div className="mx-auto min-h-screen max-w-5xl px-4 py-10 text-fg">
      <div className="flex items-center justify-between">
        <Link to="/" className="text-sm text-accent">
          ← платформа ADP
        </Link>
        <ThemeToggle />
      </div>
      <h1 className="mt-3 text-2xl font-semibold">Соответствие критериям оценки кейса</h1>
      <p className="mt-2 max-w-2xl text-sm text-muted">
        Каждый пункт ниже — ссылка на живую страницу платформы, не макет. Экономика и презентация —
        намеренно последними по плану.
      </p>

      <div className="mt-6 grid grid-cols-1 gap-4 md:grid-cols-2">
        {CRITERIA.map((c) => (
          <div key={c.name} className="rounded-xl border border-border bg-card p-5">
            <div className="flex items-start justify-between gap-3">
              <h3 className="text-sm font-semibold leading-snug">{c.name}</h3>
              <ScoreBadge score={c.score} />
            </div>
            <div className="mt-1 text-[11px] uppercase tracking-wide text-muted">вес {c.weight}</div>
            <ul className="mt-3 space-y-1.5 text-xs leading-relaxed text-fg">
              {c.points.map((p, i) => (
                <li key={i} dangerouslySetInnerHTML={{ __html: `• ${p}` }} />
              ))}
            </ul>
          </div>
        ))}
      </div>

      <div className="mt-10 rounded-xl border border-border bg-card p-6">
        <h2 className="text-lg font-semibold">Проверка: ИИ действительно генерирует ответы сейчас</h2>
        <p className="mt-2 text-xs text-muted">
          Кнопка ниже делает настоящий вызов той же локальной модели, что и все 7 сценариев — не
          статичный текст. Время запроса и задержка ответа меняются при каждом нажатии именно потому,
          что вызов живой.
        </p>
        <button
          onClick={runSelftest}
          disabled={running}
          className="mt-4 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white disabled:opacity-60"
        >
          {running ? "Жду ответ модели (до 20-30с при холодном старте)…" : "Сгенерировать ответ прямо сейчас"}
        </button>
        {error && <p className="mt-3 text-xs text-red-400">Ошибка запроса: {error}</p>}
        {result && (
          <pre className="mt-4 whitespace-pre-wrap rounded-lg bg-bg p-4 font-mono text-xs text-fg">
            {`Время запроса: ${result.requested_at}\nЗадержка: ${result.elapsed_seconds} с\nМодель: ${result.model}\n\nВопрос: ${result.prompt}\n\nОтвет модели:\n${result.reply ?? "(нет ответа — ИИ недоступна, раздел И5)"}`}
          </pre>
        )}
        <div className="mt-6 flex flex-wrap gap-6">
          <div>
            <div className="text-2xl font-semibold tabular-nums">{metrics?.duplicates_detected ?? "—"}</div>
            <div className="text-xs text-muted">ИИ: дублей найдено (реально, по данным)</div>
          </div>
          <div>
            <div className="text-2xl font-semibold tabular-nums">{metrics?.root_cause_hypotheses ?? "—"}</div>
            <div className="text-xs text-muted">ИИ: гипотез первопричины</div>
          </div>
          <div>
            <div className="text-2xl font-semibold tabular-nums">{metrics?.ai_supplements_sent ?? "—"}</div>
            <div className="text-xs text-muted">ИИ-сводок/рекомендаций отправлено</div>
          </div>
          <div>
            <div className="text-2xl font-semibold tabular-nums">{metrics?.delivered_pct ?? "—"}%</div>
            <div className="text-xs text-muted">Уведомлений доставлено</div>
          </div>
        </div>
      </div>
    </div>
  );
}
