import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, Send } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { AIChatResponse, AIEntityRef, AIJournalEntry, AITool, api } from "../api";
import { Card, EmptyState, PageHeader } from "../components/ui";

// Раздел «ADP AI» доп. ТЗ. Поток строго User → ADP AI → tool selection →
// Allowed tool → Permission check → Use Case → Repository (бэкенд,
// go-platform/internal/adminapi/ai_routes.go) — фронтенд только шлёт
// текст сообщения и рендерит структурный ответ (entities/navigate),
// сам никогда не решает, какой tool вызвать и не строит URL из текста
// модели.
const QUICK_COMMANDS = [
  "Покажи активные критические инциденты",
  "Кто сейчас доступен для реакции на инцидент 1?",
  "Покажи разрывы покрытия",
  "Сводная аналитика за неделю",
];

type ChatEntry = {
  role: "user" | "assistant";
  text: string;
  entities?: AIEntityRef[];
  navigate?: AIEntityRef | null;
  status?: AIChatResponse["status"];
};

const STATUS_LABEL: Record<string, string> = {
  SUCCESS: "успех", FAILED: "ошибка", DENIED: "отказано",
  CONFIRMATION_REQUIRED: "требуется подтверждение", CANCELLED: "отменено",
};
const STATUS_CLASS: Record<string, string> = {
  SUCCESS: "bg-emerald-500/15 text-emerald-400", FAILED: "bg-red-500/15 text-red-400",
  DENIED: "bg-amber-500/15 text-amber-400", CONFIRMATION_REQUIRED: "bg-sky-500/15 text-sky-400",
  CANCELLED: "bg-slate-500/15 text-muted",
};

function entityHref(entity: AIEntityRef): string | null {
  switch (entity.type) {
    case "incident":
      return `/incidents/${entity.id}`;
    case "equipment":
      return `/equipment/${encodeURIComponent(entity.id)}`;
    case "employee":
      return `/employees/${entity.id}`;
    case "alert":
      return "/alerts";
    default:
      return null;
  }
}

function EntityChip({ entity }: { entity: AIEntityRef }) {
  const href = entityHref(entity);
  if (!href) return <span className="rounded bg-fg/10 px-2 py-0.5 text-xs">{entity.label}</span>;
  return (
    <Link to={href} className="rounded bg-accent/15 px-2 py-0.5 text-xs text-accent hover:bg-accent/25">
      {entity.label} · Открыть
    </Link>
  );
}

function Assistant() {
  const [input, setInput] = useState("");
  const [pending, setPending] = useState(false);
  const [history, setHistory] = useState<ChatEntry[]>([]);
  const sessionID = useRef(Math.random().toString(36).slice(2));
  const scrollRef = useRef<HTMLDivElement>(null);

  const { data: tools } = useQuery<AITool[]>({
    queryKey: ["ai-tools"],
    queryFn: () => api.get<AITool[]>("/ai/tools"),
  });

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
  }, [history]);

  async function send(message: string) {
    if (!message.trim() || pending) return;
    setHistory((prev) => [...prev, { role: "user", text: message }]);
    setInput("");
    setPending(true);
    try {
      const response = await api.post<AIChatResponse>("/ai/chat", { message, session_id: sessionID.current });
      setHistory((prev) => [
        ...prev,
        { role: "assistant", text: response.message, entities: response.entities, navigate: response.navigate, status: response.status },
      ]);
    } catch {
      setHistory((prev) => [
        ...prev,
        { role: "assistant", text: "Не удалось связаться с ADP AI. Попробуйте позже.", status: "FAILED" },
      ]);
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="grid gap-4 lg:grid-cols-[1fr_260px]">
      <Card className="flex h-[560px] flex-col">
        <div ref={scrollRef} className="flex-1 space-y-3 overflow-y-auto pr-1">
          {history.length === 0 && (
            <div className="flex h-full items-center justify-center text-center text-sm text-muted">
              Задайте вопрос о текущих инцидентах, оборудовании, доступности сотрудников или покрытии.
            </div>
          )}
          {history.map((entry, i) => (
            <div key={i} className={`flex ${entry.role === "user" ? "justify-end" : "justify-start"}`}>
              <div
                className={`max-w-[80%] rounded-xl px-3 py-2 text-sm ${
                  entry.role === "user" ? "bg-accent text-white" : "bg-bg"
                }`}
              >
                {entry.role === "assistant" && (
                  <div className="mb-1 flex items-center gap-1.5 text-xs text-muted">
                    <Bot size={13} strokeWidth={1.75} />
                    ADP AI
                    {entry.status && entry.status !== "SUCCESS" && (
                      <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${STATUS_CLASS[entry.status]}`}>
                        {STATUS_LABEL[entry.status]}
                      </span>
                    )}
                  </div>
                )}
                <div className="whitespace-pre-wrap">{entry.text}</div>
                {entry.navigate && (
                  <div className="mt-2">
                    <EntityChip entity={entry.navigate} />
                  </div>
                )}
                {!!entry.entities?.length && (
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {entry.entities.slice(0, 8).map((e) => (
                      <EntityChip key={`${e.type}-${e.id}`} entity={e} />
                    ))}
                  </div>
                )}
              </div>
            </div>
          ))}
          {pending && <div className="text-xs text-muted">ADP AI думает…</div>}
        </div>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void send(input);
          }}
          className="mt-3 flex items-center gap-2 border-t border-border pt-3"
        >
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="Спросите ADP AI…"
            className="flex-1 rounded-lg border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-accent"
          />
          <button
            type="submit"
            disabled={pending || !input.trim()}
            className="flex items-center gap-1.5 rounded-lg bg-accent px-3 py-2 text-sm text-white disabled:opacity-50"
          >
            <Send size={15} strokeWidth={1.75} />
          </button>
        </form>
      </Card>

      <Card>
        <h3 className="mb-3 text-sm font-semibold">Быстрые команды</h3>
        <div className="flex flex-col gap-2">
          {QUICK_COMMANDS.map((command) => (
            <button
              key={command}
              onClick={() => void send(command)}
              className="rounded-lg border border-border px-2.5 py-2 text-left text-xs hover:border-accent hover:text-accent"
            >
              {command}
            </button>
          ))}
        </div>
        {!!tools?.length && (
          <>
            <h3 className="mb-2 mt-4 text-xs font-semibold uppercase text-muted">Доступные инструменты</h3>
            <ul className="space-y-1 text-xs text-muted">
              {tools.map((tool) => (
                <li key={tool.name}>{tool.description}</li>
              ))}
            </ul>
          </>
        )}
      </Card>
    </div>
  );
}

const JOURNAL_PRESETS = [
  { label: "Все", value: {} },
  { label: "Ошибки", value: { status: "FAILED" } },
  { label: "Denied", value: { status: "DENIED" } },
  { label: "Write actions", value: { resource_type: "write" } },
] as const;

function Journal() {
  const [params, setParams] = useSearchParams();
  const status = params.get("status") ?? "";
  const query = new URLSearchParams();
  if (status) query.set("status", status);

  const { data, isLoading } = useQuery<AIJournalEntry[]>({
    queryKey: ["ai-journal", status],
    queryFn: () => api.get<AIJournalEntry[]>(`/ai/journal?${query.toString()}`),
    refetchInterval: 15000,
  });

  return (
    <div>
      <div className="mb-3 flex gap-2">
        {JOURNAL_PRESETS.map((preset) => (
          <button
            key={preset.label}
            onClick={() => setParams(preset.value as Record<string, string>)}
            className={`rounded-md px-2.5 py-1 text-xs ${
              status === (preset.value as { status?: string }).status
                ? "bg-accent text-white"
                : "bg-card text-muted hover:text-fg"
            }`}
          >
            {preset.label}
          </button>
        ))}
      </div>
      {isLoading && <div className="text-sm text-muted">Загрузка…</div>}
      {!isLoading && data?.length === 0 && <EmptyState>Записей журнала не найдено</EmptyState>}
      {!!data?.length && (
        <div className="overflow-x-auto rounded-xl border border-border">
          <table className="w-full min-w-[960px] text-sm">
            <thead className="bg-card text-left text-xs text-muted">
              <tr>
                <th className="px-3 py-2">Время</th>
                <th className="px-3 py-2">Инициатор</th>
                <th className="px-3 py-2">Команда</th>
                <th className="px-3 py-2">Tool</th>
                <th className="px-3 py-2">Объект</th>
                <th className="px-3 py-2">Результат</th>
                <th className="px-3 py-2">Статус</th>
              </tr>
            </thead>
            <tbody>
              {data.map((entry) => (
                <tr key={entry.id} className="border-t border-border align-top hover:bg-fg/5">
                  <td className="px-3 py-2 text-xs text-muted">{new Date(entry.created_at).toLocaleString("ru-RU")}</td>
                  <td className="px-3 py-2">{entry.username}</td>
                  <td className="px-3 py-2 text-muted">{entry.request_text}</td>
                  <td className="px-3 py-2 font-mono text-xs">{entry.tool_name ?? "—"}</td>
                  <td className="px-3 py-2 text-xs text-muted">
                    {entry.resource_type ? `${entry.resource_type}${entry.resource_id ? " #" + entry.resource_id : ""}` : "—"}
                  </td>
                  <td className="px-3 py-2 text-xs">{entry.result_summary ?? entry.error_message ?? "—"}</td>
                  <td className="px-3 py-2">
                    <span className={`rounded px-2 py-0.5 text-[11px] font-medium ${STATUS_CLASS[entry.status] ?? "bg-slate-500/15 text-muted"}`}>
                      {STATUS_LABEL[entry.status] ?? entry.status}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

export default function AdpAi() {
  const [tab, setTab] = useState<"assistant" | "journal">("assistant");
  const queryClient = useQueryClient();

  return (
    <div>
      <PageHeader title="ADP AI" subtitle="AI-помощник по данным платформы + журнал его действий" />
      <div className="mb-4 flex gap-2">
        <button
          onClick={() => setTab("assistant")}
          className={`rounded-md px-3 py-1.5 text-sm ${tab === "assistant" ? "bg-accent text-white" : "bg-card text-muted hover:text-fg"}`}
        >
          AI-помощник
        </button>
        <button
          onClick={() => {
            setTab("journal");
            void queryClient.invalidateQueries({ queryKey: ["ai-journal"] });
          }}
          className={`rounded-md px-3 py-1.5 text-sm ${tab === "journal" ? "bg-accent text-white" : "bg-card text-muted hover:text-fg"}`}
        >
          Журнал
        </button>
      </div>
      {tab === "assistant" ? <Assistant /> : <Journal />}
    </div>
  );
}
