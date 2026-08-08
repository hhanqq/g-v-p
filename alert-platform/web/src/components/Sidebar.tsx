import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { api, CurrentUser } from "../api";

interface NavChild {
  label: string;
  to: string;
}

interface NavItem {
  label: string;
  to: string;
  icon: string;
  badge?: "этап 2";
  children?: NavChild[];
}

// Ровно 9 разделов из запроса — пункты можно менять/переставлять по мере
// уточнения приоритетов, структура компонента от конкретного набора не
// зависит. "Активные"/"Завершённые"/"Все" и т.п. — реальные подпункты
// (не заглушка ради красоты): каждый — отдельный query-параметр страницы.
const NAV: NavItem[] = [
  { label: "Главная", to: "/", icon: "🏠" },
  {
    label: "Инциденты", to: "/incidents", icon: "🚨",
    children: [
      { label: "Активные", to: "/incidents?status=open" },
      { label: "Завершённые", to: "/incidents?status=closed" },
      { label: "Все", to: "/incidents" },
    ],
  },
  { label: "Алерты", to: "/alerts", icon: "📋" },
  { label: "Оборудование", to: "/equipment", icon: "🛢️" },
  {
    label: "Сотрудники", to: "/employees", icon: "👥",
    children: [
      { label: "Все сотрудники", to: "/employees" },
      { label: "Доступность", to: "/employees?view=availability" },
    ],
  },
  { label: "Сценарии", to: "/scenarios", icon: "🧩", badge: "этап 2" },
  { label: "SLA", to: "/sla", icon: "⏱️", badge: "этап 2" },
  { label: "Аналитика", to: "/analytics", icon: "📈", badge: "этап 2" },
  { label: "Интеграции", to: "/integrations", icon: "🔌" },
];

export default function Sidebar({ user }: { user: CurrentUser }) {
  const location = useLocation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [expanded, setExpanded] = useState<string | null>(
    NAV.find((n) => n.children && location.pathname === n.to)?.label ?? null,
  );

  async function logout() {
    await api.post("/auth/logout");
    queryClient.setQueryData(["me"], undefined);
    queryClient.invalidateQueries({ queryKey: ["me"] });
  }

  return (
    <aside className="flex w-64 shrink-0 flex-col border-r border-border bg-card">
      <div className="border-b border-border px-5 py-4">
        <div className="text-sm font-semibold">Диспетчер</div>
        <div className="text-xs text-muted">no-code платформа инцидентов</div>
      </div>

      <nav className="flex-1 overflow-y-auto py-2">
        {NAV.map((item) => {
          const isActive = location.pathname === item.to || location.pathname.startsWith(item.to + "/");
          const isExpanded = expanded === item.label;
          return (
            <div key={item.label}>
              <button
                onClick={() => {
                  if (item.children) {
                    setExpanded(isExpanded ? null : item.label);
                  } else {
                    navigate(item.to);
                  }
                }}
                className={`flex w-full items-center justify-between px-5 py-2.5 text-left text-sm transition-colors ${
                  isActive ? "bg-accent/15 text-white" : "text-slate-300 hover:bg-white/5"
                }`}
              >
                <span className="flex items-center gap-2.5">
                  <span>{item.icon}</span>
                  <span>{item.label}</span>
                </span>
                <span className="flex items-center gap-2">
                  {item.badge && (
                    <span className="rounded bg-white/10 px-1.5 py-0.5 text-[10px] text-muted">
                      {item.badge}
                    </span>
                  )}
                  {item.children && (
                    <span className={`text-xs transition-transform ${isExpanded ? "rotate-90" : ""}`}>›</span>
                  )}
                </span>
              </button>
              {item.children && (
                <div
                  className={`grid overflow-hidden transition-all duration-200 ${
                    isExpanded ? "grid-rows-[1fr]" : "grid-rows-[0fr]"
                  }`}
                >
                  <div className="min-h-0">
                    {item.children.map((child) => (
                      <NavLink
                        key={child.to}
                        to={child.to}
                        className="block px-5 py-2 pl-12 text-sm text-muted hover:text-slate-100"
                      >
                        {child.label}
                      </NavLink>
                    ))}
                  </div>
                </div>
              )}
            </div>
          );
        })}
      </nav>

      <div className="border-t border-border px-5 py-4 text-sm">
        <div className="mb-2">
          <div>{user.username}</div>
          <div className="text-xs text-muted">{user.is_admin ? "администратор" : "сотрудник"}</div>
        </div>
        <button onClick={logout} className="text-xs text-muted hover:text-slate-100">
          Выйти
        </button>
      </div>
    </aside>
  );
}
