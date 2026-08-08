import { useQueryClient } from "@tanstack/react-query";
import { FormEvent, useState } from "react";
import { api, ApiError } from "../api";

export default function Login() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const queryClient = useQueryClient();

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.post("/auth/login", { username, password });
      await queryClient.invalidateQueries({ queryKey: ["me"] });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Ошибка входа");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex h-screen items-center justify-center bg-bg">
      <form onSubmit={onSubmit} className="w-80 rounded-xl border border-border bg-card p-8">
        <h1 className="mb-1 text-lg font-semibold">Диспетчер</h1>
        <p className="mb-6 text-sm text-muted">Вход через корпоративный LDAP-каталог</p>
        <label className="mb-3 block text-sm">
          Логин
          <input
            className="mt-1 w-full rounded-md border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-accent"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoFocus
          />
        </label>
        <label className="mb-4 block text-sm">
          Пароль
          <input
            type="password"
            className="mt-1 w-full rounded-md border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-accent"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {error && <p className="mb-4 text-sm text-red-400">{error}</p>}
        <button
          type="submit"
          disabled={busy}
          className="w-full rounded-md bg-accent py-2 text-sm font-medium text-white disabled:opacity-60"
        >
          {busy ? "Проверяю…" : "Войти"}
        </button>
      </form>
    </div>
  );
}
