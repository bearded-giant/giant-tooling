import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import "./App.css";
import {
  FacetCounts,
  GetArtifactBody,
  ListArtifacts,
  ListSessions,
  ReadFile,
  SearchFTS,
  SearchHybrid,
} from "../wailsjs/go/main/App";
import { artifacts, main, search } from "../wailsjs/go/models";

type Tab = "artifacts" | "sessions";

type Selection =
  | { kind: "artifact"; id: string }
  | { kind: "session"; path: string }
  | null;

const SORT_OPTIONS: { value: string; label: string }[] = [
  { value: "", label: "relevance" },
  { value: "updated", label: "updated" },
  { value: "created", label: "created" },
  { value: "access", label: "most accessed" },
  { value: "type", label: "type" },
];

function App() {
  const [tab, setTab] = useState<Tab>("artifacts");
  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [sortBy, setSortBy] = useState<string>("");

  const [selType, setSelType] = useState<Set<string>>(new Set());
  const [selStatus, setSelStatus] = useState<Set<string>>(new Set());
  const [selLifecycle, setSelLifecycle] = useState<Set<string>>(new Set());

  const [facets, setFacets] = useState<main.FacetCountsResult | null>(null);
  const [artifactRows, setArtifactRows] = useState<artifacts.Artifact[]>([]);
  const [hybridRows, setHybridRows] = useState<search.HybridResult[]>([]);
  const [sessionHits, setSessionHits] = useState<search.Hit[]>([]);

  const [selection, setSelection] = useState<Selection>(null);
  const [detailArt, setDetailArt] = useState<artifacts.Artifact | null>(null);
  const [detailBody, setDetailBody] = useState<string>("");
  const [sessionLines, setSessionLines] = useState<SessionTurn[] | null>(null);

  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    FacetCounts()
      .then((f) => setFacets(f))
      .catch((e) => setErr(String(e)));
  }, []);

  // ordered ids list for j/k nav
  const visibleRowIDs: string[] = useMemo(() => {
    if (tab === "artifacts") {
      return hybridRows.length
        ? hybridRows.map((h) => h.artifact.id)
        : artifactRows.map((a) => a.id);
    }
    return sessionHits.map((h) => h.filepath);
  }, [tab, hybridRows, artifactRows, sessionHits]);

  // keyboard nav: j/k move row, esc clear, / focus search
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName;
      const inField = tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
      if (e.key === "/" && !inField) {
        e.preventDefault();
        searchRef.current?.focus();
        return;
      }
      if (e.key === "Escape") {
        if (inField) {
          (e.target as HTMLElement).blur();
          return;
        }
        setSelection(null);
        return;
      }
      if (inField) return;
      if (e.key !== "j" && e.key !== "k") return;
      if (visibleRowIDs.length === 0) return;
      const curID =
        selection?.kind === "artifact"
          ? selection.id
          : selection?.kind === "session"
            ? selection.path
            : null;
      const i = curID ? visibleRowIDs.indexOf(curID) : -1;
      const next =
        e.key === "j"
          ? Math.min(visibleRowIDs.length - 1, i + 1)
          : Math.max(0, i - 1);
      const id = visibleRowIDs[next];
      setSelection(
        tab === "artifacts"
          ? { kind: "artifact", id }
          : { kind: "session", path: id },
      );
      e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [visibleRowIDs, selection, tab]);

  useEffect(() => {
    const t = setTimeout(() => setDebouncedQuery(query), 200);
    return () => clearTimeout(t);
  }, [query]);

  const filter = useMemo<artifacts.ListFilter>(
    () => ({
      Type: [...selType],
      Status: [...selStatus],
      Lifecycle: [...selLifecycle],
      Scope: "",
      Repo: "",
      Branch: "",
      Feature: "",
      Domain: "",
    }),
    [selType, selStatus, selLifecycle],
  );

  // refetch results when inputs change
  useEffect(() => {
    setErr(null);
    setLoading(true);
    const run = async () => {
      try {
        if (tab === "artifacts") {
          if (debouncedQuery.trim()) {
            const r = await SearchHybrid(debouncedQuery, filter, 100);
            setHybridRows(r || []);
            setArtifactRows([]);
          } else {
            const r = await ListArtifacts(filter, sortBy, 200);
            setArtifactRows(r || []);
            setHybridRows([]);
          }
        } else {
          let hits: search.Hit[];
          if (debouncedQuery.trim()) {
            const params: search.Params = {
              Query: debouncedQuery,
              Project: "",
              DirType: "",
              SourceType: "session",
              Feature: "",
              Latest: false,
              LiveOnly: false,
              ArchiveOnly: true,
              Since: "",
              Until: "",
              Limit: 100,
              IncludeFull: false,
            };
            hits = (await SearchFTS(params)) || [];
          } else {
            hits = (await ListSessions("", 100)) || [];
          }
          setSessionHits(hits);
        }
      } catch (e) {
        setErr(String(e));
      } finally {
        setLoading(false);
      }
    };
    run();
  }, [tab, debouncedQuery, sortBy, filter]);

  // load detail when selection changes
  useEffect(() => {
    if (!selection) {
      setDetailArt(null);
      setDetailBody("");
      setSessionLines(null);
      return;
    }
    if (selection.kind === "artifact") {
      const row =
        artifactRows.find((a) => a.id === selection.id) ||
        hybridRows.find((h) => h.artifact?.id === selection.id)?.artifact ||
        null;
      setDetailArt(row);
      setSessionLines(null);
      GetArtifactBody(selection.id)
        .then(setDetailBody)
        .catch((e: unknown) => setDetailBody(`# error\n\n${String(e)}`));
    } else {
      setDetailArt(null);
      setDetailBody("");
      ReadFile(selection.path)
        .then((raw: string) => setSessionLines(parseJSONL(raw)))
        .catch((e: unknown) => {
          setSessionLines(null);
          setErr(String(e));
        });
    }
  }, [selection, artifactRows, hybridRows]);

  const toggleSet = useCallback(
    (s: Set<string>, v: string, setter: (n: Set<string>) => void) => {
      const next = new Set(s);
      if (next.has(v)) next.delete(v);
      else next.add(v);
      setter(next);
    },
    [],
  );

  const clearFacets = () => {
    setSelType(new Set());
    setSelStatus(new Set());
    setSelLifecycle(new Set());
  };

  const totalRows =
    tab === "artifacts"
      ? hybridRows.length || artifactRows.length
      : sessionHits.length;

  return (
    <div id="App" className="app-grid">
      <div className="topbar">
        <div className="brand">giantmem</div>
        <div className="tabs">
          <button
            className={tab === "artifacts" ? "active" : ""}
            onClick={() => {
              setTab("artifacts");
              setSelection(null);
            }}
          >
            artifacts
          </button>
          <button
            className={tab === "sessions" ? "active" : ""}
            onClick={() => {
              setTab("sessions");
              setSelection(null);
            }}
          >
            sessions
          </button>
        </div>
        <div className="search-wrap">
          <input
            ref={searchRef}
            type="search"
            placeholder={
              tab === "artifacts"
                ? "hybrid search artifacts… (/ to focus)"
                : "FTS search sessions… (/ to focus)"
            }
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        {tab === "artifacts" && (
          <select value={sortBy} onChange={(e) => setSortBy(e.target.value)}>
            {SORT_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        )}
      </div>

      <div className="filter-chips">
        {tab === "artifacts" && (selType.size + selStatus.size + selLifecycle.size) > 0 ? (
          <>
            {[...selType].map((v) => (
              <span
                key={`t-${v}`}
                className="filter-chip"
                onClick={() =>
                  toggleSet(selType, v, setSelType)
                }
              >
                type: {v} <span className="x">×</span>
              </span>
            ))}
            {[...selStatus].map((v) => (
              <span
                key={`s-${v}`}
                className="filter-chip"
                onClick={() =>
                  toggleSet(selStatus, v, setSelStatus)
                }
              >
                status: {v} <span className="x">×</span>
              </span>
            ))}
            {[...selLifecycle].map((v) => (
              <span
                key={`l-${v}`}
                className="filter-chip"
                onClick={() =>
                  toggleSet(selLifecycle, v, setSelLifecycle)
                }
              >
                lifecycle: {v} <span className="x">×</span>
              </span>
            ))}
            <span
              className="filter-chip"
              onClick={clearFacets}
              style={{ color: "var(--fg-muted)" }}
            >
              clear all
            </span>
          </>
        ) : (
          <span className="filter-hint">
            <span className="kbd">/</span> focus search ·{" "}
            <span className="kbd">j</span>/<span className="kbd">k</span> nav ·{" "}
            <span className="kbd">esc</span> clear
          </span>
        )}
      </div>

      <aside className="sidebar">
        {tab === "artifacts" && facets && (
          <>
            <FacetGroup
              title="type"
              counts={facets.byType || {}}
              selected={selType}
              onToggle={(v) => toggleSet(selType, v, setSelType)}
            />
            <FacetGroup
              title="status"
              counts={facets.byStatus || {}}
              selected={selStatus}
              onToggle={(v) => toggleSet(selStatus, v, setSelStatus)}
            />
            <FacetGroup
              title="lifecycle"
              counts={facets.byLifecycle || {}}
              selected={selLifecycle}
              onToggle={(v) => toggleSet(selLifecycle, v, setSelLifecycle)}
            />
            {(selType.size || selStatus.size || selLifecycle.size) > 0 && (
              <button className="facet-clear" onClick={clearFacets}>
                clear filters
              </button>
            )}
          </>
        )}
        {tab === "sessions" && (
          <div style={{ color: "var(--fg-muted)" }}>
            sessions filters TBD — type to search.
          </div>
        )}
      </aside>

      <section className="results">
        {tab === "artifacts" && hybridRows.length > 0 &&
          hybridRows.map((h) => (
            <HybridRow
              key={h.artifact.id}
              row={h}
              selected={
                selection?.kind === "artifact" &&
                selection.id === h.artifact.id
              }
              onClick={() =>
                setSelection({ kind: "artifact", id: h.artifact.id })
              }
            />
          ))}
        {tab === "artifacts" && hybridRows.length === 0 &&
          artifactRows.map((a) => (
            <ArtifactRow
              key={a.id}
              row={a}
              selected={
                selection?.kind === "artifact" && selection.id === a.id
              }
              onClick={() => setSelection({ kind: "artifact", id: a.id })}
            />
          ))}
        {tab === "sessions" &&
          sessionHits.map((h) => (
            <SessionRow
              key={h.filepath + h.timestamp}
              hit={h}
              selected={
                selection?.kind === "session" && selection.path === h.filepath
              }
              onClick={() =>
                setSelection({ kind: "session", path: h.filepath })
              }
            />
          ))}
      </section>

      <section className="detail">
        {!selection && (
          <div className="detail-empty">
            {totalRows > 0
              ? "pick a row to read the body"
              : loading
                ? "loading…"
                : "no results"}
          </div>
        )}
        {selection?.kind === "artifact" && detailArt && (
          <>
            <header className="detail-head">
              <h1 style={{ marginBottom: 0 }}>
                {detailArt.feature || detailArt.name || detailArt.path}
              </h1>
              <div className="meta">
                <span className={`chip type-${detailArt.type}`}>
                  {detailArt.type}
                </span>
                {detailArt.status && <span className="chip">{detailArt.status}</span>}
                {detailArt.lifecycle && (
                  <span className="chip">{detailArt.lifecycle}</span>
                )}
                {detailArt.repo && <span>repo: {detailArt.repo}</span>}
                {detailArt.branch && <span>branch: {detailArt.branch}</span>}
                {detailArt.updated && (
                  <span>updated: {formatTime(detailArt.updated)}</span>
                )}
                <span style={{ fontFamily: "ui-monospace", opacity: 0.7 }}>
                  {detailArt.path}
                </span>
              </div>
            </header>
            <ReactMarkdown remarkPlugins={[remarkGfm]}>
              {detailBody}
            </ReactMarkdown>
          </>
        )}
        {selection?.kind === "session" && (
          <>
            <header className="detail-head">
              <h2 style={{ marginTop: 0, marginBottom: 0 }}>
                {selection.path.split("/").pop()}
              </h2>
              <div className="meta">
                <span style={{ fontFamily: "ui-monospace", opacity: 0.7 }}>
                  {selection.path}
                </span>
              </div>
            </header>
            {sessionLines === null && <div>loading transcript…</div>}
            {sessionLines &&
              sessionLines.map((t, i) => (
                <SessionTurnView key={i} turn={t} />
              ))}
          </>
        )}
      </section>

      <div className="status-bar">
        <span>
          {totalRows} {tab}
          {loading && " · loading…"}
        </span>
        {err && <span className="err">err: {err}</span>}
        {!err && facets && (
          <span className="ok">
            facets: {Object.values(facets.byType || {}).reduce((a, b) => a + b, 0)} artifacts
          </span>
        )}
      </div>
    </div>
  );
}

function FacetGroup({
  title,
  counts,
  selected,
  onToggle,
}: {
  title: string;
  counts: Record<string, number>;
  selected: Set<string>;
  onToggle: (v: string) => void;
}) {
  const sorted = Object.entries(counts).sort((a, b) => b[1] - a[1]);
  return (
    <div className="facet-group">
      <h4>{title}</h4>
      {sorted.map(([v, n]) => (
        <div
          key={v}
          className={`facet-row ${selected.has(v) ? "selected" : ""}`}
          onClick={() => onToggle(v)}
        >
          <span>{v || "(blank)"}</span>
          <span className="count">{n}</span>
        </div>
      ))}
    </div>
  );
}

function ArtifactRow({
  row,
  selected,
  onClick,
}: {
  row: artifacts.Artifact;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <div
      className={`result-row ${selected ? "selected" : ""}`}
      onClick={onClick}
    >
      <div className="row-head">
        <span className={`chip type-${row.type}`}>{row.type}</span>
        <span className="row-title">
          {row.feature || row.name || row.path.split("/").pop()}
        </span>
        {row.status && <span className="row-meta">{row.status}</span>}
      </div>
      <div className="row-path">{row.path}</div>
      <div className="row-meta">
        {row.repo} · {formatTime(row.updated)}
        {row.access_count ? ` · ${row.access_count} access` : ""}
        {row.has_vec ? " · vec" : ""}
      </div>
    </div>
  );
}

function HybridRow({
  row,
  selected,
  onClick,
}: {
  row: search.HybridResult;
  selected: boolean;
  onClick: () => void;
}) {
  const a = row.artifact;
  return (
    <div
      className={`result-row ${selected ? "selected" : ""}`}
      onClick={onClick}
    >
      <div className="row-head">
        <span className={`chip type-${a.type}`}>{a.type}</span>
        <span className="row-title">
          {a.feature || a.name || a.path.split("/").pop()}
        </span>
        <span className="chip score">{row.score.toFixed(3)}</span>
      </div>
      <div className="row-path">{a.path}</div>
      <div className="row-meta">
        fts {row.fts_score.toFixed(2)} · vec {row.vector_score.toFixed(2)} · rec{" "}
        {row.recency_score.toFixed(2)} · acc {row.access_score.toFixed(2)}
      </div>
    </div>
  );
}

function SessionRow({
  hit,
  selected,
  onClick,
}: {
  hit: search.Hit;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <div
      className={`result-row ${selected ? "selected" : ""}`}
      onClick={onClick}
    >
      <div className="row-head">
        <span className="chip">{hit.source_type || "session"}</span>
        <span className="row-title">
          {hit.topic || hit.filename || "(session)"}
        </span>
        {hit.timestamp && (
          <span className="row-meta">{formatTime(hit.timestamp)}</span>
        )}
      </div>
      <div className="row-meta">
        {hit.project && <strong>{hit.project}</strong>}
        {hit.cwd && <> · {hit.cwd}</>}
        {hit.session_id && (
          <> · <span style={{ fontFamily: "ui-monospace" }}>{hit.session_id.slice(0, 8)}</span></>
        )}
      </div>
      <div className="row-path">{hit.filepath}</div>
      {hit.snippet && (
        <div className="row-meta" style={{ marginTop: 4 }}>
          {hit.snippet}
        </div>
      )}
    </div>
  );
}

type ContentBlock =
  | { type: "text"; text: string }
  | { type: "tool_use"; name: string; input: any; id?: string }
  | { type: "tool_result"; content: any; tool_use_id?: string; is_error?: boolean }
  | { type: "thinking"; thinking: string }
  | { type: "unknown"; raw: any };

type SessionTurn = {
  role: string;
  timestamp?: string;
  blocks: ContentBlock[];
  raw: any;
};

function parseJSONL(raw: string): SessionTurn[] {
  const out: SessionTurn[] = [];
  for (const line of raw.split("\n")) {
    if (!line.trim()) continue;
    try {
      const obj = JSON.parse(line);
      const role: string =
        obj.type ||
        obj.role ||
        (obj.message && obj.message.role) ||
        "event";
      const blocks = extractBlocks(obj);
      out.push({
        role,
        timestamp: obj.timestamp || obj.ts || undefined,
        blocks,
        raw: obj,
      });
    } catch {
      out.push({
        role: "raw",
        blocks: [{ type: "text", text: line }],
        raw: null,
      });
    }
  }
  return out;
}

function extractBlocks(obj: any): ContentBlock[] {
  const content = obj?.message?.content ?? obj?.content;
  if (content == null) {
    if (typeof obj?.text === "string") return [{ type: "text", text: obj.text }];
    return [{ type: "unknown", raw: obj }];
  }
  if (typeof content === "string") return [{ type: "text", text: content }];
  if (!Array.isArray(content)) return [{ type: "unknown", raw: content }];
  return content.map((c: any): ContentBlock => {
    if (typeof c === "string") return { type: "text", text: c };
    switch (c.type) {
      case "text":
        return { type: "text", text: String(c.text ?? "") };
      case "tool_use":
        return {
          type: "tool_use",
          name: String(c.name ?? "tool"),
          input: c.input,
          id: c.id,
        };
      case "tool_result":
        return {
          type: "tool_result",
          content: c.content,
          tool_use_id: c.tool_use_id,
          is_error: !!c.is_error,
        };
      case "thinking":
        return { type: "thinking", thinking: String(c.thinking ?? "") };
      default:
        return { type: "unknown", raw: c };
    }
  });
}

function SessionTurnView({ turn }: { turn: SessionTurn }) {
  return (
    <div className={`session-turn ${turn.role}`}>
      <div className="who">
        {turn.role}
        {turn.timestamp && (
          <span
            style={{
              marginLeft: 8,
              color: "var(--fg-muted)",
              fontWeight: 400,
              fontSize: 11,
            }}
          >
            {formatTime(turn.timestamp)}
          </span>
        )}
      </div>
      {turn.blocks.map((b, i) => (
        <BlockView key={i} block={b} />
      ))}
    </div>
  );
}

function BlockView({ block }: { block: ContentBlock }) {
  if (block.type === "text") {
    return (
      <div className="turn-text">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{block.text}</ReactMarkdown>
      </div>
    );
  }
  if (block.type === "tool_use") {
    return (
      <details className="tool-block">
        <summary>
          <span className="chip tool">tool_use</span>
          <strong>{block.name}</strong>
        </summary>
        <pre style={{ whiteSpace: "pre-wrap" }}>
          {JSON.stringify(block.input, null, 2)}
        </pre>
      </details>
    );
  }
  if (block.type === "tool_result") {
    const text =
      typeof block.content === "string"
        ? block.content
        : Array.isArray(block.content)
          ? block.content
              .map((c: any) =>
                typeof c === "string" ? c : c.text || JSON.stringify(c),
              )
              .join("\n")
          : JSON.stringify(block.content, null, 2);
    return (
      <details className="tool-block">
        <summary>
          <span className={`chip tool ${block.is_error ? "err" : ""}`}>
            tool_result
          </span>
          {block.is_error && <span style={{ color: "var(--danger)" }}> error</span>}
        </summary>
        <pre style={{ whiteSpace: "pre-wrap" }}>{text}</pre>
      </details>
    );
  }
  if (block.type === "thinking") {
    return (
      <details className="tool-block">
        <summary>
          <span className="chip thinking">thinking</span>
        </summary>
        <pre style={{ whiteSpace: "pre-wrap", opacity: 0.8 }}>
          {block.thinking}
        </pre>
      </details>
    );
  }
  return (
    <pre style={{ whiteSpace: "pre-wrap", fontSize: 11, opacity: 0.7 }}>
      {JSON.stringify(block, null, 2)}
    </pre>
  );
}

function formatTime(s?: string): string {
  if (!s) return "";
  const t = new Date(s);
  if (Number.isNaN(t.getTime())) return s;
  const diff = (Date.now() - t.getTime()) / 1000;
  if (diff < 60) return `${Math.floor(diff)}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  if (diff < 86400 * 7) return `${Math.floor(diff / 86400)}d ago`;
  return t.toISOString().slice(0, 10);
}

export default App;
