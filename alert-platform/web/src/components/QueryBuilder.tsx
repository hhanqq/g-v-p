import { FilterNode } from "../api";

export interface FieldOption {
  value: string;
  label: string;
}

export interface FieldDef {
  field: string;
  label: string;
  kind: "enum" | "string" | "bool" | "int";
  op: string;
  options?: FieldOption[];
}

// QueryBuilder — низкокодовый конструктор условий (ВСЕ/ЛЮБОЕ, вложенные
// группы) поверх общего FilterNode. Работает с любым набором fieldDefs,
// не завязан на Алерты — при появлении «Сохранённых запросов» (раздел
// «Данные») тот же компонент переиспользуется без изменений.
export default function QueryBuilder({
  value,
  onChange,
  fieldDefs,
}: {
  value: FilterNode;
  onChange: (node: FilterNode) => void;
  fieldDefs: FieldDef[];
}) {
  const group: FilterNode =
    value.conditions || value.match
      ? { match: value.match ?? "all", conditions: value.conditions ?? [] }
      : { match: "all", conditions: value.field ? [value] : [] };

  return <GroupEditor node={group} onChange={onChange} fieldDefs={fieldDefs} depth={0} />;
}

function GroupEditor({
  node,
  onChange,
  fieldDefs,
  depth,
}: {
  node: FilterNode;
  onChange: (node: FilterNode) => void;
  fieldDefs: FieldDef[];
  depth: number;
}) {
  const conditions = node.conditions ?? [];

  function updateChild(i: number, child: FilterNode) {
    const next = [...conditions];
    next[i] = child;
    onChange({ ...node, conditions: next });
  }
  function removeChild(i: number) {
    onChange({ ...node, conditions: conditions.filter((_, idx) => idx !== i) });
  }
  function addCondition() {
    const first = fieldDefs[0];
    onChange({
      ...node,
      conditions: [...conditions, { field: first.field, op: first.op, value: first.kind === "enum" ? [] : "" }],
    });
  }
  function addGroup() {
    onChange({ ...node, conditions: [...conditions, { match: "all", conditions: [] }] });
  }

  return (
    <div className={depth > 0 ? "rounded-lg border border-border p-3" : ""}>
      <div className="mb-2 flex items-center gap-2 text-xs">
        <span className="text-muted">Условие:</span>
        <div className="flex overflow-hidden rounded-md border border-border">
          {(["all", "any"] as const).map((m) => (
            <button
              key={m}
              onClick={() => onChange({ ...node, match: m })}
              className={`px-2 py-1 ${(node.match ?? "all") === m ? "bg-accent text-white" : "bg-bg text-muted"}`}
            >
              {m === "all" ? "ВСЕ условия" : "ЛЮБОЕ условие"}
            </button>
          ))}
        </div>
      </div>

      <div className="space-y-2">
        {conditions.map((child, i) => (
          <div key={i} className="flex items-start gap-2">
            {i > 0 && (
              <span className="mt-2 w-8 shrink-0 text-center text-[10px] uppercase text-muted">
                {node.match === "any" ? "или" : "и"}
              </span>
            )}
            {i === 0 && <span className="mt-2 w-8 shrink-0" />}
            <div className="flex-1">
              {child.conditions || child.match ? (
                <GroupEditor
                  node={{ match: child.match ?? "all", conditions: child.conditions ?? [] }}
                  onChange={(g) => updateChild(i, g)}
                  fieldDefs={fieldDefs}
                  depth={depth + 1}
                />
              ) : (
                <ConditionEditor node={child} onChange={(c) => updateChild(i, c)} fieldDefs={fieldDefs} />
              )}
            </div>
            <button onClick={() => removeChild(i)} className="mt-1 text-muted hover:text-red-400" aria-label="Удалить условие">
              ×
            </button>
          </div>
        ))}
      </div>

      <div className="mt-2 flex gap-2">
        <button onClick={addCondition} className="rounded-md border border-border px-2 py-1 text-xs text-muted hover:bg-fg/5">
          + условие
        </button>
        <button onClick={addGroup} className="rounded-md border border-border px-2 py-1 text-xs text-muted hover:bg-fg/5">
          + группа
        </button>
      </div>
    </div>
  );
}

function ConditionEditor({
  node,
  onChange,
  fieldDefs,
}: {
  node: FilterNode;
  onChange: (node: FilterNode) => void;
  fieldDefs: FieldDef[];
}) {
  const def = fieldDefs.find((f) => f.field === node.field) ?? fieldDefs[0];

  function setField(fieldName: string) {
    const next = fieldDefs.find((f) => f.field === fieldName)!;
    onChange({ field: next.field, op: next.op, value: next.kind === "enum" ? [] : next.kind === "bool" ? true : "" });
  }

  return (
    <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-bg p-2">
      <select
        value={def.field}
        onChange={(e) => setField(e.target.value)}
        className="rounded-md border border-border bg-card px-2 py-1 text-xs"
      >
        {fieldDefs.map((f) => (
          <option key={f.field} value={f.field}>
            {f.label}
          </option>
        ))}
      </select>

      {def.kind === "enum" && (
        <div className="flex flex-wrap gap-1">
          {(def.options ?? []).map((opt) => {
            const selected = Array.isArray(node.value) && (node.value as string[]).includes(opt.value);
            return (
              <button
                key={opt.value}
                onClick={() => {
                  const current = Array.isArray(node.value) ? (node.value as string[]) : [];
                  const next = selected ? current.filter((v) => v !== opt.value) : [...current, opt.value];
                  onChange({ ...node, value: next });
                }}
                className={`rounded px-2 py-0.5 text-xs ${selected ? "bg-accent text-white" : "bg-card text-muted"}`}
              >
                {opt.label}
              </button>
            );
          })}
        </div>
      )}

      {def.kind === "string" && (
        <input
          value={typeof node.value === "string" ? node.value : ""}
          onChange={(e) => onChange({ ...node, value: e.target.value })}
          placeholder="значение"
          className="w-40 rounded-md border border-border bg-card px-2 py-1 text-xs"
        />
      )}

      {def.kind === "int" && (
        <input
          type="number"
          value={typeof node.value === "number" ? node.value : ""}
          onChange={(e) => onChange({ ...node, value: Number(e.target.value) })}
          className="w-24 rounded-md border border-border bg-card px-2 py-1 text-xs"
        />
      )}

      {def.kind === "bool" && (
        <select
          value={node.value === false ? "false" : "true"}
          onChange={(e) => onChange({ ...node, value: e.target.value === "true" })}
          className="rounded-md border border-border bg-card px-2 py-1 text-xs"
        >
          <option value="true">Да</option>
          <option value="false">Нет</option>
        </select>
      )}
    </div>
  );
}
