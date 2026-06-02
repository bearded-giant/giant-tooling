import { useCallback, useEffect, useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import "./App.css";
import {
  FacetCounts,
  GetArtifactBody,
  ListArtifacts,
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

  useEffect(() => {
    FacetCounts()
      .then((f) => setFacets(f))
      .catch((e) => setErr(String(e)));
  }, []);

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
          const hits = await SearchFTS(params);
          setSessionHits(hits || []);
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
            type="search"
            placeholder={
              tab === "artifacts"
                ? "hybrid search artifacts… (empty = list all)"
                : "FTS search sessions…"
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
                {detailArt.updated && <span>updated: {detailArt.updated}</span>}
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
        {row.repo} · {row.updated}
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
        {hit.timestamp && <span className="row-meta">{hit.timestamp}</span>}
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

type SessionTurn = {
  role: string;
  text: string;
  raw: any;
};

function parseJSONL(raw: string): SessionTurn[] {
  const out: SessionTurn[] = [];
  for (const line of raw.split("\n")) {
    if (!line.trim()) continue;
    try {
      const obj = JSON.parse(line);
      const role =
        obj.type ||
        obj.role ||
        (obj.message && obj.message.role) ||
        "event";
      const text = extractText(obj);
      out.push({ role, text, raw: obj });
    } catch {
      out.push({ role: "raw", text: line, raw: null });
    }
  }
  return out;
}

function extractText(obj: any): string {
  if (typeof obj === "string") return obj;
  if (obj?.message?.content) {
    if (typeof obj.message.content === "string") return obj.message.content;
    if (Array.isArray(obj.message.content)) {
      return obj.message.content
        .map((c: any) =>
          typeof c === "string" ? c : c.text || JSON.stringify(c, null, 2),
        )
        .join("\n");
    }
  }
  if (obj?.content) {
    if (typeof obj.content === "string") return obj.content;
    if (Array.isArray(obj.content)) {
      return obj.content
        .map((c: any) =>
          typeof c === "string" ? c : c.text || JSON.stringify(c, null, 2),
        )
        .join("\n");
    }
  }
  if (obj?.text) return String(obj.text);
  return JSON.stringify(obj, null, 2);
}

function SessionTurnView({ turn }: { turn: SessionTurn }) {
  return (
    <div className={`session-turn ${turn.role}`}>
      <div className="who">{turn.role}</div>
      <pre style={{ whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
        {turn.text}
      </pre>
    </div>
  );
}

export default App;
