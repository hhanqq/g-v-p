import { useQueryClient } from "@tanstack/react-query";
import {
  BarChart3, CheckCircle2, ClipboardList, HelpCircle, History, Home,
  PanelLeftClose, PanelLeftOpen, Plug, Server, ShieldCheck, Siren,
  Timer, Users, UsersRound, Workflow, CalendarDays, UserCog, Activity,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useState } from "react";
import { Link, NavLink } from "react-router-dom";
import { api, CurrentUser, hasPermission } from "../api";
import { ThemeToggle } from "../theme";

interface NavLeaf {
  label: string;
  to: string;
  icon: LucideIcon;
  permission: string;
}

interface NavGroup {
  title: string | null;
  items: NavLeaf[];
}

// Плоская структура (группа → пункты, без вложенных раскрытий) — так
// проще сворачивать sidebar в icon-only режим. Пункты, для которых
// страницы ещё не существуют («Сервисы», «Поиск», «Состояние системы» и
// т.п. из целевой схемы навигации), сюда не добавлены сознательно — не
// создаём ссылки на несуществующие экраны. Каждый пункт привязан к
// permission (раздел 16 доп. ТЗ: состав меню зависит от прав) — это
// удобство интерфейса, реальная защита — на сервере (withPermission).
const GROUPS: NavGroup[] = [
  { title: null, items: [{ label: "Главная", to: "/", icon: Home, permission: "dashboard.read" }] },
  {
    title: "Операции",
    items: [
      { label: "Алерты", to: "/alerts", icon: ClipboardList, permission: "alerts.read" },
      { label: "Инциденты", to: "/incidents", icon: Siren, permission: "incidents.read" },
    ],
  },
  {
    title: "Инфраструктура",
    items: [
      { label: "Оборудование", to: "/equipment", icon: Server, permission: "equipment.read" },
      { label: "Покрытие", to: "/coverage", icon: ShieldCheck, permission: "coverage.read" },
    ],
  },
  {
    title: "Люди",
    items: [
      { label: "Сотрудники", to: "/employees", icon: Users, permission: "employees.read" },
      { label: "Календарь", to: "/availability", icon: CalendarDays, permission: "availability.read" },
      { label: "Группы", to: "/groups", icon: UsersRound, permission: "employees.read" },
    ],
  },
  {
    title: "Автоматизация",
    items: [
      { label: "Сценарии", to: "/scenarios", icon: Workflow, permission: "scenarios.read" },
      { label: "SLA", to: "/sla", icon: Timer, permission: "sla.read" },
    ],
  },
  { title: "Данные", items: [{ label: "История изменений", to: "/change-history", icon: History, permission: "audit.read" }] },
  { title: null, items: [{ label: "Аналитика", to: "/analytics", icon: BarChart3, permission: "analytics.read" }] },
  {
    title: "Администрирование",
    items: [
      { label: "Интеграции", to: "/integrations", icon: Plug, permission: "integrations.read" },
      { label: "Источники", to: "/sources", icon: Server, permission: "sources.read" },
      { label: "Пользователи и права", to: "/users", icon: UserCog, permission: "users.read" },
      { label: "Аудит", to: "/audit", icon: History, permission: "audit.read" },
      { label: "Состояние системы", to: "/platform-health", icon: Activity, permission: "platform_health.read" },
    ],
  },
  { title: null, items: [{ label: "Справка", to: "/help", icon: HelpCircle, permission: "help.read" }] },
];

const COLLAPSE_KEY = "adp_sidebar_collapsed";

export default function Sidebar({
  user,
  mobileOpen,
  onCloseMobile,
}: {
  user: CurrentUser;
  mobileOpen: boolean;
  onCloseMobile: () => void;
}) {
  const queryClient = useQueryClient();
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem(COLLAPSE_KEY) === "1";
    } catch {
      return false;
    }
  });

  useEffect(() => {
    try {
      localStorage.setItem(COLLAPSE_KEY, collapsed ? "1" : "0");
    } catch {
      // localStorage может быть недоступен (приватный режим) — тогда
      // просто не запоминаем выбор между перезагрузками, не критично.
    }
  }, [collapsed]);

  async function logout() {
    await api.post("/auth/logout");
    queryClient.setQueryData(["me"], undefined);
    queryClient.invalidateQueries({ queryKey: ["me"] });
  }

  return (
    <>
      {mobileOpen && (
        <div className="fixed inset-0 z-40 bg-black/50 md:hidden" onClick={onCloseMobile} aria-hidden="true" />
      )}
      <aside
        className={`fixed inset-y-0 left-0 z-50 flex w-64 flex-col border-r border-border bg-card transition-transform duration-200 md:static md:z-auto md:translate-x-0 ${
          mobileOpen ? "translate-x-0" : "-translate-x-full"
        } ${collapsed ? "md:w-16" : "md:w-64"}`}
      >
        <div className="flex items-center justify-between border-b border-border px-5 py-4 md:px-4">
          <div className={collapsed ? "md:hidden" : ""}>
            <div className="text-sm font-semibold">ADP</div>
            <div className="text-xs text-muted">Alert Data Platform</div>
          </div>
          <button
            onClick={() => setCollapsed((v) => !v)}
            className="hidden shrink-0 rounded p-1 text-muted hover:bg-fg/10 hover:text-fg md:block"
            aria-label={collapsed ? "Развернуть меню" : "Свернуть меню"}
            title={collapsed ? "Развернуть меню" : "Свернуть меню"}
          >
            {collapsed ? <PanelLeftOpen size={18} strokeWidth={1.75} /> : <PanelLeftClose size={18} strokeWidth={1.75} />}
          </button>
        </div>

        <nav className="flex-1 overflow-y-auto py-2">
          {GROUPS.map((group, gi) => {
            const items = group.items.filter((item) => hasPermission(user, item.permission));
            if (items.length === 0) return null;
            return (
            <div key={gi} className="mb-1">
              {group.title && (
                <div className={`px-5 pb-1 pt-3 text-[10px] font-semibold uppercase tracking-wide text-muted ${collapsed ? "md:hidden" : ""}`}>
                  {group.title}
                </div>
              )}
              {items.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  onClick={onCloseMobile}
                  end={item.to === "/"}
                  className={({ isActive }) =>
                    `flex items-center gap-2.5 px-5 py-2.5 text-sm transition-colors md:px-4 ${
                      collapsed ? "md:justify-center md:px-0" : ""
                    } ${isActive ? "bg-accent/15 text-accent" : "text-fg hover:bg-fg/5"}`
                  }
                  title={collapsed ? item.label : undefined}
                >
                  <item.icon size={16} strokeWidth={1.75} className="shrink-0" />
                  <span className={collapsed ? "md:hidden" : ""}>{item.label}</span>
                </NavLink>
              ))}
            </div>
            );
          })}
        </nav>

        <div className={`border-t border-border px-5 py-4 text-sm md:px-4 ${collapsed ? "md:px-2" : ""}`}>
          <ThemeToggle className={`mb-3 ${collapsed ? "md:justify-center" : ""}`} />
          <Link
            to="/compliance"
            className={`mb-3 flex items-center gap-1.5 text-xs text-muted hover:text-fg ${collapsed ? "md:justify-center" : ""}`}
            title="Соответствие критериям кейса"
          >
            <CheckCircle2 size={14} strokeWidth={1.75} />
            <span className={collapsed ? "md:hidden" : ""}>Соответствие критериям кейса</span>
          </Link>
          <div className={`mb-2 ${collapsed ? "md:hidden" : ""}`}>
            <div>{user.username}</div>
            <div className="text-xs text-muted">{user.role_label}</div>
          </div>
          <button onClick={logout} className="text-xs text-muted hover:text-fg" title="Выйти">
            <span className={collapsed ? "md:hidden" : ""}>Выйти</span>
          </button>
        </div>
      </aside>
    </>
  );
}
