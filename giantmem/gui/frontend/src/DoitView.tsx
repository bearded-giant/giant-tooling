import { Fragment, useCallback, useEffect, useMemo, useState } from "react";
import { ClipboardSetText } from "../wailsjs/runtime/runtime";
import {
  DoitAdd,
  DoitDelete,
  DoitListCreate,
  DoitListDelete,
  DoitListRename,
  DoitLists,
  DoitMove,
  DoitMtime,
  DoitNoteAdd,
  DoitNoteDelete,
  DoitNoteUpdate,
  DoitSetStatus,
  DoitUpdate,
} from "../wailsjs/go/main/App";
import { main } from "../wailsjs/go/models";

type Status = "pending" | "in_progress" | "done";
type Facet = { worktree: string; feature: string; type: string };
type ListSort = "recent" | "oldest" | "name";
type ListView = {
  name: string;
  error?: string;
  todos: main.DoitTodo[];
  all: main.DoitTodo[];
  notes: main.DoitNote[];
  open: number;
  doneHidden: number;
  active: boolean;
  updated: number;
  hits: number;
};

// the tmux manager's toggle: pending -> in progress -> done -> pending
const NEXT: Record<Status, Status> = { pending: "in_progress", in_progress: "done", done: "pending" };
const CYCLE_LABEL: Record<Status, string> = { pending: "▸ start", in_progress: "✓ complete", done: "↺ reopen" };
const STATUS_LABEL: Record<Status, string> = { pending: "pending", in_progress: "in progress", done: "done" };
const GLYPH: Record<Status, string> = { pending: "·", in_progress: "▸", done: "✓" };
const BUCKETS = ["in progress", "sequence", "critical", "urgent", "important", "default", "done"];
const BUCKET_CLASS = ["in_progress", "sequence", "prio-critical", "prio-urgent", "prio-important", "", "done"];
const PRIOS = ["critical", "urgent", "important"];
const FACETS: (keyof Facet)[] = ["worktree", "feature", "type"];
const NO_FACET: Facet = { worktree: "", feature: "", type: "" };
const SORTS: ListSort[] = ["recent", "oldest", "name"];
const SORT_LABEL: Record<ListSort, string> = { recent: "recent first", oldest: "oldest first", name: "a-z" };
const NEW_NOTE = "note:new";
const OWED_LIST = "daily";

const status = (t: main.DoitTodo): Status => (t.done ? "done" : t.in_progress ? "in_progress" : "pending");

// mcp todo-text.js shape: "claude: [type] N. body (dep on #M, #K)"
const SHAPE = /^(claude:\s*)?(?:\[([^\][]+)\]\s*)?(?:(\d+[a-z]*)\.\s+)?([\s\S]*)$/i;
const DEPS = /\s*\(dep on ([^)]*)\)\s*$/i;

export function parseText(text: string) {
  const m = SHAPE.exec(text || "")!;
  let body = m[4] || "";
  const dm = DEPS.exec(body);
  if (dm) body = body.slice(0, dm.index).trimEnd();
  return {
    claude: Boolean(m[1]),
    type: m[2] || "",
    rank: m[3] || "",
    body,
    deps: dm ? dm[1].replace(/#/g, "").trim() : "",
  };
}

const splitDeps = (s: string) => s.split(/[,\s]+/).map((d) => d.replace(/^#/, "")).filter(Boolean);

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

const facetOf = (t: main.DoitTodo, f: keyof Facet) => (f === "type" ? parseText(t.text).type : t[f] || "");

function dueInfo(due: string): { label: string; cls: string } {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(due || "");
  if (!m) return { label: "", cls: "" };
  const now = new Date();
  const days = Math.round(
    (new Date(+m[1], +m[2] - 1, +m[3], 12).getTime() -
      new Date(now.getFullYear(), now.getMonth(), now.getDate(), 12).getTime()) /
      86400000,
  );
  if (days < 0) return { label: `overdue ${-days}d`, cls: "due-over" };
  if (days === 0) return { label: "due today", cls: "due-today" };
  if (days === 1) return { label: "due tomorrow", cls: "due-soon" };
  return { label: `in ${days}d`, cls: days <= 7 ? "due-soon" : "" };
}

const when = (n: number) => (n ? new Date(n > 1e12 ? n : n * 1000).toLocaleString() : "");

function age(unix: number): string {
  if (!unix) return "";
  const s = Math.max(0, Date.now() / 1000 - unix);
  if (s < 3600) return `${Math.max(1, Math.floor(s / 60))}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  if (s < 86400 * 30) return `${Math.floor(s / 86400)}d`;
  return `${Math.floor(s / (86400 * 30))}mo`;
}

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
  const [facet, setFacet] = useState<Facet>(NO_FACET);
  const [listEdit, setListEdit] = useState<"" | "new" | "rename">("");
  const [sort, setSortState] = useState<ListSort>(() => {
    const s = localStorage.getItem("gm.doit.sort") as ListSort | null;
    return s && SORTS.includes(s) ? s : "recent";
  });
  const setSort = (s: ListSort) => {
    localStorage.setItem("gm.doit.sort", s);
    setSortState(s);
  };
  // long lists: done items stay folded until asked for
  const [showDone, setShowDoneState] = useState(() => localStorage.getItem("gm.doit.showDone") === "1");
  const setShowDone = (v: boolean) => {
    localStorage.setItem("gm.doit.showDone", v ? "1" : "0");
    setShowDoneState(v);
  };
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
    const facetOk = (t: main.DoitTodo) => FACETS.every((f) => !facet[f] || facetOf(t, f) === facet[f]);
    const out: ListView[] = [];
    for (const l of lists) {
      const all = sortTodos(l.todos || []);
      const notes = l.notes || [];
      const faceted = all.filter(facetOk);
      const todos = showDone || q ? faceted : faceted.filter((t) => !t.done);
      const base = {
        name: l.name,
        error: l.error,
        all,
        open: all.filter((t) => !t.done).length,
        doneHidden: faceted.length - todos.length,
        active: all.some((t) => t.in_progress && !t.done),
        updated: l.updated_at,
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
    // daily is the owed inbox and stays pinned on top
    return out.sort(
      (a, b) =>
        Number(b.name === OWED_LIST) - Number(a.name === OWED_LIST) ||
        (sort === "name" ? 0 : sort === "recent" ? b.updated - a.updated : a.updated - b.updated) ||
        a.name.localeCompare(b.name),
    );
  }, [lists, q, facet, showDone, sort]);

  const list = views.find((v) => v.name === selList) || views[0];
  const names = lists.map((l) => l.name);
  // notes render above todos, but a freshly opened list lands on its first todo
  const keys = list ? [...list.notes.map((n) => `note:${n.id}`), ...list.todos.map((t) => t.id)] : [];
  const key = (list && selKey === NEW_NOTE) || keys.includes(selKey) ? selKey : list?.todos[0]?.id || keys[0] || "";
  const todo = list?.todos.find((t) => t.id === key);
  const note = list?.notes.find((n) => `note:${n.id}` === key);
  const facets = useMemo(() => {
    const out = {} as Record<keyof Facet, string[]>;
    for (const f of FACETS) {
      out[f] = [...new Set((list?.all || []).map((t) => facetOf(t, f)).filter(Boolean))].sort();
    }
    return out;
  }, [list?.all]);

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

  const pickList = (name: string) => {
    setSelList(name);
    setSelKey("");
    setFacet(NO_FACET);
    setListEdit("");
  };

  const d = {
    q,
    views,
    names,
    list,
    keys,
    key,
    todo,
    note,
    facet,
    facets,
    sort,
    showDone,
    listEdit,
    creatingNote: key === NEW_NOTE,
    editing,
    armed,
    setEditing,
    setFacet: (f: keyof Facet, v: string) => setFacet({ ...facet, [f]: facet[f] === v ? "" : v }),
    setSort,
    cycleSort: () => setSort(SORTS[(SORTS.indexOf(sort) + 1) % SORTS.length]),
    setShowDone,
    setListEdit,
    pickList,
    pick: (k: string) => setSelKey(k),
    jump: (name: string, id: string) => {
      setSelList(name);
      setSelKey(id);
      setFacet(NO_FACET);
    },
    cycle: (t: main.DoitTodo) => list && run(DoitSetStatus(list.name, t.id, NEXT[status(t)])),
    setStatus: (t: main.DoitTodo, s: Status) => list && run(DoitSetStatus(list.name, t.id, s)),
    save: (t: main.DoitTodo, e: main.DoitEdit) =>
      list && run(DoitUpdate(list.name, t.id, e)).then((ok) => ok && setEditing(false)),
    move: (t: main.DoitTodo, to: string) =>
      list &&
      run(DoitMove(list.name, t.id, to)).then((ok) => {
        if (ok) d.jump(to, t.id);
      }),
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
    newNote: () => list && setSelKey(NEW_NOTE),
    saveNote: async (n: main.DoitNote | undefined, title: string, body: string) => {
      if (!list) return;
      try {
        if (n) await DoitNoteUpdate(list.name, n.id, title, body);
        else setSelKey(`note:${await DoitNoteAdd(list.name, title, body)}`);
        await load();
        setEditing(false);
      } catch (e) {
        onErr(String(e));
      }
    },
    removeNote: (n: main.DoitNote) => {
      if (!list) return;
      const k = `note:${n.id}`;
      if (armed !== k) {
        setArmed(k);
        return;
      }
      const i = keys.indexOf(k);
      setSelKey(keys[i + 1] || keys[i - 1] || "");
      setArmed("");
      run(DoitNoteDelete(list.name, n.id));
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
    createList: (name: string) =>
      run(DoitListCreate(name)).then((ok) => {
        if (ok) pickList(name.trim());
        return ok;
      }),
    renameList: (name: string) =>
      !!list &&
      run(DoitListRename(list.name, name)).then((ok) => {
        if (ok) pickList(name.trim());
        return ok;
      }),
    removeList: () => {
      if (!list) return;
      const k = `list:${list.name}`;
      if (armed !== k) {
        setArmed(k);
        return;
      }
      setArmed("");
      run(DoitListDelete(list.name)).then((ok) => ok && pickList(""));
    },
  };

  // j/k item, J/K list, space cycles, x completes, e edits, d twice deletes, n new note
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
      } else if ((todo || note) && e.key === "e") {
        setEditing(true);
      } else if (todo && e.key === "d") {
        d.remove(todo);
      } else if (note && e.key === "d") {
        d.removeNote(note);
      } else if (list && e.key === "n") {
        d.newNote();
      } else if (e.key === "N") {
        setListEdit("new");
      } else if (e.key === "z") {
        setShowDone(!showDone);
      } else if (e.key === "o") {
        d.cycleSort();
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

// the text's own parts carry the scan signal: claude marker strong, type as a
// dim mono tag, rank muted, body plain; type and scope tokens click into the filter
function TodoText({ t, q, d, big }: { t: main.DoitTodo; q: string; d: Doit; big?: boolean }) {
  const p = parseText(t.text);
  return (
    <span className={big ? "doit-title" : undefined}>
      {p.claude && <span className="tok tok-claude">claude</span>}
      {p.type && (
        <span
          className={`tok tok-type ${d.facet.type === p.type ? "on" : ""}`}
          title={`filter type ${p.type}`}
          onClick={(e) => {
            e.stopPropagation();
            d.setFacet("type", p.type);
          }}
        >
          {p.type}
        </span>
      )}
      {p.rank && <span className="tok tok-rank">{p.rank}.</span>}
      <Hl text={p.body} q={q} />
      {p.deps && <span className="tok tok-deps">dep on {p.deps}</span>}
    </span>
  );
}

function ScopeTok({ f, v, d }: { f: "worktree" | "feature"; v: string; d: Doit }) {
  return (
    <span
      className={`tok tok-scope ${d.facet[f] === v ? "on" : ""}`}
      title={`filter ${f} ${v}`}
      onClick={(e) => {
        e.stopPropagation();
        d.setFacet(f, v);
      }}
    >
      {f === "worktree" ? "wt" : "feat"} {v}
    </span>
  );
}

function NameInput({
  initial,
  placeholder,
  onSubmit,
  onCancel,
}: {
  initial: string;
  placeholder: string;
  onSubmit: (name: string) => Promise<boolean>;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial);
  return (
    <input
      type="text"
      autoFocus
      className="doit-name"
      placeholder={placeholder}
      value={name}
      onChange={(e) => setName(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter" && name.trim()) onSubmit(name);
        if (e.key === "Escape") onCancel();
      }}
    />
  );
}

export function DoitSidebar({ d }: { d: Doit }) {
  return (
    <>
      <div className="doit-section doit-section-row">
        <span>lists</span>
        <span className="doit-section-tools">
          <button className="toggle-pill" onClick={d.cycleSort} title="o · sort lists">
            {SORT_LABEL[d.sort]}
          </button>
          <button className="toggle-pill" onClick={() => d.setListEdit("new")} title="N">
            + list
          </button>
        </span>
      </div>
      {d.listEdit === "new" && (
        <div className="doit-add">
          <NameInput
            initial=""
            placeholder="new list name… (enter)"
            onSubmit={d.createList}
            onCancel={() => d.setListEdit("")}
          />
        </div>
      )}
      {!d.views.length && <div className="detail-empty">{d.q ? "no match" : "no lists"}</div>}
      {d.views.map((v, i) => (
        <Fragment key={v.name}>
          {i === 1 && d.views[0].name === OWED_LIST && <div className="doit-sep" />}
          <div
            data-nav
            className={`facet-row doit-list-row ${v.name === d.list?.name ? "selected" : ""} ${v.open ? "" : "empty"}`}
            onClick={() => d.pickList(v.name)}
            title={v.error || v.name}
          >
            <span className="doit-list-name">
              {v.active && <span className="tok-active">▸</span>}
              <Hl text={v.name} q={d.q} />
              {v.error && <span className="doit-err"> ⚠</span>}
              {v.notes.length > 0 && <span className="doit-snip"> ✎{v.notes.length}</span>}
            </span>
            <span className="doit-list-right">
              <span className="doit-age">{age(v.updated)}</span>
              <span className="count">{d.q && v.hits ? v.hits : v.open}</span>
            </span>
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
  const armed = d.armed === `list:${l.name}`;
  const hasFacets = FACETS.some((f) => d.facets[f].length > 1 || d.facet[f]);
  return (
    <>
      <div className="doit-list-head">
        {d.listEdit === "rename" ? (
          <NameInput
            initial={l.name}
            placeholder="rename list… (enter)"
            onSubmit={d.renameList}
            onCancel={() => d.setListEdit("")}
          />
        ) : (
          <strong>{l.name}</strong>
        )}
        <span className="doit-snip">
          {l.open} open · {l.all.length - l.open} done
        </span>
        {l.name !== OWED_LIST && d.listEdit !== "rename" && (
          <>
            <button className="toggle-pill" onClick={() => d.setListEdit("rename")}>
              rename
            </button>
            <button className={`toggle-pill ${armed ? "doit-danger" : ""}`} onClick={d.removeList}>
              {armed ? "confirm delete list" : "delete"}
            </button>
          </>
        )}
      </div>
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
      {hasFacets && (
        <div className="doit-facets">
          {FACETS.map(
            (f) =>
              (d.facets[f].length > 1 || d.facet[f]) && (
                <select
                  key={f}
                  className={d.facet[f] ? "on" : ""}
                  value={d.facet[f]}
                  onChange={(e) => d.setFacet(f, e.target.value)}
                  title={f}
                >
                  <option value="">{f}: all</option>
                  {d.facets[f].map((v) => (
                    <option key={v} value={v}>
                      {v}
                    </option>
                  ))}
                </select>
              ),
          )}
        </div>
      )}
      {l.error && <div className="doit-section doit-err">unreadable: {l.error}</div>}
      {(!d.q || l.notes.length > 0) && (
        <div className="doit-section doit-section-row">
          <span>list notes</span>
          <button className="toggle-pill" onClick={d.newNote} title="n">
            + note
          </button>
        </div>
      )}
      {d.creatingNote && (
        <div className="doit-row selected">
          <span className="doit-glyph">✎</span>
          <div className="doit-text doit-snip">new note…</div>
        </div>
      )}
      {l.notes.map((n) => {
        const label = n.title || (n.body || "").split("\n")[0] || "untitled";
        const snip = d.q && !label.toLowerCase().includes(d.q) ? snippet(n.body, d.q) : "";
        return (
          <div
            key={n.id}
            data-nav
            className={`doit-row ${`note:${n.id}` === d.key ? "selected" : ""}`}
            onClick={() => d.pick(`note:${n.id}`)}
          >
            <span className="doit-glyph">✎</span>
            <div className="doit-text">
              <Hl text={label} q={d.q} />
              {snip && (
                <div className="doit-snip">
                  <Hl text={snip} q={d.q} />
                </div>
              )}
            </div>
          </div>
        );
      })}
      {!l.todos.length && !d.q && <div className="doit-section">no items</div>}
      {l.todos.map((t, i) => {
        const st = status(t);
        const snip = d.q && !t.text.toLowerCase().includes(d.q) ? snippet(t.description, d.q) : "";
        // a list where every item shares the value says it once in the facet row, not on every row
        const showWt = Boolean(t.worktree) && d.facets.worktree.length > 1 && !d.facet.worktree;
        const showFeat = Boolean(t.feature) && d.facets.feature.length > 1 && !d.facet.feature;
        const due = dueInfo(t.due_date);
        return (
          <Fragment key={t.id}>
            {(i === 0 || bucket(t) !== bucket(l.todos[i - 1])) && (
              <div className={`doit-section doit-section-row ${BUCKET_CLASS[bucket(t)]}`}>
                <span>{BUCKETS[bucket(t)]}</span>
                {t.done && (
                  <button className="toggle-pill" onClick={() => d.setShowDone(false)} title="z">
                    hide
                  </button>
                )}
              </div>
            )}
            <div
              data-nav
              className={`doit-row ${st} ${t.id === d.key ? "selected" : ""}`}
              onClick={() => d.pick(t.id)}
            >
              {t.depth > 0 && <span style={{ width: t.depth * 14 }} />}
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
                <TodoText t={t} q={d.q} d={d} />
                {snip && (
                  <div className="doit-snip">
                    <Hl text={snip} q={d.q} />
                  </div>
                )}
                {(due.label || showWt || showFeat || t.twin) && (
                  <div className="doit-chips">
                    {due.label && <span className={`tok ${due.cls}`}>{due.label}</span>}
                    {showWt && <ScopeTok f="worktree" v={t.worktree} d={d} />}
                    {showFeat && <ScopeTok f="feature" v={t.feature} d={d} />}
                    {t.twin && (
                      <span className="tok tok-scope" title={`twin in ${t.twin.list}`}>
                        ↔ {t.twin.list}
                      </span>
                    )}
                  </div>
                )}
              </div>
            </div>
          </Fragment>
        );
      })}
      {l.doneHidden > 0 && (
        <div className="doit-section doit-section-row done">
          <span>done ({l.doneHidden})</span>
          <button className="toggle-pill" onClick={() => d.setShowDone(true)} title="z">
            show
          </button>
        </div>
      )}
    </>
  );
}

const EMPTY_EDIT: main.DoitEdit = {
  text: "",
  description: "",
  priority: "",
  due: "",
  type: "",
  deps: "",
  claude: false,
  sequence: 0,
  worktree: "",
  feature: "",
};

export function DoitDetail({ d }: { d: Doit }) {
  const t = d.todo;
  const [form, setForm] = useState<main.DoitEdit>(EMPTY_EDIT);
  useEffect(() => {
    if (d.editing && t) {
      const p = parseText(t.text);
      setForm({
        text: p.body,
        description: t.description,
        priority: t.priorities || "",
        due: t.due_date || "",
        type: p.type,
        deps: p.deps,
        claude: p.claude,
        sequence: t.done || t.in_progress ? 0 : t.sequence || 0,
        worktree: t.worktree || "",
        feature: t.feature || "",
      });
    }
  }, [d.editing]);

  if (d.note || d.creatingNote) return <NoteDetail key={d.key} d={d} n={d.note} />;
  if (!t || !d.list) return <div className="detail-empty">{d.list ? "pick an item" : "pick a list"}</div>;

  const st = status(t);
  const p = parseText(t.text);
  const due = dueInfo(t.due_date);
  const save = () => d.save(t, form);
  const field = (k: keyof main.DoitEdit) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setForm({ ...form, [k]: e.target.value });
  // deps are rank numbers on this list; done items are not offered, a dep on one is stale
  const deps = splitDeps(form.deps);
  const depOptions = d.list.all
    .map((o) => ({ o, p: parseText(o.text) }))
    .filter(({ o, p: op }) => o.id !== t.id && !o.done && op.rank && !deps.includes(op.rank));
  return (
    <>
      <header className="detail-head doit-head">
        <h1>
          <TodoText t={t} q={d.q} d={d} big />
        </h1>
        <div className="meta doit-meta">
          <span className={`tok status-${st}`}>{STATUS_LABEL[st]}</span>
          {t.priorities && <span className={`tok prio-${t.priorities}`}>{t.priorities}</span>}
          {due.label && (
            <span className={`tok ${due.cls}`}>
              {t.due_date} · {due.label}
            </span>
          )}
          {t.sequence > 0 && !t.done && !t.in_progress && <span className="tok">seq {t.sequence}</span>}
          {t.worktree && <ScopeTok f="worktree" v={t.worktree} d={d} />}
          {t.feature && <ScopeTok f="feature" v={t.feature} d={d} />}
          <span className="doit-dim">{d.list.name}</span>
          {t.created_at > 0 && <span className="doit-dim">created {when(t.created_at)}</span>}
          {t.done && t.completed_at > 0 && <span className="doit-dim">completed {when(t.completed_at)}</span>}
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
          <select value="" onChange={(e) => e.target.value && d.move(t, e.target.value)} title="move to another list">
            <option value="">move to…</option>
            {d.names
              .filter((n) => n !== d.list!.name)
              .map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
          </select>
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
            text{p.rank ? ` (rank #${p.rank} stays)` : ""}
            <input type="text" autoFocus value={form.text} onChange={field("text")} />
          </label>
          <div className="doit-edit-row">
            <label className="doit-edit-fixed">
              burn-down
              <button
                type="button"
                className={`toggle-pill doit-claude ${form.claude ? "on" : ""}`}
                onClick={() => setForm({ ...form, claude: !form.claude })}
                title="the claude: marker: /burn works this item"
              >
                {form.claude ? "claude: on" : "claude: off"}
              </button>
            </label>
            <label>
              type
              <input type="text" value={form.type} onChange={field("type")} placeholder="bug, chore, gate…" list="doit-types" />
            </label>
            <label>
              priority
              <select value={form.priority} onChange={field("priority")}>
                <option value="">none</option>
                {PRIOS.map((p) => (
                  <option key={p} value={p}>
                    {p}
                  </option>
                ))}
              </select>
            </label>
            <label className="doit-edit-fixed">
              sequence
              <input
                type="number"
                min={0}
                value={form.sequence}
                onChange={(e) => setForm({ ...form, sequence: Number(e.target.value) || 0 })}
              />
            </label>
          </div>
          <div className="doit-edit-row">
            <label className="doit-edit-fixed">
              due
              <input type="date" value={form.due} onChange={field("due")} />
            </label>
            <label>
              worktree
              <input type="text" value={form.worktree} onChange={field("worktree")} list="doit-worktrees" />
            </label>
            <label>
              feature
              <input type="text" value={form.feature} onChange={field("feature")} list="doit-features" />
            </label>
          </div>
          <label>
            dep on (items of {d.list.name})
            <div className="doit-deps">
              {deps.map((r) => {
                const dep = d.list!.all.find((o) => parseText(o.text).rank === r);
                return (
                  <span key={r} className="tok tok-dep">
                    #{r} {dep ? parseText(dep.text).body.slice(0, 40) : "(not on this list)"}
                    <button
                      type="button"
                      onClick={() => setForm({ ...form, deps: deps.filter((x) => x !== r).join(", ") })}
                      title="remove"
                    >
                      ×
                    </button>
                  </span>
                );
              })}
              <select
                value=""
                onChange={(e) => e.target.value && setForm({ ...form, deps: [...deps, e.target.value].join(", ") })}
              >
                <option value="">+ add dep…</option>
                {depOptions.map(({ o, p: op }) => (
                  <option key={o.id} value={op.rank}>
                    #{op.rank} {op.body.slice(0, 60)}
                  </option>
                ))}
              </select>
            </div>
          </label>
          <datalist id="doit-types">
            {d.facets.type.map((v) => (
              <option key={v} value={v} />
            ))}
          </datalist>
          <datalist id="doit-worktrees">
            {d.facets.worktree.map((v) => (
              <option key={v} value={v} />
            ))}
          </datalist>
          <datalist id="doit-features">
            {d.facets.feature.map((v) => (
              <option key={v} value={v} />
            ))}
          </datalist>
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
      <KeyHint />
    </>
  );
}

function NoteDetail({ d, n }: { d: Doit; n?: main.DoitNote }) {
  const editing = d.editing || !n;
  const [title, setTitle] = useState(n?.title || "");
  const [body, setBody] = useState(n?.body || "");
  useEffect(() => {
    if (editing) {
      setTitle(n?.title || "");
      setBody(n?.body || "");
    }
  }, [editing]);
  const save = () => d.saveNote(n, title, body);
  const cancel = () => (n ? d.setEditing(false) : d.pick(""));
  const armed = n && d.armed === `note:${n.id}`;

  return (
    <>
      <header className="detail-head doit-head">
        <h1>{n ? <Hl text={n.title || "untitled"} q={d.q} /> : "new note"}</h1>
        <div className="meta doit-meta">
          <span className="tok">list note</span>
          <span className="doit-dim">{d.list?.name}</span>
          {n && n.created_at > 0 && <span className="doit-dim">created {when(n.created_at)}</span>}
          {n && n.updated_at > 0 && <span className="doit-dim">updated {when(n.updated_at)}</span>}
          {n && (
            <button className="toggle-pill" onClick={() => ClipboardSetText(n.id)} title={n.id}>
              copy id
            </button>
          )}
        </div>
      </header>
      {editing ? (
        <div
          className="doit-edit"
          onKeyDown={(e) => {
            if (e.key === "Enter" && e.metaKey) save();
            if (e.key === "Escape") cancel();
          }}
        >
          <label>
            title
            <input type="text" autoFocus value={title} onChange={(e) => setTitle(e.target.value)} />
          </label>
          <label>
            body
            <textarea rows={20} value={body} onChange={(e) => setBody(e.target.value)} />
          </label>
          <div className="doit-actions">
            <button className="active" onClick={save} title="cmd+enter">
              {n ? "save" : "create"}
            </button>
            <button onClick={cancel} title="esc">
              cancel
            </button>
          </div>
        </div>
      ) : (
        <>
          <div className="doit-actions">
            <button onClick={() => d.setEditing(true)} title="e">
              edit
            </button>
            <button className={armed ? "doit-danger" : ""} onClick={() => n && d.removeNote(n)} title="d, twice">
              {armed ? "confirm delete" : "delete"}
            </button>
          </div>
          <pre className="doit-notes">{n?.body ? <Hl text={n.body} q={d.q} /> : "empty"}</pre>
        </>
      )}
      <KeyHint />
    </>
  );
}

function KeyHint() {
  return (
    <div className="filter-hint doit-keys">
      <span className="kbd">j</span>/<span className="kbd">k</span> item · <span className="kbd">J</span>/
      <span className="kbd">K</span> list · <span className="kbd">space</span> cycle ·{" "}
      <span className="kbd">x</span> complete · <span className="kbd">e</span> edit ·{" "}
      <span className="kbd">d</span> delete · <span className="kbd">n</span> new note ·{" "}
      <span className="kbd">N</span> new list · <span className="kbd">o</span> sort lists ·{" "}
      <span className="kbd">z</span> show/hide done
    </div>
  );
}
