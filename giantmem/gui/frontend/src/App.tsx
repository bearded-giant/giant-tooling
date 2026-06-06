import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import "highlight.js/styles/github-dark.css";
import {
  ClipboardSetText,
  WindowGetPosition,
  WindowGetSize,
  WindowSetPosition,
  WindowSetSize,
} from "../wailsjs/runtime/runtime";
import "./App.css";
import {
  ActivityCounts,
  FacetCounts,
  FeaturesByRepo,
  GetArtifactBody,
  ListArtifacts,
  ListSessions,
  LiveMtime,
  ProjectHeatmap,
  ProjectSparkline,
  ReadFile,
  RecentFiles,
  RecentRepos,
  SearchFTS,
  SearchHybrid,
  SearchToolUses,
  SessionFacets,
} from "../wailsjs/go/main/App";
import { artifacts, main, search } from "../wailsjs/go/models";

type Tab = "artifacts" | "sessions" | "tools" | "activity";

const TOOL_NAMES = [
  "Bash",
  "Read",
  "Write",
  "Edit",
  "Glob",
  "Grep",
  "WebFetch",
  "WebSearch",
  "Skill",
  "Agent",
  "TodoWrite",
];

type Selection =
  | { kind: "artifact"; id: string }
  | { kind: "session"; path: string }
  | null;

const SORT_OPTIONS: { value: string; label: string }[] = [
  { value: "updated", label: "updated (newest)" },
  { value: "created", label: "created (newest)" },
  { value: "access", label: "most accessed" },
  { value: "type", label: "type" },
  { value: "", label: "repo / type (default)" },
];

function App() {
  const [tab, setTab] = useState<Tab>("activity");
  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [sortBy, setSortBy] = useState<string>("updated");

  const [selType, setSelType] = useState<Set<string>>(new Set());
  const [selStatus, setSelStatus] = useState<Set<string>>(new Set());
  const [selLifecycle, setSelLifecycle] = useState<Set<string>>(new Set());
  const [selFeature, setSelFeature] = useState<string>("");
  const [selRepo, setSelRepo] = useState<string>("");
  const [sidebarFilter, setSidebarFilter] = useState("");
  const [sidebarWidth, setSidebarWidth] = useState<number>(() => {
    const n = Number(localStorage.getItem("gm.sidebarWidth"));
    return Number.isFinite(n) && n >= 180 && n <= 600 ? n : 260;
  });
  useEffect(() => {
    localStorage.setItem("gm.sidebarWidth", String(sidebarWidth));
  }, [sidebarWidth]);

  const [turnOrder, setTurnOrder] = useState<"asc" | "desc">(() => {
    const v = localStorage.getItem("gm.turnOrder");
    return v === "asc" ? "asc" : "desc";
  });
  useEffect(() => {
    localStorage.setItem("gm.turnOrder", turnOrder);
  }, [turnOrder]);

  // restore window size/position once at mount, then persist on resize.
  useEffect(() => {
    (async () => {
      const saved = localStorage.getItem("gm.win");
      if (!saved) return;
      try {
        const { w, h, x, y } = JSON.parse(saved);
        if (typeof w === "number" && typeof h === "number" && w > 400 && h > 300) {
          WindowSetSize(w, h);
        }
        if (typeof x === "number" && typeof y === "number") {
          WindowSetPosition(x, y);
        }
      } catch {
        // ignore corrupt entry
      }
    })();
    const save = debounce(async () => {
      try {
        const size = await WindowGetSize();
        const pos = await WindowGetPosition();
        localStorage.setItem(
          "gm.win",
          JSON.stringify({ w: size.w, h: size.h, x: pos.x, y: pos.y }),
        );
      } catch {
        // ignore: wails not ready or window closed
      }
    }, 400);
    window.addEventListener("resize", save);
    // catch position changes too — there's no native window-move event in
    // wails' webview, so we ride on resize plus a slow heartbeat.
    const tick = window.setInterval(save, 4000);
    return () => {
      window.removeEventListener("resize", save);
      window.clearInterval(tick);
    };
  }, []);
  const [collapsed, setCollapsed] = useState<Set<string>>(
    new Set(["feature", "repo", "topic", "dir_type"]),
  );

  const [facets, setFacets] = useState<main.FacetCountsResult | null>(null);
  const [featuresByRepo, setFeaturesByRepo] = useState<main.FeatureRow[]>([]);
  const [sessionFacets, setSessionFacets] =
    useState<main.SessionFacetCounts | null>(null);
  const [toolHits, setToolHits] = useState<main.ToolUseHit[]>([]);
  const [toolNameFilter, setToolNameFilter] = useState<string>("Bash");
  const [toolUseFTSPre, setToolUseFTSPre] = useState(true);
  const [toolSelected, setToolSelected] = useState<main.ToolUseHit | null>(null);
  const [selSessionProject, setSelSessionProject] = useState("");
  const [selSessionDirType, setSelSessionDirType] = useState("");
  const [selSessionTopic, setSelSessionTopic] = useState("");
  const [selSessionDate, setSelSessionDate] = useState("");
  const [artifactRows, setArtifactRows] = useState<artifacts.Artifact[]>([]);
  const [hybridRows, setHybridRows] = useState<search.HybridResult[]>([]);
  const [sessionHits, setSessionHits] = useState<search.Hit[]>([]);

  const [selection, setSelection] = useState<Selection>(null);
  const [detailArt, setDetailArt] = useState<artifacts.Artifact | null>(null);
  const [detailBody, setDetailBody] = useState<string>("");
  const [sessionLines, setSessionLines] = useState<SessionTurn[] | null>(null);
  const [turnFilter, setTurnFilter] = useState("");
  const [expandRev, setExpandRev] = useState(0);
  const [defaultExpanded, setDefaultExpanded] = useState(true);

  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);
  const [justRefreshed, setJustRefreshed] = useState(false);
  const [repoActivity, setRepoActivity] = useState<main.RepoActivity[]>([]);
  const [expandedWorktree, setExpandedWorktree] = useState<string | null>(null);
  const [expandedFiles, setExpandedFiles] = useState<main.FileActivity[]>([]);
  const [activityCounts, setActivityCounts] = useState<main.ActivityCounts | null>(null);
  const [activityFilter, setActivityFilter] = useState("");
  const [sparklines, setSparklines] = useState<Record<string, main.SparklinePoint[]>>({});
  const [heatmap, setHeatmap] = useState<main.HeatmapCell[]>([]);
  const searchRef = useRef<HTMLInputElement>(null);
  const gridRef = useRef<HTMLDivElement>(null);
  const topbarRef = useRef<HTMLDivElement>(null);
  const chipsRef = useRef<HTMLDivElement>(null);

  const refreshAll = useCallback(() => {
    setReloadKey((k) => k + 1);
    setJustRefreshed(true);
    window.setTimeout(() => setJustRefreshed(false), 600);
  }, []);

  // poll live.db mtime every 5s. when daemon reconciler or peer PostToolUse
  // hook writes, mtime changes → bump reloadKey so all queries re-run.
  // visual flash via justRefreshed reuses the manual-refresh treatment.
  useEffect(() => {
    let lastMtime = 0;
    const tick = async () => {
      try {
        const m = await LiveMtime();
        if (lastMtime === 0) {
          lastMtime = m;
          return;
        }
        if (m > lastMtime) {
          lastMtime = m;
          refreshAll();
        }
      } catch {}
    };
    const id = window.setInterval(tick, 5000);
    return () => window.clearInterval(id);
  }, [refreshAll]);

  useEffect(() => {
    FacetCounts()
      .then((f) => setFacets(f))
      .catch((e) => setErr(String(e)));
    FeaturesByRepo()
      .then((rows) => setFeaturesByRepo(rows || []))
      .catch((e) => setErr(String(e)));
    SessionFacets()
      .then((sf) => setSessionFacets(sf))
      .catch((e) => setErr(String(e)));
    RecentRepos(50)
      .then((rows) => setRepoActivity(rows || []))
      .catch((e) => setErr(String(e)));
    ActivityCounts()
      .then((c) => setActivityCounts(c))
      .catch((e) => setErr(String(e)));
    ProjectHeatmap(14, 10)
      .then((cells) => setHeatmap(cells || []))
      .catch((e) => setErr(String(e)));
  }, [reloadKey]);

  // sparklines: fan out per visible repo after RecentRepos lands. Cached in
  // a map keyed by worktreePath so re-renders don't refetch. Cleared on
  // reloadKey so daemon backfill picks up fresh numbers.
  useEffect(() => {
    if (tab !== "activity") return;
    if (!repoActivity.length) return;
    const wanted = repoActivity.slice(0, 30).map((r) => r.worktreePath);
    const missing = wanted.filter((wt) => !(wt in sparklines));
    if (!missing.length) return;
    Promise.all(
      missing.map((wt) =>
        ProjectSparkline(wt, 7)
          .then((pts) => [wt, pts] as [string, main.SparklinePoint[]])
          .catch(() => [wt, [] as main.SparklinePoint[]] as [string, main.SparklinePoint[]]),
      ),
    ).then((pairs) => {
      setSparklines((prev) => {
        const next = { ...prev };
        for (const [wt, pts] of pairs) next[wt] = pts;
        return next;
      });
    });
  }, [tab, repoActivity, sparklines]);

  // reset sparkline cache when live.db ticks so a fresh poll picks up new bars
  useEffect(() => {
    setSparklines({});
  }, [reloadKey]);

  // load files for the currently-expanded worktree; reset when collapsed or
  // when reloadKey bumps (auto-refresh from LiveMtime poll).
  useEffect(() => {
    if (!expandedWorktree) {
      setExpandedFiles([]);
      return;
    }
    RecentFiles(expandedWorktree, 100)
      .then((rows) => setExpandedFiles(rows || []))
      .catch((e) => setErr(String(e)));
  }, [expandedWorktree, reloadKey]);

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
      if (e.key === "r" && !e.metaKey && !e.ctrlKey) {
        e.preventDefault();
        refreshAll();
        return;
      }
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
  }, [visibleRowIDs, selection, tab, refreshAll]);

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
      Repo: selRepo,
      Branch: "",
      Feature: selFeature,
      Domain: "",
    }),
    [selType, selStatus, selLifecycle, selFeature, selRepo],
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
        } else if (tab === "tools") {
          const f: main.ToolUseFilter = {
            query: debouncedQuery,
            toolName: toolNameFilter === "any" ? "" : toolNameFilter,
            project: "",
            useFTSPre: toolUseFTSPre,
            limit: 200,
          };
          const hits = (await SearchToolUses(f)) || [];
          setToolHits(hits);
        } else {
          let hits: search.Hit[];
          const sessionFilter: main.SessionFilter = {
            project: selSessionProject,
            dirType: selSessionDirType,
            topic: selSessionTopic,
            dateBucket: selSessionDate,
          };
          if (debouncedQuery.trim()) {
            const params: search.Params = {
              Query: debouncedQuery,
              Project: selSessionProject,
              DirType: selSessionDirType,
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
            // client-side topic/date filter for FTS path since search.Run
            // doesn't know our buckets
            if (selSessionTopic) {
              hits = hits.filter((h) => h.topic === selSessionTopic);
            }
            if (selSessionDate) {
              hits = filterByDateBucket(hits, selSessionDate);
            }
          } else {
            hits = (await ListSessions(sessionFilter, 100)) || [];
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
  }, [
    tab,
    debouncedQuery,
    sortBy,
    filter,
    selSessionProject,
    selSessionDirType,
    selSessionTopic,
    selSessionDate,
    toolNameFilter,
    toolUseFTSPre,
    reloadKey,
  ]);

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
  }, [selection, artifactRows, hybridRows, reloadKey]);

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
    setSelFeature("");
    setSelRepo("");
  };

  const toggleCollapsed = useCallback((title: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(title)) next.delete(title);
      else next.add(title);
      return next;
    });
  }, []);

  const totalRows =
    tab === "artifacts"
      ? hybridRows.length || artifactRows.length
      : tab === "tools"
        ? toolHits.length
        : sessionHits.length;

  // measure the topbar + filter-chips stack so the sidebar resizer doesn't
  // start at the very top of the window and overlay the tab buttons. write
  // the combined height to a CSS var the resizer reads.
  useEffect(() => {
    const apply = () => {
      const top = topbarRef.current?.getBoundingClientRect().height ?? 0;
      const chips = chipsRef.current?.getBoundingClientRect().height ?? 0;
      const h = Math.ceil(top + chips);
      gridRef.current?.style.setProperty("--topbar-h", `${h}px`);
    };
    apply();
    const ro = new ResizeObserver(apply);
    if (topbarRef.current) ro.observe(topbarRef.current);
    if (chipsRef.current) ro.observe(chipsRef.current);
    return () => ro.disconnect();
  }, []);

  return (
    <div
      id="App"
      ref={gridRef}
      className="app-grid"
      style={{
        gridTemplateColumns: `${sidebarWidth}px 1fr 1fr`,
      }}
    >
      <div className="topbar" ref={topbarRef}>
        <div className="brand">giantmem</div>
        <div className="tabs">
          <button
            className={tab === "activity" ? "active" : ""}
            onClick={() => {
              setTab("activity");
              setSelection(null);
            }}
          >
            activity
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
            className={tab === "tools" ? "active" : ""}
            onClick={() => {
              setTab("tools");
              setSelection(null);
            }}
          >
            tools
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
        <button
          className={`refresh-btn ${loading ? "spinning" : ""} ${justRefreshed ? "flash" : ""}`}
          onClick={refreshAll}
          disabled={loading}
          title="refresh results (r)"
          aria-label="refresh"
        >
          ⟳
        </button>
        {tab === "artifacts" && (
          <select value={sortBy} onChange={(e) => setSortBy(e.target.value)}>
            {SORT_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        )}
        {tab === "tools" && (
          <>
            <select
              value={toolNameFilter}
              onChange={(e) => setToolNameFilter(e.target.value)}
              title="tool name"
            >
              <option value="any">any tool</option>
              {TOOL_NAMES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
            <label
              style={{
                fontSize: 11,
                color: "var(--fg-muted)",
                display: "flex",
                alignItems: "center",
                gap: 4,
              }}
              title="when on, narrow candidate sessions via FTS first (fast). when off, scan every session."
            >
              <input
                type="checkbox"
                checked={toolUseFTSPre}
                onChange={(e) => setToolUseFTSPre(e.target.checked)}
              />
              FTS pre-filter
            </label>
          </>
        )}
      </div>

      <div className="filter-chips" ref={chipsRef}>
        {tab === "sessions" &&
        (!!selSessionDate || !!selSessionProject || !!selSessionDirType || !!selSessionTopic) ? (
          <>
            {selSessionDate && (
              <span
                className="filter-chip"
                onClick={() => setSelSessionDate("")}
              >
                date: {selSessionDate} <span className="x">×</span>
              </span>
            )}
            {selSessionProject && (
              <span
                className="filter-chip"
                onClick={() => setSelSessionProject("")}
              >
                project: {selSessionProject} <span className="x">×</span>
              </span>
            )}
            {selSessionDirType && (
              <span
                className="filter-chip"
                onClick={() => setSelSessionDirType("")}
              >
                dir_type: {selSessionDirType} <span className="x">×</span>
              </span>
            )}
            {selSessionTopic && (
              <span
                className="filter-chip"
                onClick={() => setSelSessionTopic("")}
              >
                topic: {selSessionTopic} <span className="x">×</span>
              </span>
            )}
            <span
              className="filter-chip"
              onClick={() => {
                setSelSessionDate("");
                setSelSessionProject("");
                setSelSessionDirType("");
                setSelSessionTopic("");
              }}
              style={{ color: "var(--fg-muted)" }}
            >
              clear all
            </span>
          </>
        ) : tab === "artifacts" &&
          (selType.size + selStatus.size + selLifecycle.size + (selFeature ? 1 : 0) + (selRepo ? 1 : 0)) > 0 ? (
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
            {selFeature && (
              <span
                className="filter-chip"
                onClick={() => setSelFeature("")}
              >
                feature: {selRepo ? `${selRepo}/${selFeature}` : selFeature}{" "}
                <span className="x">×</span>
              </span>
            )}
            {selRepo && (
              <span className="filter-chip" onClick={() => setSelRepo("")}>
                repo: {selRepo} <span className="x">×</span>
              </span>
            )}
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
            <span className="kbd">r</span> refresh ·{" "}
            <span className="kbd">esc</span> clear
          </span>
        )}
      </div>

      <SidebarResizer width={sidebarWidth} onResize={setSidebarWidth} />
      <aside className="sidebar">
        {tab === "artifacts" && facets && (
          <>
            <div className="sidebar-filter">
              <input
                type="search"
                placeholder="filter facets…"
                value={sidebarFilter}
                onChange={(e) => setSidebarFilter(e.target.value)}
              />
            </div>
            <FacetGroup
              title="type"
              counts={facets.byType || {}}
              selected={selType}
              onToggle={(v) => toggleSet(selType, v, setSelType)}
              filter={sidebarFilter}
              isCollapsed={collapsed.has("type")}
              onToggleCollapse={() => toggleCollapsed("type")}
            />
            <FacetGroup
              title="status"
              counts={facets.byStatus || {}}
              selected={selStatus}
              onToggle={(v) => toggleSet(selStatus, v, setSelStatus)}
              filter={sidebarFilter}
              isCollapsed={collapsed.has("status")}
              onToggleCollapse={() => toggleCollapsed("status")}
            />
            <FacetGroup
              title="lifecycle"
              counts={facets.byLifecycle || {}}
              selected={selLifecycle}
              onToggle={(v) => toggleSet(selLifecycle, v, setSelLifecycle)}
              filter={sidebarFilter}
              isCollapsed={collapsed.has("lifecycle")}
              onToggleCollapse={() => toggleCollapsed("lifecycle")}
            />
            <SingleFacetGroup
              title="repo"
              counts={facets.byRepo || {}}
              selected={selRepo}
              onPick={(v) => {
                setSelRepo(v);
                if (
                  selFeature &&
                  v &&
                  !featuresByRepo.some(
                    (f) => f.repo === v && f.feature === selFeature,
                  )
                ) {
                  setSelFeature("");
                }
              }}
              filter={sidebarFilter}
              isCollapsed={collapsed.has("repo")}
              onToggleCollapse={() => toggleCollapsed("repo")}
            />
            <FeaturesByRepoGroup
              rows={featuresByRepo}
              filterRepo={selRepo}
              selected={selFeature}
              onPick={(repo, feature) => {
                setSelRepo(repo);
                setSelFeature(feature);
              }}
              filter={sidebarFilter}
              isCollapsed={collapsed.has("feature")}
              onToggleCollapse={() => toggleCollapsed("feature")}
            />
            {(selType.size > 0 ||
              selStatus.size > 0 ||
              selLifecycle.size > 0 ||
              !!selFeature ||
              !!selRepo) && (
              <button className="facet-clear" onClick={clearFacets}>
                clear filters
              </button>
            )}
          </>
        )}
        {tab === "activity" && (
          <ActivitySidebar
            counts={activityCounts}
            filter={activityFilter}
            onFilter={setActivityFilter}
            heatmap={heatmap}
          />
        )}
        {tab === "tools" && (
          <div style={{ color: "var(--fg-muted)", fontSize: 12 }}>
            <p style={{ marginTop: 0 }}>
              Searches each session's JSONL for <strong>tool_use</strong> blocks.
              Matches on input JSON and the paired tool_result body.
            </p>
            <p>
              <strong>Caveat:</strong> the archive FTS body only stores the
              first 20 Bash commands per session, each clipped at 150 chars,
              and skips other tool inputs / outputs entirely. Untick{" "}
              <em>FTS pre-filter</em> to scan every session jsonl (slow but
              complete).
            </p>
          </div>
        )}
        {tab === "sessions" && sessionFacets && (
          <>
            <div className="sidebar-filter">
              <input
                type="search"
                placeholder="filter facets…"
                value={sidebarFilter}
                onChange={(e) => setSidebarFilter(e.target.value)}
              />
            </div>
            <SingleFacetGroup
              title="date"
              counts={sessionFacets.byDate || {}}
              selected={selSessionDate}
              onPick={setSelSessionDate}
              filter={sidebarFilter}
              isCollapsed={collapsed.has("date")}
              onToggleCollapse={() => toggleCollapsed("date")}
            />
            <SingleFacetGroup
              title="project"
              counts={sessionFacets.byProject || {}}
              selected={selSessionProject}
              onPick={setSelSessionProject}
              filter={sidebarFilter}
              isCollapsed={collapsed.has("project")}
              onToggleCollapse={() => toggleCollapsed("project")}
            />
            <SingleFacetGroup
              title="dir_type"
              counts={sessionFacets.byDirType || {}}
              selected={selSessionDirType}
              onPick={setSelSessionDirType}
              filter={sidebarFilter}
              isCollapsed={collapsed.has("dir_type")}
              onToggleCollapse={() => toggleCollapsed("dir_type")}
            />
            <SingleFacetGroup
              title="topic"
              counts={sessionFacets.byTopic || {}}
              selected={selSessionTopic}
              onPick={setSelSessionTopic}
              minCount={1}
              filter={sidebarFilter}
              isCollapsed={collapsed.has("topic")}
              onToggleCollapse={() => toggleCollapsed("topic")}
            />
            {(selSessionDate ||
              selSessionProject ||
              selSessionDirType ||
              selSessionTopic) && (
              <button
                className="facet-clear"
                onClick={() => {
                  setSelSessionDate("");
                  setSelSessionProject("");
                  setSelSessionDirType("");
                  setSelSessionTopic("");
                }}
              >
                clear filters
              </button>
            )}
          </>
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
        {tab === "tools" &&
          toolHits.map((h, i) => (
            <ToolUseRow
              key={`${h.sessionPath}:${h.turnIndex}:${i}`}
              hit={h}
              selected={
                toolSelected !== null &&
                toolSelected.sessionPath === h.sessionPath &&
                toolSelected.turnIndex === h.turnIndex
              }
              onClick={() => setToolSelected(h)}
              onOpenSession={() => {
                setTab("sessions");
                setSelection({ kind: "session", path: h.sessionPath });
                setTurnFilter(h.inputSummary.split(" ").slice(0, 3).join(" "));
                setToolSelected(null);
              }}
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
        {tab === "activity" && (
          <ActivityList
            repos={repoActivity.filter((r) =>
              activityFilter
                ? r.project.toLowerCase().includes(activityFilter.toLowerCase()) ||
                  r.worktreePath.toLowerCase().includes(activityFilter.toLowerCase())
                : true,
            )}
            expandedWorktree={expandedWorktree}
            expandedFiles={expandedFiles}
            sparklines={sparklines}
            onToggle={(wt) =>
              setExpandedWorktree((cur) => (cur === wt ? null : wt))
            }
          />
        )}
      </section>

      <section className="detail">
        {tab === "tools" && toolSelected && (
          <ToolUseDetail
            hit={toolSelected}
            onOpenSession={() => {
              setTab("sessions");
              setSelection({ kind: "session", path: toolSelected.sessionPath });
              setTurnFilter(
                toolSelected.inputSummary.split(" ").slice(0, 3).join(" "),
              );
              setToolSelected(null);
            }}
          />
        )}
        {tab === "tools" && !toolSelected && (
          <div className="detail-empty">
            {toolHits.length > 0
              ? "pick a row to see the input + output"
              : loading
                ? "loading…"
                : "no results"}
          </div>
        )}
        {tab !== "tools" && !selection && (
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
                {detailArt.worktree && (
                  <span title={detailArt.worktree}>
                    worktree: {shortPath(detailArt.worktree)}
                  </span>
                )}
                {detailArt.branch && <span>branch: {detailArt.branch}</span>}
                {detailArt.feature && (
                  <span>feature: {detailArt.feature}</span>
                )}
                {detailArt.updated && (
                  <span>updated: {formatTime(detailArt.updated)}</span>
                )}
                <span style={{ fontFamily: "ui-monospace", opacity: 0.7 }}>
                  {detailArt.path}
                </span>
                <CopyButton
                  text={
                    detailArt.worktree
                      ? `${detailArt.worktree}/.giantmem/${detailArt.path}`
                      : detailArt.path
                  }
                  label="copy absolute path"
                />
              </div>
            </header>
            <ReactMarkdown
              remarkPlugins={[remarkGfm]}
              rehypePlugins={[rehypeHighlight as any]}
            >
              {detailBody}
            </ReactMarkdown>
          </>
        )}
        {selection?.kind === "session" && (
          <SessionDetail
            path={selection.path}
            turns={sessionLines}
            filter={turnFilter}
            onFilterChange={setTurnFilter}
            defaultExpanded={defaultExpanded}
            expandRev={expandRev}
            order={turnOrder}
            onToggleOrder={() =>
              setTurnOrder((o) => (o === "desc" ? "asc" : "desc"))
            }
            onExpandAll={() => {
              setDefaultExpanded(true);
              setExpandRev((r) => r + 1);
            }}
            onCollapseAll={() => {
              setDefaultExpanded(false);
              setExpandRev((r) => r + 1);
            }}
          />
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
  filter = "",
  isCollapsed = false,
  onToggleCollapse,
}: {
  title: string;
  counts: Record<string, number>;
  selected: Set<string>;
  onToggle: (v: string) => void;
  filter?: string;
  isCollapsed?: boolean;
  onToggleCollapse?: () => void;
}) {
  const q = filter.toLowerCase();
  const sorted = Object.entries(counts)
    .filter(([k]) => !q || k.toLowerCase().includes(q))
    .sort((a, b) => b[1] - a[1]);
  return (
    <div className="facet-group">
      <FacetHeader
        title={title}
        count={sorted.length}
        isCollapsed={isCollapsed}
        onToggleCollapse={onToggleCollapse}
        activeCount={selected.size}
      />
      {!isCollapsed &&
        sorted.map(([v, n]) => (
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

function SidebarResizer({
  width,
  onResize,
}: {
  width: number;
  onResize: (w: number) => void;
}) {
  const dragging = useRef(false);
  const startX = useRef(0);
  const startW = useRef(0);
  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!dragging.current) return;
      const dx = e.clientX - startX.current;
      const next = Math.max(180, Math.min(600, startW.current + dx));
      onResize(next);
    };
    const onUp = () => {
      dragging.current = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, [onResize]);
  return (
    <div
      className="sidebar-resizer"
      style={{ left: `${width}px` }}
      title="drag to resize"
      onMouseDown={(e) => {
        dragging.current = true;
        startX.current = e.clientX;
        startW.current = width;
        document.body.style.cursor = "col-resize";
        document.body.style.userSelect = "none";
      }}
      onDoubleClick={() => onResize(260)}
    />
  );
}

function CopyButton({ text, label }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  if (!text) return null;
  const onClick = async (e: React.MouseEvent) => {
    e.stopPropagation();
    try {
      await ClipboardSetText(text);
    } catch {
      try {
        await navigator.clipboard.writeText(text);
      } catch {
        return;
      }
    }
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  };
  return (
    <button
      className={`copy-btn ${copied ? "copied" : ""}`}
      onClick={onClick}
      title={label || `copy: ${text}`}
      aria-label="copy"
    >
      {copied ? "✓" : "⎘"}
    </button>
  );
}

function FacetHeader({
  title,
  count,
  isCollapsed,
  onToggleCollapse,
  activeCount = 0,
}: {
  title: string;
  count: number;
  isCollapsed: boolean;
  onToggleCollapse?: () => void;
  activeCount?: number;
}) {
  return (
    <h4 className="facet-header" onClick={onToggleCollapse}>
      <span>
        <span className="facet-arrow">{isCollapsed ? "▸" : "▾"}</span> {title}
      </span>
      <span className="facet-header-meta">
        {activeCount > 0 && <span className="active-dot">{activeCount}</span>}
        <span className="facet-header-count">{count}</span>
      </span>
    </h4>
  );
}

function FeaturesByRepoGroup({
  rows,
  filterRepo,
  selected,
  onPick,
  filter = "",
  isCollapsed = false,
  onToggleCollapse,
}: {
  rows: main.FeatureRow[];
  filterRepo: string;
  selected: string;
  onPick: (repo: string, feature: string) => void;
  filter?: string;
  isCollapsed?: boolean;
  onToggleCollapse?: () => void;
}) {
  const q = filter.toLowerCase();
  const grouped = useMemo(() => {
    const out: Record<string, main.FeatureRow[]> = {};
    for (const r of rows) {
      if (filterRepo && r.repo !== filterRepo) continue;
      if (q && !r.feature.toLowerCase().includes(q) && !r.repo.toLowerCase().includes(q)) {
        continue;
      }
      (out[r.repo] = out[r.repo] || []).push(r);
    }
    return out;
  }, [rows, filterRepo, q]);
  const repos = Object.keys(grouped).sort();
  const total = repos.reduce((acc, r) => acc + grouped[r].length, 0);
  if (total === 0 && !selected) return null;
  return (
    <div className="facet-group">
      <FacetHeader
        title="feature"
        count={total}
        isCollapsed={isCollapsed}
        onToggleCollapse={onToggleCollapse}
        activeCount={selected ? 1 : 0}
      />
      {!isCollapsed &&
        repos.map((repo) => (
          <div key={repo} className="repo-block">
            {!filterRepo && <div className="repo-label">{repo}</div>}
            {grouped[repo].map((r) => (
              <div
                key={`${r.repo}/${r.feature}`}
                className={`facet-row ${
                  selected === r.feature &&
                  (!filterRepo || filterRepo === r.repo)
                    ? "selected"
                    : ""
                }`}
                onClick={() =>
                  onPick(r.repo, selected === r.feature ? "" : r.feature)
                }
                title={r.worktree || ""}
              >
                <span>{r.feature}</span>
                <span className="count">{r.count}</span>
              </div>
            ))}
          </div>
        ))}
    </div>
  );
}

function SingleFacetGroup({
  title,
  counts,
  selected,
  onPick,
  minCount = 0,
  filter = "",
  isCollapsed = false,
  onToggleCollapse,
}: {
  title: string;
  counts: Record<string, number>;
  selected: string;
  onPick: (v: string) => void;
  minCount?: number;
  filter?: string;
  isCollapsed?: boolean;
  onToggleCollapse?: () => void;
}) {
  const q = filter.toLowerCase();
  const sorted = Object.entries(counts)
    .filter(([k, n]) => k !== "" && n >= minCount && (!q || k.toLowerCase().includes(q)))
    .sort((a, b) => b[1] - a[1]);
  if (sorted.length === 0 && !selected) return null;
  return (
    <div className="facet-group">
      <FacetHeader
        title={title}
        count={sorted.length}
        isCollapsed={isCollapsed}
        onToggleCollapse={onToggleCollapse}
        activeCount={selected ? 1 : 0}
      />
      {!isCollapsed &&
        sorted.map(([v, n]) => (
          <div
            key={v}
            className={`facet-row ${selected === v ? "selected" : ""}`}
            onClick={() => onPick(selected === v ? "" : v)}
          >
            <span>{v}</span>
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
        <strong>{row.repo}</strong>
        {row.worktree && shortPath(row.worktree) !== row.repo && (
          <> · <span title={row.worktree}>{shortPath(row.worktree)}</span></>
        )}
        {row.feature && <> · <span style={{ color: "var(--accent-2)" }}>{row.feature}</span></>}
        {row.branch && row.branch !== "main" && <> · {row.branch}</>}
        {" · "}
        {formatTime(row.updated)}
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

function ToolUseRow({
  hit,
  selected,
  onClick,
  onOpenSession,
}: {
  hit: main.ToolUseHit;
  selected: boolean;
  onClick: () => void;
  onOpenSession: () => void;
}) {
  return (
    <div
      className={`result-row ${selected ? "selected" : ""}`}
      onClick={onClick}
    >
      <div className="row-head">
        <span className={`chip tool ${hit.isError ? "err" : ""}`}>
          {hit.toolName}
        </span>
        <span className="row-title">{hit.inputSummary || "(no summary)"}</span>
        {hit.timestamp && (
          <span className="row-meta">{formatTime(hit.timestamp)}</span>
        )}
      </div>
      <div className="row-meta">
        {hit.project && <strong>{hit.project}</strong>}
        {hit.sessionId && (
          <>
            {" · "}
            <span style={{ fontFamily: "ui-monospace" }}>
              {hit.sessionId.slice(0, 8)}
            </span>
          </>
        )}
        {" · turn "}
        {hit.turnIndex}
        {hit.isError && <span style={{ color: "var(--danger)" }}> · error</span>}
        {" · "}
        <a
          onClick={(e) => {
            e.stopPropagation();
            onOpenSession();
          }}
          style={{ color: "var(--accent)", cursor: "pointer" }}
        >
          open session ›
        </a>
      </div>
      {hit.outputClip && (
        <div className="row-meta" style={{ marginTop: 4, opacity: 0.75 }}>
          {hit.outputClip}
        </div>
      )}
    </div>
  );
}

function ToolUseDetail({
  hit,
  onOpenSession,
}: {
  hit: main.ToolUseHit;
  onOpenSession: () => void;
}) {
  let inputObj: any = null;
  try {
    inputObj = JSON.parse(hit.inputJSON);
  } catch {
    inputObj = hit.inputJSON;
  }
  const inputText = JSON.stringify(inputObj, null, 2);
  return (
    <>
      <header className="detail-head">
        <h2 style={{ marginTop: 0, marginBottom: 0 }}>
          {hit.toolName} <span style={{ color: "var(--fg-muted)" }}>· {hit.inputSummary}</span>
        </h2>
        <div className="meta">
          {hit.project && <span>project: {hit.project}</span>}
          {hit.sessionId && (
            <span title={hit.sessionId}>
              session: {hit.sessionId.slice(0, 12)}
            </span>
          )}
          <span>turn {hit.turnIndex}</span>
          {hit.timestamp && <span>{formatTime(hit.timestamp)}</span>}
          <span style={{ fontFamily: "ui-monospace", opacity: 0.7 }}>
            {hit.sessionPath}
          </span>
          <CopyButton text={hit.sessionPath} label="copy session path" />
          <button
            onClick={onOpenSession}
            style={{
              padding: "2px 8px",
              fontSize: 11,
            }}
          >
            open session ›
          </button>
        </div>
      </header>
      <div className="tool-body" style={{ padding: 10, marginTop: 0 }}>
        <ToolSection title="Input" body={inputText} mono />
        {hit.output !== undefined && hit.output !== "" && (
          <ToolSection
            title="Output"
            body={hit.output}
            mono
            statusColor={hit.isError ? "danger" : "success"}
          />
        )}
        {(hit.output === undefined || hit.output === "") && !hit.isError && (
          <p style={{ color: "var(--fg-muted)", marginTop: 8 }}>
            (no tool_result paired in JSONL)
          </p>
        )}
      </div>
    </>
  );
}

function ActivityList({
  repos,
  expandedWorktree,
  expandedFiles,
  sparklines,
  onToggle,
}: {
  repos: main.RepoActivity[];
  expandedWorktree: string | null;
  expandedFiles: main.FileActivity[];
  sparklines: Record<string, main.SparklinePoint[]>;
  onToggle: (worktree: string) => void;
}) {
  if (!repos.length) {
    return (
      <div className="row-meta" style={{ padding: 12 }}>
        no activity (live.db empty? or filter excludes everything)
      </div>
    );
  }
  return (
    <>
      {repos.map((r) => {
        const isOpen = expandedWorktree === r.worktreePath;
        const spark = sparklines[r.worktreePath] || [];
        return (
          <div key={r.worktreePath} className="result-row">
            <div
              onClick={() => onToggle(r.worktreePath)}
              style={{ cursor: "pointer", display: "flex", alignItems: "center", gap: 10 }}
            >
              <div style={{ flex: 1, minWidth: 0 }}>
                <div className="row-head">
                  <span className="chip">{isOpen ? "▾" : "▸"}</span>
                  <span className="row-title">{r.project}</span>
                  <span className="row-meta">{ago(r.mtime)}</span>
                </div>
                <div className="row-meta">
                  {r.docCount} doc{r.docCount === 1 ? "" : "s"} · {r.worktreePath}
                </div>
              </div>
              {spark.length > 0 && <Sparkline points={spark} />}
            </div>
            {isOpen && (
              <div style={{ marginTop: 8, paddingLeft: 12, borderLeft: "2px solid var(--border)" }}>
                {expandedFiles.length === 0 && (
                  <div className="row-meta">loading…</div>
                )}
                {expandedFiles.map((f) => (
                  <div key={f.path} style={{ padding: "3px 0", fontSize: 12 }}>
                    <span className="row-meta" style={{ marginRight: 8 }}>
                      {ago(f.mtime)}
                    </span>
                    {f.feature && (
                      <span className="chip" style={{ marginRight: 6 }}>
                        {f.feature}
                      </span>
                    )}
                    {f.dirType && (
                      <span className="row-meta" style={{ marginRight: 6 }}>
                        {f.dirType}
                      </span>
                    )}
                    <span style={{ fontFamily: "ui-monospace" }}>
                      {trimWorktree(f.path, r.worktreePath)}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>
        );
      })}
    </>
  );
}

// ago renders a unix-second mtime as a short relative string (e.g. "23m",
// "11h", "3d"). Used by ActivityList; keep colocated to avoid pulling in a
// date lib for one place.
function ago(mtime: number): string {
  const s = Math.max(0, Math.floor(Date.now() / 1000 - mtime));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h`;
  const d = Math.floor(h / 24);
  return `${d}d`;
}

function trimWorktree(path: string, wt: string): string {
  if (wt && path.startsWith(wt + "/")) return path.slice(wt.length + 1);
  return path;
}

// Sparkline renders a fixed-width inline SVG bar chart of doc-write counts
// per day. Empty days render as a flat baseline so all rows visually align.
function Sparkline({ points }: { points: main.SparklinePoint[] }) {
  const w = 84;
  const h = 22;
  const max = Math.max(1, ...points.map((p) => p.count));
  const bw = w / points.length;
  return (
    <svg
      width={w}
      height={h}
      viewBox={`0 0 ${w} ${h}`}
      style={{ flexShrink: 0, opacity: 0.85 }}
      aria-label="7-day activity"
    >
      {points.map((p, i) => {
        const bh = (p.count / max) * (h - 2);
        return (
          <rect
            key={p.day}
            x={i * bw + 0.5}
            y={h - bh}
            width={Math.max(1, bw - 1)}
            height={Math.max(1, bh)}
            fill={p.count === 0 ? "var(--border)" : "var(--accent)"}
          />
        );
      })}
    </svg>
  );
}

function ActivitySidebar({
  counts,
  filter,
  onFilter,
  heatmap,
}: {
  counts: main.ActivityCounts | null;
  filter: string;
  onFilter: (v: string) => void;
  heatmap: main.HeatmapCell[];
}) {
  return (
    <>
      {counts && (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "1fr 1fr",
            gap: 6,
            padding: "8px 4px",
          }}
        >
          <Tile label="live docs" value={counts.liveDocs} />
          <Tile label="sessions" value={counts.sessions} />
          <Tile label="writes today" value={counts.writesToday} accent />
          <Tile label="active features" value={counts.activeFeatures} />
        </div>
      )}
      <div className="sidebar-filter">
        <input
          type="search"
          placeholder="filter projects…"
          value={filter}
          onChange={(e) => onFilter(e.target.value)}
        />
      </div>
      {heatmap.length > 0 && <HeatmapPanel cells={heatmap} />}
    </>
  );
}

function Tile({
  label,
  value,
  accent,
}: {
  label: string;
  value: number;
  accent?: boolean;
}) {
  return (
    <div
      style={{
        background: "var(--bg-2)",
        border: "1px solid var(--border)",
        borderRadius: 4,
        padding: "6px 8px",
      }}
    >
      <div
        style={{
          fontSize: 16,
          fontWeight: 700,
          color: accent ? "var(--accent)" : "var(--fg)",
        }}
      >
        {value.toLocaleString()}
      </div>
      <div style={{ fontSize: 10, color: "var(--fg-muted)" }}>{label}</div>
    </div>
  );
}

// HeatmapPanel pivots flat HeatmapCell[] into a project × day grid.
// Cell color saturates with count relative to the panel's max so a sleepy
// repo still shows shape, not just one bright row.
function HeatmapPanel({ cells }: { cells: main.HeatmapCell[] }) {
  if (!cells.length) return null;
  const byWt = new Map<string, { project: string; days: main.HeatmapCell[] }>();
  for (const c of cells) {
    if (!byWt.has(c.worktreePath)) {
      byWt.set(c.worktreePath, { project: c.project, days: [] });
    }
    byWt.get(c.worktreePath)!.days.push(c);
  }
  const max = Math.max(1, ...cells.map((c) => c.count));
  const rows = Array.from(byWt.entries());
  return (
    <div style={{ padding: "8px 4px" }}>
      <div
        style={{
          fontSize: 11,
          color: "var(--fg-muted)",
          marginBottom: 6,
          letterSpacing: "0.04em",
          textTransform: "uppercase",
        }}
      >
        last 14 days · top {rows.length}
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {rows.map(([wt, info]) => (
          <div
            key={wt}
            style={{ display: "flex", alignItems: "center", gap: 6 }}
          >
            <div
              style={{
                flex: 1,
                fontSize: 11,
                color: "var(--fg-muted)",
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
              title={wt}
            >
              {info.project}
            </div>
            <div style={{ display: "flex", gap: 1 }}>
              {info.days.map((c) => {
                const intensity = c.count === 0 ? 0 : 0.15 + (c.count / max) * 0.85;
                return (
                  <div
                    key={c.day}
                    title={`${c.day}: ${c.count}`}
                    style={{
                      width: 9,
                      height: 9,
                      background:
                        c.count === 0
                          ? "var(--bg-3)"
                          : `color-mix(in srgb, var(--accent) ${Math.round(
                              intensity * 100,
                            )}%, transparent)`,
                      border: "1px solid var(--border)",
                    }}
                  />
                );
              })}
            </div>
          </div>
        ))}
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
  | {
      type: "tool_call";
      name: string;
      input: any;
      id?: string;
      result?: string;
      isError?: boolean;
    }
  | { type: "tool_result"; content: any; tool_use_id?: string; is_error?: boolean }
  | { type: "thinking"; thinking: string }
  | { type: "unknown"; raw: any };

type SessionTurn = {
  role: string;
  timestamp?: string;
  toolCount: number;
  textCount: number;
  thinkingCount: number;
  blocks: ContentBlock[];
  raw: any;
};

function parseJSONL(raw: string): SessionTurn[] {
  const turns: SessionTurn[] = [];
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
      turns.push({
        role,
        timestamp: obj.timestamp || obj.ts || undefined,
        toolCount: 0,
        textCount: 0,
        thinkingCount: 0,
        blocks,
        raw: obj,
      });
    } catch {
      turns.push({
        role: "raw",
        toolCount: 0,
        textCount: 0,
        thinkingCount: 0,
        blocks: [{ type: "text", text: line }],
        raw: null,
      });
    }
  }
  // pair tool_use with its tool_result by tool_use_id, then drop the
  // standalone result blocks. devtools-style nesting.
  const resultByID = new Map<string, { text: string; isError: boolean }>();
  for (const t of turns) {
    for (const b of t.blocks) {
      if (b.type === "tool_result" && b.tool_use_id) {
        resultByID.set(b.tool_use_id, {
          text: stringifyResult(b.content),
          isError: !!b.is_error,
        });
      }
    }
  }
  for (const t of turns) {
    t.blocks = t.blocks
      .map((b): ContentBlock => {
        if (b.type === "tool_call") {
          const r = b.id ? resultByID.get(b.id) : undefined;
          return r
            ? { ...b, result: r.text, isError: r.isError }
            : b;
        }
        return b;
      })
      .filter((b) => {
        if (b.type !== "tool_result") return true;
        return !(b.tool_use_id && resultByID.has(b.tool_use_id));
      });
    for (const b of t.blocks) {
      if (b.type === "tool_call") t.toolCount++;
      else if (b.type === "text") t.textCount++;
      else if (b.type === "thinking") t.thinkingCount++;
    }
  }
  // Drop empty turns. Many user turns hold only system-reminder text or
  // tool_result blocks that just paired upward — they leave nothing
  // worth showing. A turn survives if it has any tool call, any
  // thinking block, or any non-blank text/result content.
  return turns.filter((t) => {
    if (t.toolCount > 0 || t.thinkingCount > 0) return true;
    for (const b of t.blocks) {
      if (b.type === "text" && b.text.trim() !== "") return true;
      if (
        b.type === "tool_result" &&
        stringifyResult(b.content).trim() !== ""
      ) {
        return true;
      }
      if (b.type === "unknown") return true;
    }
    return false;
  });
}

function stringifyResult(content: any): string {
  if (content == null) return "";
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((c: any) =>
        typeof c === "string" ? c : c?.text || JSON.stringify(c, null, 2),
      )
      .join("\n");
  }
  return JSON.stringify(content, null, 2);
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
          type: "tool_call",
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

// describeTool produces a one-line caption for a tool call from its input —
// the bit the devtools header shows next to the tool name.
function describeTool(name: string, input: any): string {
  if (!input || typeof input !== "object") return "";
  switch (name) {
    case "Bash":
      return clip(input.description || input.command || "", 80);
    case "Read":
      return clip(input.file_path || "", 80);
    case "Write":
      return clip(input.file_path || "", 80);
    case "Edit":
      return clip(input.file_path || "", 80);
    case "Glob":
      return clip(input.pattern || "", 80);
    case "Grep":
      return clip(
        `${input.pattern || ""}${input.path ? ` in ${input.path}` : ""}`,
        80,
      );
    case "TodoWrite":
      return Array.isArray(input.todos) ? `${input.todos.length} todos` : "";
    case "WebFetch":
    case "WebSearch":
      return clip(input.url || input.query || "", 80);
    case "Skill":
      return clip(input.skill || "", 40);
    case "Agent":
      return clip(input.description || input.subagent_type || "", 60);
  }
  // generic: first string-valued prop
  for (const [k, v] of Object.entries(input)) {
    if (typeof v === "string") return clip(`${k}: ${v}`, 80);
  }
  return "";
}

function clip(s: string, n: number): string {
  if (!s) return "";
  s = s.replace(/\s+/g, " ").trim();
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}

function SessionDetail({
  path,
  turns,
  filter,
  onFilterChange,
  defaultExpanded,
  expandRev,
  order,
  onToggleOrder,
  onExpandAll,
  onCollapseAll,
}: {
  path: string;
  turns: SessionTurn[] | null;
  filter: string;
  onFilterChange: (s: string) => void;
  defaultExpanded: boolean;
  expandRev: number;
  order: "asc" | "desc";
  onToggleOrder: () => void;
  onExpandAll: () => void;
  onCollapseAll: () => void;
}) {
  const filtered = useMemo(() => {
    if (!turns) return null;
    const q = filter.trim().toLowerCase();
    const base = q ? turns.filter((t) => turnMatches(t, q)) : turns;
    // JSONL is appended oldest -> newest; default desc shows newest first.
    return order === "desc" ? [...base].reverse() : base;
  }, [turns, filter, order]);
  const totalTools = useMemo(
    () => (turns || []).reduce((acc, t) => acc + t.toolCount, 0),
    [turns],
  );
  return (
    <>
      <header className="detail-head">
        <h2 style={{ marginTop: 0, marginBottom: 0 }}>{path.split("/").pop()}</h2>
        <div className="meta">
          <span style={{ fontFamily: "ui-monospace", opacity: 0.7 }}>{path}</span>
          <CopyButton text={path} label="copy file path" />
          {turns && (
            <span>
              {turns.length} turn{turns.length === 1 ? "" : "s"} ·{" "}
              {totalTools} tool call{totalTools === 1 ? "" : "s"}
            </span>
          )}
        </div>
      </header>
      <div className="transcript-toolbar">
        <input
          type="search"
          placeholder="filter turns…"
          value={filter}
          onChange={(e) => onFilterChange(e.target.value)}
        />
        <button
          onClick={onToggleOrder}
          title={
            order === "desc"
              ? "newest first — click for oldest first"
              : "oldest first — click for newest first"
          }
        >
          {order === "desc" ? "↓ newest" : "↑ oldest"}
        </button>
        <button onClick={onExpandAll}>expand all</button>
        <button onClick={onCollapseAll}>collapse all</button>
      </div>
      {turns === null && <div>loading transcript…</div>}
      {filtered &&
        filtered.map((t, i) => (
          <SessionTurnView
            key={`${expandRev}:${i}`}
            turn={t}
            defaultOpen={defaultExpanded}
            filter={filter}
          />
        ))}
      {filtered && filtered.length === 0 && (
        <div className="detail-empty">no turns match filter</div>
      )}
    </>
  );
}

function turnMatches(t: SessionTurn, q: string): boolean {
  if (t.role.toLowerCase().includes(q)) return true;
  for (const b of t.blocks) {
    if (b.type === "text" && b.text.toLowerCase().includes(q)) return true;
    if (b.type === "thinking" && b.thinking.toLowerCase().includes(q)) return true;
    if (b.type === "tool_call") {
      if (b.name.toLowerCase().includes(q)) return true;
      const desc = describeTool(b.name, b.input).toLowerCase();
      if (desc.includes(q)) return true;
      if (b.result && b.result.toLowerCase().includes(q)) return true;
      if (
        b.input &&
        JSON.stringify(b.input).toLowerCase().includes(q)
      )
        return true;
    }
  }
  return false;
}

function SessionTurnView({
  turn,
  defaultOpen,
  filter,
}: {
  turn: SessionTurn;
  defaultOpen: boolean;
  filter: string;
}) {
  const summary: string[] = [];
  if (turn.textCount) summary.push(`${turn.textCount} msg`);
  if (turn.thinkingCount) summary.push(`${turn.thinkingCount} think`);
  if (turn.toolCount)
    summary.push(`${turn.toolCount} tool call${turn.toolCount === 1 ? "" : "s"}`);
  return (
    <details className={`session-turn ${turn.role}`} open={defaultOpen}>
      <summary className="turn-summary">
        <span className="turn-caret">▸</span>
        <span className="who">{turn.role}</span>
        {summary.length > 0 && (
          <span className="turn-summary-meta">{summary.join(" · ")}</span>
        )}
        <span className="turn-summary-spacer" />
        {turn.timestamp && (
          <span className="turn-time" title={formatRelative(turn.timestamp)}>
            {formatTime(turn.timestamp)}
          </span>
        )}
      </summary>
      <div className="turn-body">
        {turn.blocks.map((b, i) => (
          <BlockView
            key={i}
            block={b}
            defaultOpen={defaultOpen}
            filter={filter}
          />
        ))}
      </div>
    </details>
  );
}

function BlockView({
  block,
  defaultOpen,
  filter,
}: {
  block: ContentBlock;
  defaultOpen: boolean;
  filter: string;
}) {
  if (block.type === "text") {
    return (
      <div className="turn-text">
        <ReactMarkdown
          remarkPlugins={[remarkGfm]}
          rehypePlugins={[rehypeHighlight as any]}
        >
          {block.text}
        </ReactMarkdown>
      </div>
    );
  }
  if (block.type === "tool_call") {
    const desc = describeTool(block.name, block.input);
    // auto-open when a filter matches something inside this tool call.
    const q = filter.trim().toLowerCase();
    const filterHit =
      !!q &&
      ((block.name && block.name.toLowerCase().includes(q)) ||
        desc.toLowerCase().includes(q) ||
        (block.result && block.result.toLowerCase().includes(q)) ||
        JSON.stringify(block.input || {}).toLowerCase().includes(q));
    const open = defaultOpen || filterHit;
    return (
      <details className={`tool-call ${block.isError ? "is-error" : ""}`} open={open}>
        <summary>
          <span className="tool-caret">▸</span>
          <span className="tool-icon">🔧</span>
          <strong className="tool-name">{block.name}</strong>
          {desc && (
            <>
              <span className="tool-dash">—</span>
              <span className="tool-desc">{desc}</span>
            </>
          )}
          {block.isError && <span className="chip tool err">error</span>}
        </summary>
        <div className="tool-body">
          <ToolSection title="Input" body={JSON.stringify(block.input, null, 2)} mono />
          {block.result !== undefined && (
            <ToolSection
              title="Output"
              body={block.result}
              mono
              statusColor={block.isError ? "danger" : "success"}
            />
          )}
        </div>
      </details>
    );
  }
  if (block.type === "tool_result") {
    // orphan tool_result: render minimally.
    return (
      <details className="tool-call orphan" open={defaultOpen}>
        <summary>
          <span className="tool-caret">▸</span>
          <span className="chip tool">tool_result</span>
        </summary>
        <ToolSection title="Output" body={stringifyResult(block.content)} mono />
      </details>
    );
  }
  if (block.type === "thinking") {
    return (
      <details className="tool-call thinking-block" open={defaultOpen}>
        <summary>
          <span className="tool-caret">▸</span>
          <span className="chip thinking">thinking</span>
        </summary>
        <pre className="tool-pre" style={{ opacity: 0.85 }}>
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

function ToolSection({
  title,
  body,
  mono = false,
  statusColor,
}: {
  title: string;
  body: string;
  mono?: boolean;
  statusColor?: "success" | "danger";
}) {
  const [open, setOpen] = useState(true);
  return (
    <div className="tool-section">
      <div
        className="tool-section-head"
        onClick={() => setOpen((o) => !o)}
      >
        <span className="tool-section-caret">{open ? "▾" : "▸"}</span>
        <span className="tool-section-title">{title}</span>
        {statusColor && (
          <span className={`tool-dot ${statusColor}`} aria-hidden />
        )}
      </div>
      {open && (
        <pre className={`tool-pre ${mono ? "" : "wrap"}`}>{body || "(empty)"}</pre>
      )}
    </div>
  );
}

// shortPath returns ~/last-2-segments form of an absolute path so the UI can
// surface worktree context like 'cc-wt/stage' or 'python/recharge-auth'
// without eating row width.
// filterByDateBucket trims a hit list to one of the same bucket labels the
// backend's dateBucketWhere recognises ("today", "yesterday", "7d", "30d",
// "older"). Used on the FTS path which doesn't see those buckets.
function filterByDateBucket(hits: search.Hit[], bucket: string): search.Hit[] {
  if (!bucket) return hits;
  const now = new Date();
  const dayMs = 86400_000;
  return hits.filter((h) => {
    if (!h.timestamp) return false;
    const t = new Date(h.timestamp);
    if (Number.isNaN(t.getTime())) return false;
    const diff = (now.getTime() - t.getTime()) / dayMs;
    const sameLocalDay = (a: Date, b: Date) =>
      a.getFullYear() === b.getFullYear() &&
      a.getMonth() === b.getMonth() &&
      a.getDate() === b.getDate();
    const yest = new Date(now.getTime() - dayMs);
    if (bucket === "today") return sameLocalDay(t, now);
    if (bucket === "yesterday") return sameLocalDay(t, yest);
    if (bucket === "7d") return diff > 1 && diff <= 7;
    if (bucket === "30d") return diff > 7 && diff <= 30;
    if (bucket === "older") return diff > 30;
    return true;
  });
}

function debounce<F extends (...a: any[]) => void>(fn: F, ms: number): F {
  let t: number | undefined;
  return ((...args: any[]) => {
    if (t) window.clearTimeout(t);
    t = window.setTimeout(() => fn(...args), ms);
  }) as F;
}

function shortPath(p?: string): string {
  if (!p) return "";
  const segs = p.split("/").filter(Boolean);
  if (segs.length <= 2) return p;
  return segs.slice(-2).join("/");
}

type ParsedTs = { date: Date; hasTime: boolean };

// parseTimestamp handles ISO 8601, the YYYYMMDD_HHMMSS form the documents
// table writes from file mtime, and bare YYYY-MM-DD dates from frontmatter.
// hasTime tells the renderer whether to show the time portion — without it
// we'd render date-only frontmatter as 12:00 AM (or local 5 PM, etc, since
// 'new Date(\"2026-06-02\")' resolves to UTC midnight and shifts in local tz).
function parseTimestamp(s?: string): ParsedTs | null {
  if (!s) return null;
  const compact = s.match(/^(\d{4})(\d{2})(\d{2})_(\d{2})(\d{2})(\d{2})$/);
  if (compact) {
    return {
      date: new Date(
        Number(compact[1]),
        Number(compact[2]) - 1,
        Number(compact[3]),
        Number(compact[4]),
        Number(compact[5]),
        Number(compact[6]),
      ),
      hasTime: true,
    };
  }
  const dateOnly = s.match(/^(\d{4})-(\d{2})-(\d{2})$/);
  if (dateOnly) {
    return {
      date: new Date(
        Number(dateOnly[1]),
        Number(dateOnly[2]) - 1,
        Number(dateOnly[3]),
      ),
      hasTime: false,
    };
  }
  const t = new Date(s);
  if (Number.isNaN(t.getTime())) return null;
  // anything else is treated as having a real wall-clock time
  return { date: t, hasTime: true };
}

// formatTime renders an absolute local datetime — 2026-06-01 12:30 PM —
// across rows, detail headers, and transcript turns. Use formatRelative
// when you want '5m ago' as a tooltip instead.
function formatTime(s?: string): string {
  const p = parseTimestamp(s);
  if (!p) return s || "";
  const t = p.date;
  const y = t.getFullYear();
  const mo = String(t.getMonth() + 1).padStart(2, "0");
  const d = String(t.getDate()).padStart(2, "0");
  if (!p.hasTime) return `${y}-${mo}-${d}`;
  let h = t.getHours();
  const min = String(t.getMinutes()).padStart(2, "0");
  const ap = h >= 12 ? "PM" : "AM";
  h = h % 12;
  if (h === 0) h = 12;
  return `${y}-${mo}-${d} ${h}:${min} ${ap}`;
}

function formatRelative(s?: string): string {
  const p = parseTimestamp(s);
  if (!p) return s || "";
  const diff = (Date.now() - p.date.getTime()) / 1000;
  if (diff < 60) return `${Math.floor(diff)}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  if (diff < 86400 * 7) return `${Math.floor(diff / 86400)}d ago`;
  return formatTime(s);
}

export default App;
