import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import "highlight.js/styles/github-dark.css";
import {
  BrowserOpenURL,
  ClipboardSetText,
  WindowGetPosition,
  WindowGetSize,
  WindowSetPosition,
  WindowSetSize,
} from "../wailsjs/runtime/runtime";
import "./App.css";
import {
  ActivityCounts,
  BrowseTree,
  DeleteProject,
  FacetCounts,
  GetArtifactBody,
  GetLiveBody,
  GetPref,
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
  SessionPathByID,
  SetPref,
  Version,
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

type DateRange = { preset: string; from: string; to: string };

// preset -> duration spec understood by the Go side (search.parseSinceUntil).
// "all" clears the bound; "custom" uses the from/to date inputs.
const DATE_PRESETS: { value: string; label: string }[] = [
  { value: "all", label: "All time" },
  { value: "15m", label: "Last 15 min" },
  { value: "1h", label: "Last 1 hour" },
  { value: "4h", label: "Last 4 hours" },
  { value: "24h", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
  { value: "90d", label: "Last 90 days" },
  { value: "custom", label: "Custom range…" },
];

function rangeSinceUntil(r: DateRange): { since: string; until: string } {
  if (r.preset === "all") return { since: "", until: "" };
  if (r.preset === "custom") return { since: r.from || "", until: r.to || "" };
  return { since: r.preset, until: "" };
}

// client-side ms bounds for browse mode (mtime filtering without a backend trip)
function rangeToMs(r: DateRange): { from: number; to: number } {
  if (r.preset === "all") return { from: 0, to: Infinity };
  if (r.preset === "custom") {
    const from = r.from ? Date.parse(r.from) : 0;
    const to = r.to ? Date.parse(r.to) + 86400000 : Infinity;
    return { from, to };
  }
  const m = /^(\d+)([mhd])$/.exec(r.preset);
  if (!m) return { from: 0, to: Infinity };
  const unit = m[2] === "m" ? 60000 : m[2] === "h" ? 3600000 : 86400000;
  return { from: Date.now() - Number(m[1]) * unit, to: Infinity };
}

// sidebar filter grammar: free text (substring over rel/repo/feature/worktree)
// plus ext:md style tokens
type SidebarFilter = { text: string; ext: string };

function parseSidebarFilter(q: string): SidebarFilter {
  let ext = "";
  const words: string[] = [];
  for (const w of q.trim().toLowerCase().split(/\s+/)) {
    if (!w) continue;
    if (w.startsWith("ext:")) ext = w.slice(4).replace(/^\./, "");
    else words.push(w);
  }
  return { text: words.join(" "), ext };
}

function rowMatchesFilter(r: main.BrowseRow, f: SidebarFilter): boolean {
  if (f.ext) {
    const name = r.rel.split("/").pop() || "";
    const e = name.includes(".") ? name.split(".").pop()!.toLowerCase() : "";
    if (e !== f.ext) return false;
  }
  if (
    f.text &&
    !`${r.rel} ${r.repo} ${r.feature} ${r.worktree}`.toLowerCase().includes(f.text)
  ) {
    return false;
  }
  return true;
}

type Selection =
  | { kind: "artifact"; id: string }
  | { kind: "file"; path: string }
  | { kind: "session"; path: string }
  | null;

function App() {
  const [tab, setTab] = useState<Tab>("activity");
  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");

  const [selFeature, setSelFeature] = useState<string>("");
  const [selRepo, setSelRepo] = useState<string>("");
  const [sidebarFilter, setSidebarFilter] = useState("");
  const [browseRows, setBrowseRows] = useState<main.BrowseRow[]>([]);
  const [treeExpanded, setTreeExpanded] = useState<Set<string>>(new Set());
  // history docs = per-session turn-set summaries. Useful, but they bump on
  // every session and drown the recent view — hence the toggle.
  const [showHistory, setShowHistory] = useState<boolean>(
    () => localStorage.getItem("gm.showHistory") !== "0",
  );
  useEffect(() => {
    GetPref("showHistory")
      .then((v) => {
        if (v) setShowHistory(v !== "0");
      })
      .catch(() => {});
  }, []);
  useEffect(() => {
    const v = showHistory ? "1" : "0";
    localStorage.setItem("gm.showHistory", v);
    SetPref("showHistory", v).catch(() => {});
  }, [showHistory]);
  // sidebarWidth: localStorage seeds first paint (avoids flash) and the
  // Go-backed prefs file is the source of truth across restarts. Read both
  // — file wins, then writes go to both.
  const [sidebarWidth, setSidebarWidth] = useState<number>(() => {
    const n = Number(localStorage.getItem("gm.sidebarWidth"));
    return Number.isFinite(n) && n >= 180 && n <= 600 ? n : 260;
  });
  useEffect(() => {
    GetPref("sidebarWidth")
      .then((v) => {
        const n = Number(v);
        if (Number.isFinite(n) && n >= 180 && n <= 600) setSidebarWidth(n);
      })
      .catch(() => {});
  }, []);
  useEffect(() => {
    localStorage.setItem("gm.sidebarWidth", String(sidebarWidth));
    SetPref("sidebarWidth", String(sidebarWidth)).catch(() => {});
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
  const [sessionFacets, setSessionFacets] =
    useState<main.SessionFacetCounts | null>(null);
  const [toolHits, setToolHits] = useState<main.ToolUseHit[]>([]);
  const [toolNameFilter, setToolNameFilter] = useState<string>(
    () => localStorage.getItem("gm.toolNameFilter") || "any",
  );
  const [toolUseFTSPre, setToolUseFTSPre] = useState(true);
  const [toolSelected, setToolSelected] = useState<main.ToolUseHit | null>(null);
  useEffect(() => {
    GetPref("toolNameFilter")
      .then((v) => {
        if (v) setToolNameFilter(v);
      })
      .catch(() => {});
  }, []);
  useEffect(() => {
    localStorage.setItem("gm.toolNameFilter", toolNameFilter);
    SetPref("toolNameFilter", toolNameFilter).catch(() => {});
  }, [toolNameFilter]);
  const [selSessionProject, setSelSessionProject] = useState("");
  const [selSessionDirType, setSelSessionDirType] = useState("");
  const [selSessionTopic, setSelSessionTopic] = useState("");
  const [dateRange, setDateRange] = useState<DateRange>(() => {
    try {
      const raw = localStorage.getItem("gm.dateRange");
      if (raw) return JSON.parse(raw) as DateRange;
    } catch {}
    return { preset: "all", from: "", to: "" };
  });
  const { since, until } = useMemo(() => rangeSinceUntil(dateRange), [dateRange]);
  useEffect(() => {
    GetPref("dateRange")
      .then((v) => {
        if (v) {
          try {
            setDateRange(JSON.parse(v) as DateRange);
          } catch {}
        }
      })
      .catch(() => {});
  }, []);
  useEffect(() => {
    const s = JSON.stringify(dateRange);
    localStorage.setItem("gm.dateRange", s);
    SetPref("dateRange", s).catch(() => {});
  }, [dateRange]);
  const [hybridRows, setHybridRows] = useState<search.HybridResult[]>([]);
  const [sessionHits, setSessionHits] = useState<search.Hit[]>([]);

  const [selection, setSelection] = useState<Selection>(null);
  const [detailArt, setDetailArt] = useState<artifacts.Artifact | null>(null);
  const [detailFile, setDetailFile] = useState<main.BrowseRow | null>(null);
  const [detailBody, setDetailBody] = useState<string>("");
  const [sessionLines, setSessionLines] = useState<SessionTurn[] | null>(null);
  const [turnFilter, setTurnFilter] = useState("");
  const [expandRev, setExpandRev] = useState(0);
  const [defaultExpanded, setDefaultExpanded] = useState(true);

  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);
  const [repoMenu, setRepoMenu] = useState<{
    repo: string;
    x: number;
    y: number;
    purgeDefault: boolean;
  } | null>(null);
  const [deleteRepo, setDeleteRepo] = useState<{
    repo: string;
    purgeDefault: boolean;
  } | null>(null);
  const [justRefreshed, setJustRefreshed] = useState(false);
  const [repoActivity, setRepoActivity] = useState<main.RepoActivity[]>([]);
  const [expandedWorktree, setExpandedWorktree] = useState<string | null>(null);
  const [expandedFiles, setExpandedFiles] = useState<main.FileActivity[]>([]);
  const [activityCounts, setActivityCounts] = useState<main.ActivityCounts | null>(null);
  const [activityFilter, setActivityFilter] = useState("");
  const [sparklines, setSparklines] = useState<Record<string, main.SparklinePoint[]>>({});
  const [heatmap, setHeatmap] = useState<main.HeatmapCell[]>([]);
  const [version, setVersion] = useState<string>("");
  const [aboutOpen, setAboutOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [uiZoom, setUiZoom] = useState<number>(() => {
    const n = Number(localStorage.getItem("gm.uiZoom"));
    return Number.isFinite(n) && n >= 80 && n <= 150 ? n : 100;
  });
  useEffect(() => {
    GetPref("uiZoom")
      .then((v) => {
        const n = Number(v);
        if (Number.isFinite(n) && n >= 80 && n <= 150) setUiZoom(n);
      })
      .catch(() => {});
  }, []);
  useEffect(() => {
    localStorage.setItem("gm.uiZoom", String(uiZoom));
    SetPref("uiZoom", String(uiZoom)).catch(() => {});
    // css zoom scales the whole app; supported by WKWebView
    (document.body.style as any).zoom = uiZoom === 100 ? "" : `${uiZoom}%`;
  }, [uiZoom]);
  const searchRef = useRef<HTMLInputElement>(null);
  const activityFilterRef = useRef<HTMLInputElement>(null);
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
    BrowseTree()
      .then((rows) => setBrowseRows(rows || []))
      .catch((e) => setErr(String(e)));
    SessionFacets()
      .then((sf) => setSessionFacets(sf))
      .catch((e) => setErr(String(e)));
    Version()
      .then((v: string) => setVersion(v))
      .catch(() => setVersion(""));
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

  // sparklines: fan out per visible repo after RecentRepos lands. Cached by
  // worktreePath so re-renders don't refetch. On reloadKey bump we refetch
  // in place (NOT wipe-then-fetch) — wiping caused a frame where rows had
  // no sparkline + a race when the daemon backfill bumped reloadKey faster
  // than the fetch could land.
  useEffect(() => {
    if (tab !== "activity") return;
    if (!repoActivity.length) return;
    const wanted = repoActivity.slice(0, 30).map((r) => r.worktreePath);
    let cancelled = false;
    Promise.all(
      wanted.map((wt) =>
        ProjectSparkline(wt, 7)
          .then((pts) => [wt, pts] as [string, main.SparklinePoint[]])
          .catch(() => [wt, [] as main.SparklinePoint[]] as [string, main.SparklinePoint[]]),
      ),
    ).then((pairs) => {
      if (cancelled) return;
      setSparklines((prev) => {
        const next = { ...prev };
        for (const [wt, pts] of pairs) next[wt] = pts;
        return next;
      });
    });
    return () => {
      cancelled = true;
    };
  }, [tab, repoActivity, reloadKey]);

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

  // browse mode (no query): compact grouped list from the tree data. Shared
  // context (repo · feature, or dir) lives in the group header once, not on
  // every row — the old flat ListArtifacts wall repeated it 80 times. Sidebar
  // filter (text + ext:md) and the topbar date range both apply here, so
  // "md files touched in the last 15 min for repo X" is: pick repo, 15m
  // preset, type ext:md.
  const sbFilter = useMemo(() => parseSidebarFilter(sidebarFilter), [sidebarFilter]);
  const browseGroups = useMemo(() => {
    const { from, to } = rangeToMs(dateRange);
    let rows = browseRows.filter((r) => {
      if (!showHistory && r.type === "history") return false;
      const ms = r.mtime * 1000;
      if (ms < from || ms >= to) return false;
      return rowMatchesFilter(r, sbFilter);
    });
    if (selRepo) rows = rows.filter((r) => r.repo === selRepo);
    if (selFeature) rows = rows.filter((r) => r.feature === selFeature);
    if (!selRepo && !selFeature) {
      // recent across everything: bucket by repo · feature, freshest bucket
      // first, newest file first inside, 300-row cap
      const recent = [...rows].sort((a, b) => b.mtime - a.mtime).slice(0, 300);
      const buckets = new Map<string, { dir: string; files: main.BrowseRow[] }>();
      for (const r of recent) {
        const key = `${r.repo} ${r.feature}`;
        let b = buckets.get(key);
        if (!b) {
          b = { dir: r.feature ? `${r.repo} · ${r.feature}` : r.repo, files: [] };
          buckets.set(key, b);
        }
        b.files.push(r);
      }
      return [...buckets.values()];
    }
    const byDir = new Map<string, main.BrowseRow[]>();
    for (const r of rows) {
      let rel = r.rel;
      if (selFeature && rel.startsWith(`features/${r.feature}/`)) {
        rel = rel.slice(`features/${r.feature}/`.length);
      }
      const dir = rel.includes("/") ? rel.slice(0, rel.lastIndexOf("/") + 1) : "./";
      const list = byDir.get(dir);
      if (list) list.push(r);
      else byDir.set(dir, [r]);
    }
    return [...byDir.entries()]
      .sort((a, b) => a[0].localeCompare(b[0]))
      .map(([dir, files]) => ({
        dir,
        files: [...files].sort((a, b) => a.rel.localeCompare(b.rel)),
      }));
  }, [browseRows, selRepo, selFeature, sbFilter, dateRange, showHistory, reloadKey]);
  const browsePaths = useMemo(
    () => browseGroups.flatMap((g) => g.files.map((f) => f.path)),
    [browseGroups],
  );

  // ordered ids list for j/k nav
  const visibleRowIDs: string[] = useMemo(() => {
    if (tab === "artifacts") {
      return hybridRows.length
        ? hybridRows.map((h) => h.artifact.id)
        : browsePaths;
    }
    return sessionHits.map((h) => h.filepath);
  }, [tab, hybridRows, browsePaths, sessionHits]);

  // keyboard nav: j/k move row, esc clear, / focus search
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName;
      const inField = tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
      if (e.key === "/" && !inField) {
        e.preventDefault();
        // on activity tab the topbar FTS search doesn't apply — the
        // activity-sidebar project filter is what the user actually wants.
        const target =
          tab === "activity"
            ? activityFilterRef.current || searchRef.current
            : searchRef.current;
        target?.focus();
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
          : selection?.kind === "file" || selection?.kind === "session"
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
          ? hybridRows.length
            ? { kind: "artifact", id }
            : { kind: "file", path: id }
          : { kind: "session", path: id },
      );
      e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [visibleRowIDs, selection, tab, hybridRows, refreshAll]);

  useEffect(() => {
    const t = setTimeout(() => setDebouncedQuery(query), 200);
    return () => clearTimeout(t);
  }, [query]);

  const filter = useMemo<artifacts.ListFilter>(
    () => ({
      Type: [],
      Status: [],
      Lifecycle: [],
      Scope: "",
      Repo: selRepo,
      Branch: "",
      Feature: selFeature,
      Domain: "",
    }),
    [selFeature, selRepo],
  );

  // refetch results when inputs change
  useEffect(() => {
    setErr(null);
    setLoading(true);
    const run = async () => {
      try {
        if (tab === "artifacts") {
          if (debouncedQuery.trim()) {
            const r = await SearchHybrid(debouncedQuery, filter, 100, since, until);
            setHybridRows(r || []);
          } else {
            // browse mode renders from browseRows client-side
            setHybridRows([]);
          }
        } else if (tab === "tools") {
          const f: main.ToolUseFilter = {
            query: debouncedQuery,
            toolName: toolNameFilter === "any" ? "" : toolNameFilter,
            project: "",
            useFTSPre: toolUseFTSPre,
            since,
            until,
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
            since,
            until,
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
              Since: since,
              Until: until,
              Limit: 100,
              IncludeFull: true,
            };
            hits = (await SearchFTS(params)) || [];
            // client-side topic filter for FTS path since search.Run doesn't
            // know our topic dimension; date range is handled server-side
            if (selSessionTopic) {
              hits = hits.filter((h) => h.topic === selSessionTopic);
            }
            hits = [...hits].sort((a, b) =>
              (b.timestamp || "").localeCompare(a.timestamp || ""),
            );
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
    filter,
    selSessionProject,
    selSessionDirType,
    selSessionTopic,
    since,
    until,
    toolNameFilter,
    toolUseFTSPre,
    reloadKey,
  ]);

  // load detail when selection changes
  useEffect(() => {
    if (!selection) {
      setDetailArt(null);
      setDetailFile(null);
      setDetailBody("");
      setSessionLines(null);
      return;
    }
    if (selection.kind === "artifact") {
      const row =
        hybridRows.find((h) => h.artifact?.id === selection.id)?.artifact ||
        null;
      setDetailArt(row);
      setDetailFile(null);
      setSessionLines(null);
      GetArtifactBody(selection.id)
        .then(setDetailBody)
        .catch((e: unknown) => setDetailBody(`# error\n\n${String(e)}`));
    } else if (selection.kind === "file") {
      setDetailArt(null);
      setSessionLines(null);
      setDetailFile(browseRows.find((r) => r.path === selection.path) || null);
      GetLiveBody(selection.path)
        .then(setDetailBody)
        .catch((e: unknown) => setDetailBody(`# error\n\n${String(e)}`));
    } else {
      setDetailArt(null);
      setDetailFile(null);
      setDetailBody("");
      ReadFile(selection.path)
        .then((raw: string) => setSessionLines(parseJSONL(raw)))
        .catch((e: unknown) => {
          setSessionLines(null);
          setErr(String(e));
        });
    }
  }, [selection, hybridRows, browseRows, reloadKey]);

  const clearFacets = () => {
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
      ? hybridRows.length || browsePaths.length
      : tab === "tools"
        ? toolHits.length
        : sessionHits.length;

  const trimmedQuery = query.trim();
  const queryActive = trimmedQuery.length > 0;
  const sessionFilterCount =
    (selSessionProject ? 1 : 0) +
    (selSessionDirType ? 1 : 0) +
    (selSessionTopic ? 1 : 0) +
    (tab === "sessions" && queryActive ? 1 : 0);
  const artifactFilterCount =
    (selFeature ? 1 : 0) +
    (selRepo ? 1 : 0) +
    (tab === "artifacts" && queryActive ? 1 : 0);
  const toolFilterCount =
    (tab === "tools" && queryActive ? 1 : 0) +
    (tab === "tools" && toolNameFilter !== "any" ? 1 : 0);
  const activeFilterCount =
    tab === "sessions"
      ? sessionFilterCount
      : tab === "artifacts"
        ? artifactFilterCount
        : tab === "tools"
          ? toolFilterCount
          : 0;
  const hasActiveFilters = activeFilterCount > 0;

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
            {sessionFilterCount > 0 && (
              <span className="tab-filter-badge" title={`${sessionFilterCount} filter(s) active`}>
                {sessionFilterCount}
              </span>
            )}
          </button>
          <button
            className={tab === "artifacts" ? "active" : ""}
            onClick={() => {
              setTab("artifacts");
              setSelection(null);
            }}
          >
            artifacts
            {artifactFilterCount > 0 && (
              <span className="tab-filter-badge" title={`${artifactFilterCount} filter(s) active`}>
                {artifactFilterCount}
              </span>
            )}
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
        {tab !== "activity" && (
          <div className="date-range" title="filter results by date range">
            <select
              value={dateRange.preset}
              onChange={(e) =>
                setDateRange((r) => ({ ...r, preset: e.target.value }))
              }
            >
              {DATE_PRESETS.map((p) => (
                <option key={p.value} value={p.value}>
                  {p.label}
                </option>
              ))}
            </select>
            {dateRange.preset === "custom" && (
              <>
                <input
                  type="date"
                  value={dateRange.from}
                  max={dateRange.to || undefined}
                  onChange={(e) =>
                    setDateRange((r) => ({ ...r, from: e.target.value }))
                  }
                  title="from (inclusive)"
                />
                <span className="date-range-sep">→</span>
                <input
                  type="date"
                  value={dateRange.to}
                  min={dateRange.from || undefined}
                  onChange={(e) =>
                    setDateRange((r) => ({ ...r, to: e.target.value }))
                  }
                  title="to (inclusive)"
                />
              </>
            )}
          </div>
        )}
        <button
          className={`refresh-btn ${loading ? "spinning" : ""} ${justRefreshed ? "flash" : ""}`}
          onClick={refreshAll}
          disabled={loading}
          title="refresh results (r)"
          aria-label="refresh"
        >
          ⟳
        </button>
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
                fontSize: 13,
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

      <div
        className={`filter-chips${hasActiveFilters ? " has-filters" : ""}`}
        ref={chipsRef}
      >
        {hasActiveFilters && (
          <span className="filter-active-label">
            {activeFilterCount} filter{activeFilterCount === 1 ? "" : "s"} active:
          </span>
        )}
        {queryActive && (
          <span className="filter-chip" onClick={() => setQuery("")}>
            search: {trimmedQuery} <span className="x">×</span>
          </span>
        )}
        {tab === "sessions" &&
          (!!selSessionProject ||
            !!selSessionDirType ||
            !!selSessionTopic) && (
            <>
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
            </>
          )}
        {tab === "artifacts" &&
          (selFeature ? 1 : 0) + (selRepo ? 1 : 0) > 0 && (
            <>
              {selFeature && (
                <span
                  className="filter-chip"
                  onClick={() => setSelFeature("")}
                >
                  feature:{" "}
                  {selRepo ? `${selRepo}/${selFeature}` : selFeature}{" "}
                  <span className="x">×</span>
                </span>
              )}
              {selRepo && (
                <span
                  className="filter-chip"
                  onClick={() => setSelRepo("")}
                >
                  repo: {selRepo} <span className="x">×</span>
                </span>
              )}
            </>
          )}
        {tab === "tools" && toolNameFilter !== "any" && (
          <span
            className="filter-chip"
            onClick={() => setToolNameFilter("any")}
          >
            tool: {toolNameFilter} <span className="x">×</span>
          </span>
        )}
        {hasActiveFilters && (
          <span
            className="filter-chip"
            onClick={() => {
              setQuery("");
              if (tab === "sessions") {
                setSelSessionProject("");
                setSelSessionDirType("");
                setSelSessionTopic("");
              } else if (tab === "artifacts") {
                clearFacets();
              } else if (tab === "tools") {
                setToolNameFilter("any");
              }
            }}
            style={{ color: "var(--fg-muted)" }}
          >
            clear all
          </span>
        )}
        {!hasActiveFilters && (
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
        {tab === "artifacts" && (
          <>
            <div className="sidebar-filter">
              <input
                type="search"
                placeholder="filter files… (text, ext:md)"
                value={sidebarFilter}
                onChange={(e) => setSidebarFilter(e.target.value)}
              />
            </div>
            <div className="browse-toggles">
              <button
                className={`toggle-pill ${showHistory ? "on" : ""}`}
                onClick={() => setShowHistory((v) => !v)}
                title="session turn-set summaries (history/) in the file list"
              >
                history {showHistory ? "on" : "off"}
              </button>
            </div>
            <TreeSidebar
              rows={browseRows}
              filter={sidebarFilter}
              expanded={treeExpanded}
              onToggle={(key) =>
                setTreeExpanded((prev) => {
                  const next = new Set(prev);
                  if (next.has(key)) next.delete(key);
                  else next.add(key);
                  return next;
                })
              }
              selRepo={selRepo}
              selFeature={selFeature}
              selection={selection}
              onPickRepo={(repo) => {
                setSelFeature("");
                setSelRepo(selRepo === repo && !selFeature ? "" : repo);
              }}
              onPickFeature={(repo, feature) => {
                if (selRepo === repo && selFeature === feature) {
                  setSelFeature("");
                } else {
                  setSelRepo(repo);
                  setSelFeature(feature);
                }
              }}
              onPickFile={(path) => setSelection({ kind: "file", path })}
              onRepoMenu={(repo, x, y) =>
                setRepoMenu({ repo, x, y, purgeDefault: false })
              }
            />
            {(!!selFeature || !!selRepo) && (
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
            filterRef={activityFilterRef}
          />
        )}
        {tab === "tools" && (
          <div style={{ color: "var(--fg-muted)", fontSize: 13 }}>
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
              title="project"
              counts={sessionFacets.byProject || {}}
              selected={selSessionProject}
              onPick={setSelSessionProject}
              filter={sidebarFilter}
              isCollapsed={collapsed.has("project")}
              onToggleCollapse={() => toggleCollapsed("project")}
              onItemMenu={(v, x, y) =>
                setRepoMenu({ repo: v, x, y, purgeDefault: true })
              }
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
            {(selSessionProject ||
              selSessionDirType ||
              selSessionTopic) && (
              <button
                className="facet-clear"
                onClick={() => {
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
        {tab === "artifacts" && hybridRows.length === 0 && (
          <BrowseList
            groups={browseGroups}
            grouped={!!selRepo || !!selFeature}
            selPath={selection?.kind === "file" ? selection.path : ""}
            onPick={(path) => setSelection({ kind: "file", path })}
          />
        )}
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
            onRepoMenu={(repo, x, y) =>
              setRepoMenu({ repo, x, y, purgeDefault: false })
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
        {selection?.kind === "file" && (
          <FileDetail
            path={selection.path}
            row={detailFile}
            body={detailBody}
            onOpenSession={async (sid) => {
              try {
                const p = await SessionPathByID(sid);
                setTab("sessions");
                setSelection({ kind: "session", path: p });
              } catch (e) {
                setErr(String(e));
              }
            }}
          />
        )}
        {selection?.kind === "session" && (
          <SessionDetail
            path={selection.path}
            turns={sessionLines}
            filter={turnFilter}
            onFilterChange={setTurnFilter}
            files={browseRows.filter(
              (r) =>
                r.sessionId &&
                r.sessionId === sessionIDFromPath(selection.path),
            )}
            onOpenFile={(path) => {
              setTab("artifacts");
              setSelection({ kind: "file", path });
            }}
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
        <span className="status-spacer" />
        <button
          type="button"
          className="status-about"
          onClick={() => setSettingsOpen(true)}
          title="ui settings"
        >
          settings
        </button>
        <button
          type="button"
          className="status-about"
          onClick={() => setAboutOpen(true)}
          title="about giantmem"
        >
          about
        </button>
        {version && (
          <span className="status-version" title="giantmem GUI version">
            v{version}
          </span>
        )}
      </div>
      {aboutOpen && (
        <AboutModal version={version} onClose={() => setAboutOpen(false)} />
      )}
      {settingsOpen && (
        <SettingsModal
          uiZoom={uiZoom}
          onZoom={setUiZoom}
          showHistory={showHistory}
          onShowHistory={setShowHistory}
          onClose={() => setSettingsOpen(false)}
        />
      )}
      {repoMenu && (
        <RepoContextMenu
          menu={repoMenu}
          onClose={() => setRepoMenu(null)}
          onDelete={() => {
            setDeleteRepo({
              repo: repoMenu.repo,
              purgeDefault: repoMenu.purgeDefault,
            });
            setRepoMenu(null);
          }}
        />
      )}
      {deleteRepo && (
        <ConfirmDeleteModal
          repo={deleteRepo.repo}
          fileCount={browseRows.filter((r) => r.repo === deleteRepo.repo).length}
          sessionCount={sessionFacets?.byProject?.[deleteRepo.repo] || 0}
          purgeDefault={deleteRepo.purgeDefault}
          onCancel={() => setDeleteRepo(null)}
          onConfirm={(purge) => {
            DeleteProject(deleteRepo.repo, purge)
              .then(() => {
                if (selRepo === deleteRepo.repo) {
                  setSelRepo("");
                  setSelFeature("");
                }
                if (selSessionProject === deleteRepo.repo) {
                  setSelSessionProject("");
                }
                setDeleteRepo(null);
                refreshAll();
              })
              .catch((e) => {
                setDeleteRepo(null);
                setErr(String(e));
              });
          }}
        />
      )}
    </div>
  );
}

function RepoContextMenu({
  menu,
  onClose,
  onDelete,
}: {
  menu: { repo: string; x: number; y: number };
  onClose: () => void;
  onDelete: () => void;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  // keep the menu on-screen when the click lands near the bottom edge
  const top = Math.min(menu.y, window.innerHeight - 80);
  return (
    <div className="ctx-backdrop" onClick={onClose} onContextMenu={(e) => { e.preventDefault(); onClose(); }}>
      <div className="ctx-menu" style={{ left: menu.x, top }} onClick={(e) => e.stopPropagation()}>
        <div className="ctx-title" title={menu.repo}>{menu.repo}</div>
        <button type="button" className="ctx-item danger" onClick={onDelete}>
          Delete from index…
        </button>
      </div>
    </div>
  );
}

function ConfirmDeleteModal({
  repo,
  fileCount,
  sessionCount,
  purgeDefault,
  onCancel,
  onConfirm,
}: {
  repo: string;
  fileCount: number;
  sessionCount: number;
  purgeDefault: boolean;
  onCancel: () => void;
  onConfirm: (purgeArchive: boolean) => void;
}) {
  const [purge, setPurge] = useState(purgeDefault);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const k = e.key.toLowerCase();
      if (k === "y") onConfirm(purge);
      if (k === "n" || e.key === "Escape") onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onConfirm, onCancel, purge]);
  return (
    <div className="modal-backdrop" onClick={onCancel}>
      <div
        className="modal confirm-modal"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label="Confirm delete"
      >
        <h2 className="settings-title">Delete project?</h2>
        <p className="confirm-text">
          Remove <strong>{repo}</strong> ({fileCount} indexed{" "}
          {fileCount === 1 ? "file" : "files"}) from the live index.
        </p>
        <div className="settings-row confirm-purge">
          <div className="settings-info">
            <div className="settings-label">Also delete archived history</div>
            <div className="settings-desc">
              {sessionCount} archived session{" "}
              {sessionCount === 1 ? "doc" : "docs"} — this is what the sessions
              tab lists. Off keeps them searchable.
            </div>
          </div>
          <Switch checked={purge} onChange={setPurge} />
        </div>
        <div className="confirm-actions">
          <button type="button" onClick={onCancel}>
            Cancel <span className="key-hint">n</span>
          </button>
          <button type="button" className="danger" onClick={() => onConfirm(purge)}>
            Delete <span className="key-hint">y</span>
          </button>
        </div>
      </div>
    </div>
  );
}

// text size is a scale, not an absolute px — the UI mixes 11-28px on
// purpose (hierarchy), so one fixed size would flatten it. Each step
// scales everything proportionally.
const TEXT_SIZES: { label: string; zoom: number }[] = [
  { label: "Small", zoom: 90 },
  { label: "Default", zoom: 100 },
  { label: "Large", zoom: 110 },
  { label: "X-Large", zoom: 120 },
  { label: "Huge", zoom: 135 },
];

function Switch({
  checked,
  onChange,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      className={`switch ${checked ? "on" : ""}`}
      onClick={() => onChange(!checked)}
    >
      <span className="switch-knob" />
    </button>
  );
}

function SettingsModal({
  uiZoom,
  onZoom,
  showHistory,
  onShowHistory,
  onClose,
}: {
  uiZoom: number;
  onZoom: (n: number) => void;
  showHistory: boolean;
  onShowHistory: (v: boolean) => void;
  onClose: () => void;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="modal settings-modal"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label="Settings"
      >
        <button
          type="button"
          className="modal-close"
          onClick={onClose}
          aria-label="close"
        >
          ×
        </button>
        <h2 className="settings-title">Settings</h2>

        <div className="settings-section">Appearance</div>
        <div className="settings-row">
          <div className="settings-info">
            <div className="settings-label">Text size</div>
            <div className="settings-desc">
              Scales the whole UI proportionally — element sizes differ by
              design, so there's no single font size to set.
            </div>
          </div>
          <div className="segmented">
            {TEXT_SIZES.map((s) => (
              <button
                key={s.zoom}
                type="button"
                className={uiZoom === s.zoom ? "active" : ""}
                onClick={() => onZoom(s.zoom)}
                title={`${s.zoom}%`}
              >
                {s.label}
              </button>
            ))}
          </div>
        </div>

        <div className="settings-section">File list</div>
        <div className="settings-row">
          <div className="settings-info">
            <div className="settings-label">Show history docs</div>
            <div className="settings-desc">
              Per-session turn-set summaries (history/). Every session bumps
              them, so hiding them keeps the recent view to real work docs.
            </div>
          </div>
          <Switch checked={showHistory} onChange={onShowHistory} />
        </div>
      </div>
    </div>
  );
}

function AboutModal({
  version,
  onClose,
}: {
  version: string;
  onClose: () => void;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  const repoURL = "https://github.com/bearded-giant/giant-tooling";
  const siteURL = "https://beardedgiantllc.com";
  const openLink = (e: React.MouseEvent, url: string) => {
    e.preventDefault();
    BrowserOpenURL(url);
  };
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="modal about-modal"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label="About Giantmem"
      >
        <button
          type="button"
          className="modal-close"
          onClick={onClose}
          aria-label="close"
        >
          ×
        </button>
        <h1 className="about-title">Giantmem</h1>
        {version && <div className="about-version">v{version}</div>}
        <div className="about-tagline">Built by Bearded Giant</div>
        <div className="about-links">
          <a href={repoURL} onClick={(e) => openLink(e, repoURL)}>
            {repoURL.replace(/^https?:\/\//, "")}
          </a>
          <a href={siteURL} onClick={(e) => openLink(e, siteURL)}>
            beardedgiantllc.com
          </a>
        </div>
      </div>
    </div>
  );
}

// ----- tree sidebar ---------------------------------------------------------

type TreeDir = {
  dirs: Map<string, TreeDir>;
  files: main.BrowseRow[];
};

type RepoNode = {
  repo: string;
  dead: boolean;
  features: Map<string, TreeDir>; // "" key = repo-level docs
  count: number;
};

function newDir(): TreeDir {
  return { dirs: new Map(), files: [] };
}

function insertPath(root: TreeDir, segs: string[], row: main.BrowseRow) {
  let cur = root;
  for (let i = 0; i < segs.length - 1; i++) {
    let next = cur.dirs.get(segs[i]);
    if (!next) {
      next = newDir();
      cur.dirs.set(segs[i], next);
    }
    cur = next;
  }
  cur.files.push(row);
}

function sessionIDFromPath(p: string): string {
  const stem = p.split("/").pop() || "";
  return stem.replace(/\.jsonl$/, "");
}

function TreeSidebar({
  rows,
  filter,
  expanded,
  onToggle,
  selRepo,
  selFeature,
  selection,
  onPickRepo,
  onPickFeature,
  onPickFile,
  onRepoMenu,
}: {
  rows: main.BrowseRow[];
  filter: string;
  expanded: Set<string>;
  onToggle: (key: string) => void;
  selRepo: string;
  selFeature: string;
  selection: Selection;
  onPickRepo: (repo: string) => void;
  onPickFeature: (repo: string, feature: string) => void;
  onPickFile: (path: string) => void;
  onRepoMenu: (repo: string, x: number, y: number) => void;
}) {
  const f = useMemo(() => parseSidebarFilter(filter), [filter]);
  const q = f.text || f.ext;
  const repos = useMemo(() => {
    const byRepo = new Map<string, RepoNode>();
    for (const r of rows) {
      if (!rowMatchesFilter(r, f)) {
        continue;
      }
      let node = byRepo.get(r.repo);
      if (!node) {
        node = { repo: r.repo, dead: true, features: new Map(), count: 0 };
        byRepo.set(r.repo, node);
      }
      node.count++;
      if (!r.dead) node.dead = false;
      let root = node.features.get(r.feature);
      if (!root) {
        root = newDir();
        node.features.set(r.feature, root);
      }
      // strip the features/{name}/ prefix so the feature node shows its own
      // subtree, not the fixed features/ scaffolding
      let rel = r.rel;
      if (r.feature && rel.startsWith(`features/${r.feature}/`)) {
        rel = rel.slice(`features/${r.feature}/`.length);
      }
      insertPath(root, rel.split("/"), r);
    }
    return [...byRepo.values()].sort((a, b) => a.repo.localeCompare(b.repo));
  }, [rows, f]);

  // filtering auto-expands everything that survived; manual expand state
  // drives the unfiltered browse
  const isOpen = (key: string) => (q ? true : expanded.has(key));

  const selPath = selection?.kind === "file" ? selection.path : "";

  return (
    <div className="tree">
      {repos.map((node) => {
        const rkey = `r:${node.repo}`;
        const featureNames = [...node.features.keys()].sort((a, b) => {
          if (a === "") return 1; // repo-level docs after features
          if (b === "") return -1;
          return a.localeCompare(b);
        });
        return (
          <div key={rkey}>
            <div
              className={`tree-row repo ${selRepo === node.repo && !selFeature ? "selected" : ""}`}
              onClick={() => onToggle(rkey)}
              onContextMenu={(e) => {
                e.preventDefault();
                onRepoMenu(node.repo, e.clientX, e.clientY);
              }}
            >
              <span className="facet-arrow">{isOpen(rkey) ? "▾" : "▸"}</span>
              <span
                className="tree-label"
                onClick={(e) => {
                  e.stopPropagation();
                  onPickRepo(node.repo);
                  if (!isOpen(rkey)) onToggle(rkey);
                }}
                title={node.repo}
              >
                {node.repo}
              </span>
              {node.dead && <span className="chip dead">gone</span>}
              <span className="count">{node.count}</span>
            </div>
            {isOpen(rkey) &&
              featureNames.map((f) => {
                const fkey = `${rkey}/f:${f}`;
                const root = node.features.get(f)!;
                return (
                  <div key={fkey} className="tree-indent">
                    <div
                      className={`tree-row feature ${
                        selRepo === node.repo && selFeature === f && f !== ""
                          ? "selected"
                          : ""
                      }`}
                      onClick={() => onToggle(fkey)}
                    >
                      <span className="facet-arrow">
                        {isOpen(fkey) ? "▾" : "▸"}
                      </span>
                      <span
                        className="tree-label"
                        onClick={(e) => {
                          e.stopPropagation();
                          if (f !== "") onPickFeature(node.repo, f);
                          if (!isOpen(fkey)) onToggle(fkey);
                        }}
                      >
                        {f === "" ? "(repo docs)" : f}
                      </span>
                    </div>
                    {isOpen(fkey) && (
                      <TreeDirView
                        dir={root}
                        keyPrefix={fkey}
                        isOpen={isOpen}
                        onToggle={onToggle}
                        onPickFile={onPickFile}
                        selPath={selPath}
                      />
                    )}
                  </div>
                );
              })}
          </div>
        );
      })}
      {repos.length === 0 && (
        <div style={{ color: "var(--fg-muted)", fontSize: 13 }}>
          {q ? "nothing matches" : "no indexed files"}
        </div>
      )}
    </div>
  );
}

function TreeDirView({
  dir,
  keyPrefix,
  isOpen,
  onToggle,
  onPickFile,
  selPath,
}: {
  dir: TreeDir;
  keyPrefix: string;
  isOpen: (key: string) => boolean;
  onToggle: (key: string) => void;
  onPickFile: (path: string) => void;
  selPath: string;
}) {
  const dirNames = [...dir.dirs.keys()].sort();
  const files = [...dir.files].sort((a, b) => a.rel.localeCompare(b.rel));
  return (
    <div className="tree-indent">
      {dirNames.map((d) => {
        const dkey = `${keyPrefix}/d:${d}`;
        return (
          <div key={dkey}>
            <div className="tree-row dir" onClick={() => onToggle(dkey)}>
              <span className="facet-arrow">{isOpen(dkey) ? "▾" : "▸"}</span>
              <span className="tree-label">{d}/</span>
            </div>
            {isOpen(dkey) && (
              <TreeDirView
                dir={dir.dirs.get(d)!}
                keyPrefix={dkey}
                isOpen={isOpen}
                onToggle={onToggle}
                onPickFile={onPickFile}
                selPath={selPath}
              />
            )}
          </div>
        );
      })}
      {files.map((f) => (
        <div
          key={f.path}
          className={`tree-row file ${selPath === f.path ? "selected" : ""}`}
          onClick={() => onPickFile(f.path)}
          title={f.path}
        >
          <span className="tree-label">{f.rel.split("/").pop()}</span>
          {f.type && <span className={`chip type-${f.type}`}>{f.type}</span>}
        </div>
      ))}
    </div>
  );
}

// ----- file detail ----------------------------------------------------------

// parseCSV: minimal RFC-4180 — quoted fields, embedded commas/newlines.
function parseCSV(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let cur = "";
  let inQ = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (inQ) {
      if (c === '"') {
        if (text[i + 1] === '"') {
          cur += '"';
          i++;
        } else {
          inQ = false;
        }
      } else {
        cur += c;
      }
    } else if (c === '"') {
      inQ = true;
    } else if (c === ",") {
      row.push(cur);
      cur = "";
    } else if (c === "\n" || c === "\r") {
      if (c === "\r" && text[i + 1] === "\n") i++;
      row.push(cur);
      cur = "";
      if (row.some((v) => v !== "")) rows.push(row);
      row = [];
    } else {
      cur += c;
    }
  }
  if (cur !== "" || row.length) {
    row.push(cur);
    if (row.some((v) => v !== "")) rows.push(row);
  }
  return rows;
}

function FileDetail({
  path,
  row,
  body,
  onOpenSession,
}: {
  path: string;
  row: main.BrowseRow | null;
  body: string;
  onOpenSession: (sessionID: string) => void;
}) {
  const name = path.split("/").pop() || path;
  const ext = name.includes(".") ? name.split(".").pop()!.toLowerCase() : "";
  const csvRows = useMemo(
    () => (ext === "csv" ? parseCSV(body).slice(0, 500) : null),
    [ext, body],
  );
  return (
    <>
      <header className="detail-head">
        <h1 style={{ marginBottom: 0 }}>{name}</h1>
        <div className="meta">
          {row?.type && <span className={`chip type-${row.type}`}>{row.type}</span>}
          {row?.dead && <span className="chip dead">worktree gone</span>}
          {row?.repo && <span>repo: {row.repo}</span>}
          {row?.feature && <span>feature: {row.feature}</span>}
          {row?.mtime ? <span>updated: {formatTime(new Date(row.mtime * 1000).toISOString())}</span> : null}
          {row?.sessionId && (
            <a
              onClick={() => onOpenSession(row.sessionId)}
              style={{ color: "var(--accent)", cursor: "pointer" }}
              title={`open session ${row.sessionId}`}
            >
              session {row.sessionId.slice(0, 8)} ›
            </a>
          )}
          <span style={{ fontFamily: "ui-monospace", opacity: 0.7 }}>{path}</span>
          <CopyButton text={path} label="copy absolute path" />
        </div>
      </header>
      {ext === "md" || ext === "mmd" ? (
        <ReactMarkdown
          remarkPlugins={[remarkGfm]}
          rehypePlugins={[rehypeHighlight as any]}
        >
          {body}
        </ReactMarkdown>
      ) : csvRows ? (
        <div className="csv-wrap">
          <table className="csv-table">
            <thead>
              <tr>
                {(csvRows[0] || []).map((h, i) => (
                  <th key={i}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {csvRows.slice(1).map((r, i) => (
                <tr key={i}>
                  {r.map((v, j) => (
                    <td key={j}>{v}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <pre className="raw-body">{body}</pre>
      )}
    </>
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

function SingleFacetGroup({
  title,
  counts,
  selected,
  onPick,
  minCount = 0,
  filter = "",
  isCollapsed = false,
  onToggleCollapse,
  onItemMenu,
}: {
  title: string;
  counts: Record<string, number>;
  selected: string;
  onPick: (v: string) => void;
  minCount?: number;
  filter?: string;
  isCollapsed?: boolean;
  onToggleCollapse?: () => void;
  onItemMenu?: (v: string, x: number, y: number) => void;
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
            onContextMenu={
              onItemMenu
                ? (e) => {
                    e.preventDefault();
                    onItemMenu(v, e.clientX, e.clientY);
                  }
                : undefined
            }
          >
            <span>{v}</span>
            <span className="count">{n}</span>
          </div>
        ))}
    </div>
  );
}

// BrowseList: compact rows for browse mode. Every group carries the shared
// context in its header exactly once — dir path when a repo/feature is
// picked, repo · feature bucket in the recent view — so rows are just
// name + type + time.
function BrowseList({
  groups,
  grouped,
  selPath,
  onPick,
}: {
  groups: { dir: string; files: main.BrowseRow[] }[];
  grouped: boolean;
  selPath: string;
  onPick: (path: string) => void;
}) {
  if (groups.length === 0) {
    return null;
  }
  return (
    <>
      {groups.map((g) => (
        <div key={g.dir || "(recent)"}>
          <div className="browse-group-head">{g.dir}</div>
          {g.files.map((f) => (
            <div
              key={f.path}
              className={`browse-row ${selPath === f.path ? "selected" : ""}`}
              onClick={() => onPick(f.path)}
              title={f.path}
            >
              {f.type && <span className={`chip type-${f.type}`}>{f.type}</span>}
              <span className="browse-name">
                {grouped ? f.rel.split("/").pop() : f.rel}
              </span>
              {f.dead && <span className="chip dead">gone</span>}
              <span className="browse-meta">{formatMtime(f.mtime)}</span>
            </div>
          ))}
        </div>
      ))}
    </>
  );
}

// formatMtime: today -> HH:MM, else date. Recent view is about "what just
// happened", a bare date can't answer that.
function formatMtime(mtime: number): string {
  const d = new Date(mtime * 1000);
  const now = new Date();
  if (d.toDateString() === now.toDateString()) {
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  return d.toLocaleDateString();
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
              fontSize: 13,
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
  onRepoMenu,
}: {
  repos: main.RepoActivity[];
  expandedWorktree: string | null;
  expandedFiles: main.FileActivity[];
  sparklines: Record<string, main.SparklinePoint[]>;
  onToggle: (worktree: string) => void;
  onRepoMenu: (repo: string, x: number, y: number) => void;
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
              onContextMenu={(e) => {
                e.preventDefault();
                onRepoMenu(r.project, e.clientX, e.clientY);
              }}
              style={{ cursor: "pointer", display: "flex", alignItems: "center", gap: 10 }}
            >
              <div style={{ flex: 1, minWidth: 0 }}>
                <div className="row-head">
                  <span className="chip">{isOpen ? "▾" : "▸"}</span>
                  <span className="row-title">{r.project}</span>
                </div>
                <div className="row-meta">
                  {r.docCount} doc{r.docCount === 1 ? "" : "s"} · {r.worktreePath}
                </div>
              </div>
              {/* fixed-width sparkline slot so age stays aligned even when
                  data hasn't loaded yet for a worktree */}
              <div style={{ width: 84, flexShrink: 0 }}>
                {spark.length > 0 && <Sparkline points={spark} />}
              </div>
              <div
                className="row-meta"
                style={{ width: 36, textAlign: "right", flexShrink: 0 }}
              >
                {ago(r.mtime)}
              </div>
            </div>
            {isOpen && (
              <div style={{ marginTop: 8, paddingLeft: 12, borderLeft: "2px solid var(--border)" }}>
                {expandedFiles.length === 0 && (
                  <div className="row-meta">loading…</div>
                )}
                {expandedFiles.map((f) => (
                  <div key={f.path} style={{ padding: "3px 0", fontSize: 13 }}>
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
  filterRef,
}: {
  counts: main.ActivityCounts | null;
  filter: string;
  onFilter: (v: string) => void;
  heatmap: main.HeatmapCell[];
  filterRef?: React.RefObject<HTMLInputElement>;
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
          ref={filterRef}
          type="search"
          placeholder="filter projects… (/)"
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
      <div style={{ fontSize: 12, color: "var(--fg-muted)" }}>{label}</div>
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
          fontSize: 13,
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
                fontSize: 13,
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
              {info.days.map((c) => (
                <div
                  key={c.day}
                  title={`${c.day}: ${c.count}`}
                  style={{
                    width: 9,
                    height: 9,
                    background: heatColor(c.count, max),
                    border: "1px solid var(--border)",
                  }}
                />
              ))}
            </div>
          </div>
        ))}
      </div>
      {/* gradient legend — 5 swatches from empty to max so users can map
          color back to count. tooltip on each shows the bucket. */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 4,
          marginTop: 8,
          fontSize: 12,
          color: "var(--fg-muted)",
        }}
      >
        <span>0</span>
        <div style={{ display: "flex", gap: 1 }}>
          {[0, 0.25, 0.5, 0.75, 1].map((f) => {
            const n = Math.round(f * max);
            return (
              <div
                key={f}
                title={`${n}`}
                style={{
                  width: 12,
                  height: 9,
                  background: heatColor(n, max),
                  border: "1px solid var(--border)",
                }}
              />
            );
          })}
        </div>
        <span>{max}+</span>
      </div>
    </div>
  );
}

// heatColor returns the cell background for a (count, panel-max) pair.
// 0 = subtle bg-3, anything else fades from low-intensity accent up to
// solid white at the panel max. Single helper so the row cells + legend
// stay perfectly in sync.
function heatColor(count: number, max: number): string {
  if (count <= 0) return "var(--bg-3)";
  const f = Math.min(1, count / max);
  if (f >= 1) return "white";
  // ramp: dark bg-3 → accent → white. Two color-mix legs.
  if (f < 0.5) {
    const pct = Math.round((f / 0.5) * 100);
    return `color-mix(in srgb, var(--accent) ${pct}%, var(--bg-3))`;
  }
  const pct = Math.round(((f - 0.5) / 0.5) * 100);
  return `color-mix(in srgb, white ${pct}%, var(--accent))`;
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
  files,
  onOpenFile,
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
  files: main.BrowseRow[];
  onOpenFile: (path: string) => void;
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
      {files.length > 0 && (
        <div className="session-files">
          <div className="session-files-title">
            files written this session ({files.length})
          </div>
          {files.map((f) => (
            <div
              key={f.path}
              className="session-file-row"
              onClick={() => onOpenFile(f.path)}
              title={f.path}
            >
              {f.type && <span className={`chip type-${f.type}`}>{f.type}</span>}
              <span className="session-file-name">{f.rel}</span>
              <span className="row-meta">
                {f.repo}
                {f.feature ? ` · ${f.feature}` : ""}
              </span>
            </div>
          ))}
        </div>
      )}
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
    <pre style={{ whiteSpace: "pre-wrap", fontSize: 13, opacity: 0.7 }}>
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
