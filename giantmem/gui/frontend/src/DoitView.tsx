import { Fragment, useCallback, useEffect, useMemo, useState } from "react";
import { ClipboardSetText } from "../wailsjs/runtime/runtime";
import {
  DoitAdd,
  DoitDelete,
  DoitLists,
  DoitMtime,
  DoitSetStatus,
  DoitUpdate,
} from "../wailsjs/go/main/App";
import { main } from "../wailsjs/go/models";

type Status = "pending" | "in_progress" | "done";
type ListView = {
  name: string;
  error?: string;
  todos: main.DoitTodo[];
  notes: main.DoitNote[];
  open: number;
  active: boolean;
  hits: number;
};

// the tmux manager's toggle: pending -> in progress -> done -> pending
const NEXT: Record<Status, Status> = { pending: "in_progress", in_progress: "done", done: "pending" };
const CYCLE_LABEL: Record<Status, string> = { pending: "▸ start", in_progress: "✓ complete", done: "↺ reopen" };
const STATUS_LABEL: Record<Status, string> = { pending: "pending", in_progress: "in progress", done: "done" };
const GLYPH: Record<Status, string> = { pending: "·", in_progress: "▸", done: "✓" };
const BUCKETS = ["in progress", "sequence", "critical", "urgent", "important", "default", "done"];
const GROUPS = ["active", "open", "empty"];
const PRIOS = ["critical", "urgent", "important"];

const status = (t: main.DoitTodo): Status => (t.done ? "done" : t.in_progress ? "in_progress" : "pending");

function bucket(t: main.DoitTodo): number {
  if (t.done) return 6;
  if (t.in_progress) return 0;
  if (t.sequence > 0) return 1;
  const p = PRIOS.indexOf(t.priorities);
  return p >= 0 ? p + 2 : 5;
}

function sortTodos(todos: main.DoitTodo[]): main.DoitTodo[] {
  return [...todos].sort(
    (a, b) =>
      bucket(a) - bucket(b) ||
      (a.done ? b.completed_at - a.completed_at : 0) ||
      (a.sequence || 0) - (b.sequence || 0) ||
      a.order_index - b.order_index,
  );
}

const group = (v: ListView) => (v.active ? 0 : v.open > 0 ? 1 : 2);

function dueLabel(due: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(due || "");
  if (!m) return "";
  const now = new Date();
  const days = Math.round(
    (new Date(+m[1], +m[2] - 1, +m[3], 12).getTime() -
      new Date(now.getFullYear(), now.getMonth(), now.getDate(), 12).getTime()) /
      86400000,
  );
  if (days < 0) return `overdue ${-days}d`;
  if (days === 0) return "due today";
  if (days === 1) return "due tomorrow";
  return `in ${days}d`;
}

const when = (n: number) => (n ? new Date(n > 1e12 ? n : n * 1000).toLocaleString() : "");

function Hl({ text, q }: { text: string; q: string }) {
  if (!q || !text) return <>{text}</>;
  const lower = text.toLowerCase();
  const out: (string | JSX.Element)[] = [];
  let i = 0;
  for (let j = lower.indexOf(q); j >= 0; j = lower.indexOf(q, i)) {
    if (j > i) out.push(text.slice(i, j));
    out.push(<mark key={j}>{text.slice(j, j + q.length)}</mark>);
    i = j + q.length;
  }
  out.push(text.slice(i));
  return <>{out}</>;
}

function snippet(s: string, q: string): string {
  const i = s.toLowerCase().indexOf(q);
  if (!q || i < 0) return "";
  const from = Math.max(0, i - 40);
  return `${from ? "…" : ""}${s.slice(from, i + q.length + 60).replace(/\s+/g, " ")}…`;
}

export type Doit = ReturnType<typeof useDoit>;

export function useDoit(active: boolean, query: string, reloadKey: number, onErr: (e: string) => void) {
  const [lists, setLists] = useState<main.DoitList[]>([]);
  const [selList, setSelList] = useState("");
  const [selKey, setSelKey] = useState("");
  const [editing, setEditing] = useState(false);
  const [armed, setArmed] = useState("");
  const q = query.trim().toLowerCase();

  const load = useCallback(
    () =>
      DoitLists()
        .then((l) => setLists(l || []))
        .catch((e) => onErr(String(e))),
    [onErr],
  );
  useEffect(() => {
    if (active) load();
  }, [active, reloadKey, load]);
  // nvim, tmux and the mcp server write the same files; follow them
  useEffect(() => {
    if (!active) return;
    let last = 0;
    const id = window.setInterval(async () => {
      try {
        const m = await DoitMtime();
        if (last && m !== last) load();
        last = m;
      } catch {}
    }, 2000);
    return () => window.clearInterval(id);
  }, [active, load]);

  const views = useMemo(() => {
    const hit = (s: string) => (s || "").toLowerCase().includes(q);
    const out: ListView[] = [];
    for (const l of lists) {
      const todos = sortTodos(l.todos || []);
      const notes = l.notes || [];
      const base = {
        name: l.name,
        error: l.error,
        open: todos.filter((t) => !t.done).length,
        active: todos.some((t) => t.in_progress && !t.done),
      };
      if (!q) {
        out.push({ ...base, todos, notes, hits: 0 });
        continue;
      }
      const mt = todos.filter((t) => hit(t.text) || hit(t.description));
      const mn = notes.filter((n) => hit(n.title) || hit(n.body));
      if (mt.length || mn.length) out.push({ ...base, todos: mt, notes: mn, hits: mt.length + mn.length });
      else if (hit(l.name)) out.push({ ...base, todos, notes, hits: 0 });
    }
    return out.sort(
      (a, b) =>
        group(a) - group(b) ||
        Number(b.name === "daily") - Number(a.name === "daily") ||
        a.name.localeCompare(b.name),
    );
  }, [lists, q]);

  const list = views.find((v) => v.name === selList) || views[0];
  const keys = list ? [...list.todos.map((t) => t.id), ...list.notes.map((n) => `note:${n.id}`)] : [];
  const key = keys.includes(selKey) ? selKey : keys[0] || "";
  const todo = list?.todos.find((t) => t.id === key);
  const note = key.startsWith("note:") ? list?.notes.find((n) => `note:${n.id}` === key) : undefined;

  useEffect(() => {
    setEditing(false);
    setArmed("");
  }, [list?.name, key]);

  const run = (p: Promise<unknown>) =>
    p
      .then(() => {
        load();
        return true;
      })
      .catch((e) => {
        onErr(String(e));
        return false;
      });

  const d = {
    q,
    views,
    list,
    keys,
    key,
    todo,
    note,
    editing,
    armed,
    setEditing,
    pickList: (name: string) => setSelList(name),
    pick: (k: string) => setSelKey(k),
    jump: (name: string, id: string) => {
      setSelList(name);
      setSelKey(id);
    },
    cycle: (t: main.DoitTodo) => list && run(DoitSetStatus(list.name, t.id, NEXT[status(t)])),
    setStatus: (t: main.DoitTodo, s: Status) => list && run(DoitSetStatus(list.name, t.id, s)),
    save: (t: main.DoitTodo, e: main.DoitEdit) =>
      list && run(DoitUpdate(list.name, t.id, e)).then((ok) => ok && setEditing(false)),
    remove: (t: main.DoitTodo) => {
      if (!list) return;
      if (armed !== t.id) {
        setArmed(t.id);
        return;
      }
      const i = keys.indexOf(t.id);
      setSelKey(keys[i + 1] || keys[i - 1] || "");
      setArmed("");
      run(DoitDelete(list.name, t.id));
    },
    add: async (text: string) => {
      if (!list) return false;
      try {
        const id = await DoitAdd(list.name, text);
        await load();
        setSelKey(id);
        return true;
      } catch (e) {
        onErr(String(e));
        return false;
      }
    },
  };

  // j/k item, J/K list, space cycles, x completes, e edits, d twice deletes
  useEffect(() => {
    if (!active) return;
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const step = (arr: string[], cur: string, by: number) =>
        arr[Math.min(arr.length - 1, Math.max(0, arr.indexOf(cur) + by))];
      if (e.key === "j" || e.key === "k") {
        if (keys.length) setSelKey(step(keys, key, e.key === "j" ? 1 : -1));
      } else if (e.key === "J" || e.key === "K") {
        const names = views.map((v) => v.name);
        if (names.length && list) setSelList(step(names, list.name, e.key === "J" ? 1 : -1));
      } else if (todo && e.key === " ") {
        d.cycle(todo);
      } else if (todo && e.key === "x") {
        d.setStatus(todo, "done");
      } else if (todo && e.key === "e") {
        setEditing(true);
      } else if (todo && e.key === "d") {
        d.remove(todo);
      } else {
        return;
      }
      e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  return d;
}

export function DoitSidebar({ d }: { d: Doit }) {
  if (!d.views.length) return <div className="detail-empty">{d.q ? "no match" : "no lists"}</div>;
  return (
    <>
      {d.views.map((v, i) => (
        <Fragment key={v.name}>
          {(i === 0 || group(v) !== group(d.views[i - 1])) && (
            <div className="repo-label">{GROUPS[group(v)]}</div>
          )}
          <div
            className={`facet-row ${v.name === d.list?.name ? "selected" : ""}`}
            onClick={() => d.pickList(v.name)}
            title={v.error || v.name}
          >
            <span>
              <Hl text={v.name} q={d.q} />
              {v.error && <span className="doit-err"> ⚠</span>}
            </span>
            <span className="count">{d.q && v.hits ? v.hits : v.open}</span>
          </div>
        </Fragment>
      ))}
    </>
  );
}

export function DoitItems({ d }: { d: Doit }) {
  const [draft, setDraft] = useState("");
  const l = d.list;
  if (!l) return null;
  return (
    <>
      <div className="doit-add">
        <input
          type="text"
          placeholder={`add to ${l.name}… (enter)`}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && draft.trim()) d.add(draft).then((ok) => ok && setDraft(""));
          }}
        />
      </div>
      {l.error && <div className="doit-section doit-err">unreadable: {l.error}</div>}
      {!l.todos.length && !l.notes.length && <div className="detail-empty">no items</div>}
      {l.todos.map((t, i) => {
        const st = status(t);
        const snip = d.q && !t.text.toLowerCase().includes(d.q) ? snippet(t.description, d.q) : "";
        return (
          <Fragment key={t.id}>
            {(i === 0 || bucket(t) !== bucket(l.todos[i - 1])) && (
              <div className="doit-section">{BUCKETS[bucket(t)]}</div>
            )}
            <div
              className={`doit-row ${st} ${t.id === d.key ? "selected" : ""}`}
              onClick={() => d.pick(t.id)}
            >
              <button
                className="doit-glyph"
                title={`${STATUS_LABEL[st]} · click: ${CYCLE_LABEL[st]}`}
                onClick={(e) => {
                  e.stopPropagation();
                  d.cycle(t);
                }}
              >
                {GLYPH[st]}
              </button>
              <div className="doit-text">
                <Hl text={t.text} q={d.q} />
                {snip && (
                  <div className="doit-snip">
                    <Hl text={snip} q={d.q} />
                  </div>
                )}
              </div>
              {t.priorities && <span className={`chip prio-${t.priorities}`}>{t.priorities}</span>}
              {t.due_date && <span className="chip">{dueLabel(t.due_date)}</span>}
              {t.twin && <span className="chip" title={`twin in ${t.twin.list}`}>↔ {t.twin.list}</span>}
            </div>
          </Fragment>
        );
      })}
      {l.notes.length > 0 && <div className="doit-section">list notes</div>}
      {l.notes.map((n) => (
        <div
          key={n.id}
          className={`doit-row ${`note:${n.id}` === d.key ? "selected" : ""}`}
          onClick={() => d.pick(`note:${n.id}`)}
        >
          <span className="doit-glyph">✎</span>
          <div className="doit-text">
            <Hl text={n.title || (n.body || "").split("\n")[0]} q={d.q} />
          </div>
        </div>
      ))}
    </>
  );
}

export function DoitDetail({ d }: { d: Doit }) {
  const t = d.todo;
  const [form, setForm] = useState<main.DoitEdit>({ text: "", description: "", priority: "", due: "" });
  useEffect(() => {
    if (d.editing && t) {
      setForm({ text: t.text, description: t.description, priority: t.priorities || "", due: t.due_date || "" });
    }
  }, [d.editing]);

  if (d.note) {
    return (
      <>
        <header className="detail-head">
          <h1 style={{ marginBottom: 0 }}>
            <Hl text={d.note.title || "note"} q={d.q} />
          </h1>
          <div className="meta">
            <span className="chip">list note</span>
            <span>{d.list?.name}</span>
            {d.note.updated_at > 0 && <span>updated: {when(d.note.updated_at)}</span>}
          </div>
        </header>
        <pre className="doit-notes">
          <Hl text={d.note.body} q={d.q} />
        </pre>
      </>
    );
  }
  if (!t || !d.list) return <div className="detail-empty">{d.list ? "pick an item" : "pick a list"}</div>;

  const st = status(t);
  const save = () => d.save(t, form);
  return (
    <>
      <header className="detail-head">
        <h1 style={{ marginBottom: 0 }}>
          <Hl text={t.text} q={d.q} />
        </h1>
        <div className="meta">
          <span className={`chip status-${st}`}>{STATUS_LABEL[st]}</span>
          {t.priorities && <span className={`chip prio-${t.priorities}`}>{t.priorities}</span>}
          {t.due_date && (
            <span className="chip">
              {t.due_date} · {dueLabel(t.due_date)}
            </span>
          )}
          {t.sequence > 0 && <span className="chip">seq {t.sequence}</span>}
          <span>{d.list.name}</span>
          {t.created_at > 0 && <span>created: {when(t.created_at)}</span>}
          {t.done && t.completed_at > 0 && <span>completed: {when(t.completed_at)}</span>}
          {t.twin && (
            <button className="toggle-pill" onClick={() => d.jump(t.twin!.list, t.twin!.id)}>
              twin in {t.twin.list} ›
            </button>
          )}
          <button className="toggle-pill" onClick={() => ClipboardSetText(t.id)} title={t.id}>
            copy id
          </button>
        </div>
      </header>
      {!d.editing && (
        <div className="doit-actions">
          <button onClick={() => d.cycle(t)} title="space">
            {CYCLE_LABEL[st]}
          </button>
          {st === "pending" && (
            <button onClick={() => d.setStatus(t, "done")} title="x">
              ✓ complete
            </button>
          )}
          <button onClick={() => d.setEditing(true)} title="e">
            edit
          </button>
          <button
            className={d.armed === t.id ? "doit-danger" : ""}
            onClick={() => d.remove(t)}
            title="d, twice"
          >
            {d.armed === t.id ? (t.twin ? `confirm: delete here and in ${t.twin.list}` : "confirm delete") : "delete"}
          </button>
        </div>
      )}
      {d.editing ? (
        <div
          className="doit-edit"
          onKeyDown={(e) => {
            if (e.key === "Enter" && e.metaKey) save();
            if (e.key === "Escape") d.setEditing(false);
          }}
        >
          <label>
            text
            <input
              type="text"
              autoFocus
              value={form.text}
              onChange={(e) => setForm({ ...form, text: e.target.value })}
            />
          </label>
          <div className="doit-edit-row">
            <label>
              priority
              <select value={form.priority} onChange={(e) => setForm({ ...form, priority: e.target.value })}>
                <option value="">none</option>
                {PRIOS.map((p) => (
                  <option key={p} value={p}>
                    {p}
                  </option>
                ))}
              </select>
            </label>
            <label>
              due
              <input type="date" value={form.due} onChange={(e) => setForm({ ...form, due: e.target.value })} />
            </label>
          </div>
          <label>
            notes
            <textarea
              rows={18}
              value={form.description}
              onChange={(e) => setForm({ ...form, description: e.target.value })}
            />
          </label>
          <div className="doit-actions">
            <button className="active" onClick={save} title="cmd+enter">
              save
            </button>
            <button onClick={() => d.setEditing(false)} title="esc">
              cancel
            </button>
          </div>
        </div>
      ) : (
        <pre className="doit-notes">{t.description ? <Hl text={t.description} q={d.q} /> : "no notes"}</pre>
      )}
      <div className="filter-hint doit-keys">
        <span className="kbd">j</span>/<span className="kbd">k</span> item · <span className="kbd">J</span>/
        <span className="kbd">K</span> list · <span className="kbd">space</span> cycle ·{" "}
        <span className="kbd">x</span> complete · <span className="kbd">e</span> edit ·{" "}
        <span className="kbd">d</span> delete
      </div>
    </>
  );
}
