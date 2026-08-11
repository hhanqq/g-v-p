import { ReactNode } from "react";
import { Link } from "react-router-dom";
import { HelpCircle } from "lucide-react";

export function HelpButton({ articleId }: { articleId: string }) {
  return (
    <Link
      to={`/help?a=${articleId}`}
      className="inline-flex h-6 w-6 items-center justify-center rounded-full text-muted hover:bg-fg/10 hover:text-fg"
      aria-label="Справка по этому разделу"
      title="Справка по этому разделу"
    >
      <HelpCircle size={16} strokeWidth={1.75} />
    </Link>
  );
}

export function PageHeader({
  title,
  subtitle,
  helpArticle,
}: {
  title: string;
  subtitle?: string;
  helpArticle?: string;
}) {
  return (
    <div className="mb-6">
      <div className="flex items-center gap-2">
        <h1 className="text-xl font-semibold">{title}</h1>
        {helpArticle && <HelpButton articleId={helpArticle} />}
      </div>
      {subtitle && <p className="mt-1 text-sm text-muted">{subtitle}</p>}
    </div>
  );
}

export function Card({
  children,
  className = "",
  onClick,
}: {
  children: ReactNode;
  className?: string;
  onClick?: () => void;
}) {
  return (
    <div className={`rounded-xl border border-border bg-card p-5 ${className}`} onClick={onClick}>
      {children}
    </div>
  );
}

export function StatTile({ label, value }: { label: string; value: ReactNode }) {
  return (
    <Card>
      <div className="text-2xl font-semibold tabular-nums">{value}</div>
      <div className="mt-1 text-xs text-muted">{label}</div>
    </Card>
  );
}

const PRIORITY_COLOR: Record<string, string> = {
  P0: "bg-red-500/15 text-red-400",
  P1: "bg-orange-500/15 text-orange-400",
  P2: "bg-yellow-500/15 text-yellow-400",
  P3: "bg-slate-500/15 text-muted",
};

export function PriorityBadge({ priority }: { priority: string | null }) {
  const cls = PRIORITY_COLOR[priority ?? ""] ?? "bg-slate-500/15 text-muted";
  return <span className={`rounded px-2 py-0.5 text-xs font-medium ${cls}`}>{priority ?? "—"}</span>;
}

export function StatusBadge({ status }: { status: string }) {
  const active = status === "OPEN" || status === "FLAPPING";
  return (
    <span
      className={`rounded px-2 py-0.5 text-xs font-medium ${
        active ? "bg-red-500/15 text-red-400" : "bg-emerald-500/15 text-emerald-400"
      }`}
    >
      {status}
    </span>
  );
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <div className="rounded-xl border border-dashed border-border p-8 text-center text-sm text-muted">{children}</div>;
}
