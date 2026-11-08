import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ClipboardSetText, OnFileDrop, OnFileDropOff } from "../wailsjs/runtime/runtime";
import {
  DownloadFile,
  GetPref,
  OpenPath,
  PickUploadFiles,
  ReindexWorkspaces,
  SetPref,
  UploadFiles,
  WorkspaceFiles,
  WorkspaceProjects,
  WorkspaceSearch,
} from "../wailsjs/go/main/App";
import { main, search } from "../wailsjs/go/models";

type Stars = { projects: string[]; folders: string[] };
type Scope = "workspace" | "project" | "all";
type Mode = "tree" | "content" | "starred";

type TNode = {
  key: string;
  name: string;
  kind: "ws" | "removed" | "dir" | "file";
  path: string;
  rel: string;
  ws: main.WsWorkspace | null;
  mtime: number;
  size: number;
  indexed: boolean;
  sessionId: string;
  dead: boolean;
  files: number;
  children: TNode[];
  parent: TNode | null;
};

type Conflict = { dest: string; files: string[]; names: string[] };
type Menu = { node: TNode; x: number; y: number };

const TOP_DIRS = ["features", "plans", "context", "research", "reviews", "specs"];
const FEATURE_RANK: Record<string, number> = { in_progress: 0, pending: 1, paused: 2 };
const FEATURE_DONE = new Set(["complete", "archived", "abandoned"]);
const NO_STARS: Stars = { projects: [], folders: [] };

const gmDir = (ws: main.WsWorkspace) => `${ws.dir}/.giantmem`;
const isFolder = (n: TNode) => n.kind === "ws" || n.kind === "dir";
const relPath = (n: TNode) => (n.rel ? `.giantmem/${n.rel}` : ".giantmem");

function ago(mtime: number): string {
  if (!mtime) return "";
  const s = Date.now() / 1000 - mtime;
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  if (s < 86400 * 30) return `${Math.floor(s / 86400)}d`;
  return new Date(mtime * 1000).toISOString().slice(0, 10);
}

function node(ws: main.WsWorkspace, rel: string, name: string, kind: TNode["kind"]): TNode {
  return {
    key: `${ws.dir}\0${rel}`,
    name,
    kind,
    path: rel ? `${gmDir(ws)}/${rel}` : gmDir(ws),
    rel,
    ws,
    mtime: 0,
    size: 0,
    indexed: false,
    sessionId: "",
    dead: ws.dead,
    files: 0,
    children: [],
    parent: null,
  };
}

function topRank(n: TNode): number {
  if (n.kind === "file") return 50;
  if (n.name === "history") return 99;
  const i = TOP_DIRS.indexOf(n.name);
  return i >= 0 ? i : 10;
}

function featureRank(status?: string): number {
  if (status && status in FEATURE_RANK) return FEATURE_RANK[status];
  return status && FEATURE_DONE.has(status) ? 4 : 3;
}

function sortTree(n: TNode, byMtime: boolean) {
  const features = n.ws?.features || {};
  n.children.sort((a, b) => {
    if (n.kind === "ws") {
      const d = topRank(a) - topRank(b);
      if (d) return d;
    } else if (n.rel === "features" && a.kind === "dir" && b.kind === "dir") {
      const d = featureRank(features[a.name]) - featureRank(features[b.name]);
      return d || b.mtime - a.mtime;
    }
    if ((a.kind === "file") !== (b.kind === "file")) return a.kind === "file" ? 1 : -1;
    if (byMtime && a.kind === "file") return b.mtime - a.mtime;
    return a.name.localeCompare(b.name);
  });
  for (const c of n.children) if (c.kind !== "file") sortTree(c, byMtime);
}

function rollup(n: TNode) {
  if (n.kind === "file") {
    n.files = 1;
    return;
  }
  for (const c of n.children) {
    c.parent = n;
    rollup(c);
    n.files += c.files;
    n.mtime = Math.max(n.mtime, c.mtime);
  }
}

function buildWorkspace(ws: main.WsWorkspace, rows: main.WsNode[], byMtime: boolean): TNode {
  const root = node(ws, "", ws.label, "ws");
  const dirs = new Map<string, TNode>([["", root]]);
  const dirOf = (rel: string): TNode => {
    const hit = dirs.get(rel);
    if (hit) return hit;
    const i = rel.lastIndexOf("/");
    const d = node(ws, rel, rel.slice(i + 1), "dir");
    dirOf(i < 0 ? "" : rel.slice(0, i)).children.push(d);
    dirs.set(rel, d);
    return d;
  };
  for (const r of rows) {
    if (r.dir) {
      dirOf(r.rel);
      continue;
    }
    const i = r.rel.lastIndexOf("/");
    const f = node(ws, r.rel, r.rel.slice(i + 1), "file");
    Object.assign(f, { mtime: r.mtime, size: r.size, indexed: r.indexed, sessionId: r.sessionId });
    dirOf(i < 0 ? "" : r.rel.slice(0, i)).children.push(f);
  }
  rollup(root);
  sortTree(root, byMtime);
  return root;
}

function buildProject(p: main.WsProject, rows: main.WsNode[], byMtime: boolean): TNode[] {
  const byWs = new Map<string, main.WsNode[]>();
  for (const r of rows) {
    const list = byWs.get(r.ws);
    if (list) list.push(r);
    else byWs.set(r.ws, [r]);
  }
  const trees = (p.workspaces || []).map((w) => buildWorkspace(w, byWs.get(w.dir) || [], byMtime));
  if (p.flat && trees.length === 1) {
    for (const c of trees[0].children) c.parent = null;
    return trees[0].children;
  }
  const live = trees.filter((t) => !t.dead);
  const dead = trees.filter((t) => t.dead);
  if (dead.length) {
    const removed: TNode = {
      key: `removed\0${p.root}`,
      name: `removed (${dead.length})`,
      kind: "removed",
      path: "",
      rel: "",
      ws: null,
      mtime: Math.max(...dead.map((d) => d.mtime)),
      size: 0,
      indexed: false,
      sessionId: "",
      dead: true,
      files: dead.reduce((s, d) => s + d.files, 0),
      children: dead,
      parent: null,
    };
    for (const d of dead) d.parent = removed;
    live.push(removed);
  }
  return live;
}

function walk(nodes: TNode[], fn: (n: TNode) => void) {
  for (const n of nodes) {
    fn(n);
    walk(n.children, fn);
  }
}

// filter grammar shared with the artifacts sidebar: free text plus ext:md
function parseFilter(q: string): { text: string; ext: string } {
  let ext = "";
  const words: string[] = [];
  for (const w of q.trim().toLowerCase().split(/\s+/)) {
    if (!w) continue;
    if (w.startsWith("ext:")) ext = w.slice(4).replace(/^\./, "");
    else words.push(w);
  }
  return { text: words.join(" "), ext };
}

function filterTree(nodes: TNode[], f: { text: string; ext: string }, forced: Set<string>): TNode[] {
  const out: TNode[] = [];
  for (const n of nodes) {
    if (n.kind === "file") {
      const ext = n.name.includes(".") ? n.name.split(".").pop()!.toLowerCase() : "";
      const hay = `${n.ws?.label || ""}/${n.rel}`.toLowerCase();
      if ((!f.ext || ext === f.ext) && (!f.text || hay.includes(f.text))) out.push(n);
      continue;
    }
    const kids = filterTree(n.children, f, forced);
    if (kids.length) {
      forced.add(n.key);
      out.push({ ...n, children: kids });
    } else if (f.text && !f.ext && n.name.toLowerCase().includes(f.text)) {
      out.push(n);
    }
  }
  return out;
}

function Hl({ text, q }: { text: string; q: string }) {
  const i = q ? text.toLowerCase().indexOf(q) : -1;
  if (i < 0) return <>{text}</>;
  return (
    <>
      {text.slice(0, i)}
      <mark>{text.slice(i, i + q.length)}</mark>
      {text.slice(i + q.length)}
    </>
  );
}

function Snippet({ text }: { text: string }) {
  const parts = text.split(/<([^<>]*)>/);
  return <>{parts.map((p, i) => (i % 2 ? <mark key={i}>{p}</mark> : p))}</>;
}

function loadOpen(root: string): Set<string> | null {
  try {
    const raw = localStorage.getItem(`gm.ws.open:${root}`);
    return raw ? new Set(JSON.parse(raw) as string[]) : null;
  } catch {
    return null;
  }
}

function saveOpen(root: string, open: Set<string>) {
  try {
    localStorage.setItem(`gm.ws.open:${root}`, JSON.stringify([...open]));
  } catch {}
}

export function useWorkspace(
  active: boolean,
  reloadKey: number,
  selectedPath: string,
  onOpenFile: (path: string) => void,
  onErr: (e: string) => void,
) {
  const [projects, setProjects] = useState<main.WsProject[]>([]);
  const [selRoot, setSelRootState] = useState(() => {
    try {
      return localStorage.getItem("gm.ws.project") || "";
    } catch {
      return "";
    }
  });
  const [rows, setRows] = useState<main.WsNode[]>([]);
  const [stars, setStars] = useState<Stars>(NO_STARS);
  const [projectFilter, setProjectFilter] = useState("");
  const [projSort, setProjSortState] = useState<"updated" | "name">(() => {
    try {
      return localStorage.getItem("gm.ws.projSort") === "name" ? "name" : "updated";
    } catch {
      return "updated";
    }
  });
  const setProjSort = (v: "updated" | "name") => {
    setProjSortState(v);
    try {
      localStorage.setItem("gm.ws.projSort", v);
    } catch {}
  };
  const [query, setQuery] = useState("");
  const [scope, setScope] = useState<Scope>("project");
  const [byMtime, setByMtime] = useState(false);
  const [mode, setMode] = useState<Mode>("tree");
  const [hits, setHits] = useState<search.Hit[]>([]);
  const [searching, setSearching] = useState(false);
  const [reindexing, setReindexing] = useState(false);
  const [open, setOpen] = useState<Set<string>>(new Set());
  const [focusKey, setFocusKey] = useState("");
  const [menu, setMenu] = useState<Menu | null>(null);
  const [conflict, setConflict] = useState<Conflict | null>(null);
  const [toast, setToast] = useState("");
  const searchRef = useRef<HTMLInputElement>(null);
  const pendingReveal = useRef("");

  const setSelRoot = useCallback((root: string) => {
    setSelRootState(root);
    try {
      localStorage.setItem("gm.ws.project", root);
    } catch {}
  }, []);

  const flash = useCallback((msg: string) => {
    setToast(msg);
    window.setTimeout(() => setToast((t) => (t === msg ? "" : t)), 1600);
  }, []);

  useEffect(() => {
    GetPref("ws.stars")
      .then((raw) => {
        if (!raw) return;
        const s = JSON.parse(raw) as Partial<Stars>;
        setStars({ projects: s.projects || [], folders: s.folders || [] });
      })
      .catch(() => {});
  }, []);

  const saveStars = useCallback(
    (next: Stars) => {
      setStars(next);
      SetPref("ws.stars", JSON.stringify(next)).catch((e) => onErr(String(e)));
    },
    [onErr],
  );
  const toggle = (list: string[], v: string) => (list.includes(v) ? list.filter((x) => x !== v) : [...list, v]);
  const toggleProjectStar = (root: string) => saveStars({ ...stars, projects: toggle(stars.projects, root) });
  const toggleFolderStar = (path: string) => saveStars({ ...stars, folders: toggle(stars.folders, path) });

  useEffect(() => {
    if (!active) return;
    WorkspaceProjects()
      .then((ps) => setProjects(ps || []))
      .catch((e) => onErr(String(e)));
  }, [active, reloadKey, onErr]);

  const project = useMemo(() => projects.find((p) => p.root === selRoot) || null, [projects, selRoot]);

  useEffect(() => {
    if (!projects.length || project) return;
    const starred = projects.find((p) => stars.projects.includes(p.root));
    setSelRoot((starred || projects[0]).root);
  }, [projects, project, stars.projects, setSelRoot]);

  const projectDirs = useMemo(() => (project?.workspaces || []).map((w) => w.dir), [project]);
  const dirsKey = projectDirs.join("\n");

  useEffect(() => {
    if (!active || !projectDirs.length) {
      setRows([]);
      return;
    }
    WorkspaceFiles(projectDirs)
      .then((r) => setRows(r || []))
      .catch((e) => onErr(String(e)));
  }, [active, dirsKey, reloadKey, onErr]);

  const reindex = useCallback(
    (dirs: string[]) => {
      if (!dirs.length) return;
      setReindexing(true);
      ReindexWorkspaces(dirs)
        .catch((e) => onErr(String(e)))
        .finally(() => setReindexing(false));
    },
    [onErr],
  );

  // edits made outside claude reach live.db only on daemon start; selecting a project catches them up
  useEffect(() => {
    if (!active || !project) return;
    reindex(project.workspaces.filter((w) => !w.dead).map((w) => w.dir));
  }, [active, project?.root]);

  useEffect(() => {
    if (!project) return;
    const stored = loadOpen(project.root);
    if (stored) {
      setOpen(stored);
      return;
    }
    const first = project.workspaces.find((w) => !w.dead);
    const keys = first ? [`${first.dir}\0`, `${first.dir}\0features`] : [];
    setOpen(new Set(keys));
  }, [project?.root]);

  const setOpenSaved = useCallback(
    (fn: (prev: Set<string>) => Set<string>) => {
      setOpen((prev) => {
        const next = fn(prev);
        if (project) saveOpen(project.root, next);
        return next;
      });
    },
    [project],
  );

  const tree = useMemo(() => (project ? buildProject(project, rows, byMtime) : []), [project, rows, byMtime]);

  const byPath = useMemo(() => {
    const m = new Map<string, TNode>();
    walk(tree, (n) => n.path && m.set(n.path, n));
    return m;
  }, [tree]);

  const filter = useMemo(() => parseFilter(mode === "tree" ? query : ""), [query, mode]);
  const { visible, forced } = useMemo(() => {
    const forced = new Set<string>();
    if (!filter.text && !filter.ext) return { visible: tree, forced };
    return { visible: filterTree(tree, filter, forced), forced };
  }, [tree, filter]);

  const flat = useMemo(() => {
    const out: { n: TNode; depth: number }[] = [];
    const add = (nodes: TNode[], depth: number) => {
      for (const n of nodes) {
        out.push({ n, depth });
        if (n.kind !== "file" && (open.has(n.key) || forced.has(n.key))) add(n.children, depth + 1);
      }
    };
    add(visible, 0);
    return out;
  }, [visible, open, forced]);

  const revealPath = useCallback(
    (path: string, expandSelf: boolean) => {
      const n = byPath.get(path);
      if (!n) return false;
      setOpenSaved((prev) => {
        const next = new Set(prev);
        for (let p = expandSelf ? n : n.parent; p; p = p.parent) next.add(p.key);
        return next;
      });
      if (isFolder(n)) setFocusKey(n.key);
      requestAnimationFrame(() =>
        document.querySelector(`.ws-tree [data-path="${CSS.escape(path)}"]`)?.scrollIntoView({ block: "nearest" }),
      );
      return true;
    },
    [byPath, setOpenSaved],
  );

  useEffect(() => {
    if (pendingReveal.current && revealPath(pendingReveal.current, true)) pendingReveal.current = "";
  }, [revealPath]);

  const wsOf = useCallback(
    (path: string): { p: main.WsProject; w: main.WsWorkspace } | null => {
      for (const p of projects)
        for (const w of p.workspaces || [])
          if (path === gmDir(w) || path.startsWith(`${gmDir(w)}/`)) return { p, w };
      return null;
    },
    [projects],
  );

  const goTo = useCallback(
    (path: string) => {
      const hit = wsOf(path);
      setMode("tree");
      if (!hit) return;
      if (hit.p.root !== selRoot) {
        pendingReveal.current = path;
        setSelRoot(hit.p.root);
        return;
      }
      revealPath(path, true);
    },
    [wsOf, selRoot, setSelRoot, revealPath],
  );

  useEffect(() => {
    if (selectedPath && mode === "tree") revealPath(selectedPath, false);
  }, [selectedPath]);

  const focused = focusKey ? flat.find((x) => x.n.key === focusKey)?.n : undefined;
  const scopeDirs = (): string[] => {
    if (scope === "all") return projects.flatMap((p) => (p.workspaces || []).map((w) => w.dir));
    if (scope === "workspace") {
      const ws = (selectedPath && byPath.get(selectedPath)?.ws) || focused?.ws || project?.workspaces.find((w) => !w.dead);
      return ws ? [ws.dir] : [];
    }
    return projectDirs;
  };

  const runSearch = () => {
    const q = query.trim();
    if (!q) return;
    setMode("content");
    setSearching(true);
    WorkspaceSearch(q, scopeDirs(), 200)
      .then((h) => setHits(h || []))
      .catch((e) => onErr(String(e)))
      .finally(() => setSearching(false));
  };

  const exitContent = useCallback(() => {
    setMode("tree");
    setQuery("");
    if (selectedPath) goTo(selectedPath);
  }, [selectedPath, goTo]);

  useEffect(() => {
    if (!active || mode === "tree") return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || document.querySelector(".modal-backdrop, .ctx-backdrop")) return;
      e.preventDefault();
      e.stopPropagation();
      (document.activeElement as HTMLElement | null)?.blur();
      exitContent();
    };
    // capture runs before the app-wide esc handler, which would clear the open doc
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [active, mode, exitContent]);

  const reloadRows = useCallback(() => {
    if (!projectDirs.length) return;
    WorkspaceFiles(projectDirs)
      .then((r) => setRows(r || []))
      .catch((e) => onErr(String(e)));
  }, [projectDirs, onErr]);

  const upload = useCallback(
    async (dest: string, files: string[], onConflict: string) => {
      try {
        const res = await UploadFiles(dest, files, onConflict);
        if (res.conflicts?.length && !onConflict) {
          setConflict({ dest, files, names: res.conflicts });
          return;
        }
        setConflict(null);
        const n = res.written?.length || 0;
        flash(n ? `uploaded ${n} file${n === 1 ? "" : "s"}` : "nothing uploaded");
        reloadRows();
      } catch (e) {
        onErr(String(e));
      }
    },
    [flash, reloadRows, onErr],
  );

  const pickAndUpload = async (dest: string) => {
    try {
      const files = await PickUploadFiles();
      if (files?.length) upload(dest, files, "");
    } catch (e) {
      onErr(String(e));
    }
  };

  useEffect(() => {
    if (!active) return;
    OnFileDrop((x, y, paths) => {
      const el = document.elementFromPoint(x, y)?.closest<HTMLElement>("[data-drop-dir]");
      if (el?.dataset.dropDir && paths?.length) upload(el.dataset.dropDir, paths, "");
    }, true);
    return () => OnFileDropOff();
  }, [active, upload]);

  const copy = async (text: string) => {
    try {
      await ClipboardSetText(text);
      flash("copied");
    } catch (e) {
      onErr(String(e));
    }
  };

  const download = async (path: string) => {
    try {
      const dest = await DownloadFile(path);
      if (dest) flash(`saved ${dest.split("/").pop()}`);
    } catch (e) {
      onErr(String(e));
    }
  };

  const toggleOpen = (n: TNode) =>
    setOpenSaved((prev) => {
      const next = new Set(prev);
      if (next.has(n.key) || forced.has(n.key)) next.delete(n.key);
      else next.add(n.key);
      return next;
    });

  const rowFor = (path: string): main.BrowseRow | null => {
    const n = byPath.get(path);
    if (!n || n.kind !== "file" || !n.ws) return null;
    const feature = n.rel.startsWith("features/") ? n.rel.split("/")[1] : "";
    return {
      path,
      rel: n.rel,
      repo: project?.name || "",
      feature,
      worktree: n.ws.dir,
      type: "",
      mtime: n.mtime,
      sessionId: n.sessionId,
      dead: n.dead,
    } as main.BrowseRow;
  };

  const sidebarProjects = useMemo(() => {
    const f = projectFilter.trim().toLowerCase();
    let list = f ? projects.filter((p) => p.name.toLowerCase().includes(f)) : projects;
    if (projSort === "name") list = [...list].sort((a, b) => a.name.localeCompare(b.name));
    return [...list.filter((p) => stars.projects.includes(p.root)), ...list.filter((p) => !stars.projects.includes(p.root))];
  }, [projects, projectFilter, stars.projects, projSort]);

  const projectStars = useMemo(
    () => stars.folders.filter((f) => projectDirs.some((d) => f === `${d}/.giantmem` || f.startsWith(`${d}/.giantmem/`))),
    [stars.folders, projectDirs],
  );

  const fileCount = useMemo(() => rows.filter((r) => !r.dir).length, [rows]);

  return {
    projects,
    sidebarProjects,
    project,
    selRoot,
    pickProject: (root: string) => {
      setSelRoot(root);
      setMode("tree");
    },
    stars,
    toggleProjectStar,
    toggleFolderStar,
    projectStars,
    projectFilter,
    setProjectFilter,
    projSort,
    setProjSort,
    query,
    setQuery,
    scope,
    setScope,
    byMtime,
    setByMtime,
    mode,
    setMode,
    hits,
    searching,
    runSearch,
    exitContent,
    reindexing,
    refresh: () => {
      reloadRows();
      if (project) reindex(project.workspaces.filter((w) => !w.dead).map((w) => w.dir));
    },
    flat,
    filterText: filter.text,
    open,
    forced,
    toggleOpen,
    focusKey,
    setFocusKey,
    byPath,
    wsOf,
    goTo,
    selectedPath,
    onOpenFile,
    menu,
    setMenu,
    conflict,
    setConflict,
    upload,
    pickAndUpload,
    copy,
    download,
    toast,
    searchRef,
    rowFor,
    fileCount,
  };
}

export type Ws = ReturnType<typeof useWorkspace>;

function Svg({ children, size = 16 }: { children: React.ReactNode; size?: number }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {children}
    </svg>
  );
}

// lucide paths, same set as the ribbon
const ICON = {
  chevRight: <path d="m9 18 6-6-6-6" />,
  chevDown: <path d="m6 9 6 6 6-6" />,
  folder: (
    <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
  ),
  folderOpen: (
    <path d="m6 14 1.5-2.9A2 2 0 0 1 9.24 10H20a2 2 0 0 1 1.94 2.5l-1.54 6a2 2 0 0 1-1.95 1.5H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H18a2 2 0 0 1 2 2v2" />
  ),
  doc: (
    <>
      <path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z" />
      <path d="M14 2v4a2 2 0 0 0 2 2h4" />
      <path d="M16 13H8" />
      <path d="M16 17H8" />
    </>
  ),
  file: (
    <>
      <path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z" />
      <path d="M14 2v4a2 2 0 0 0 2 2h4" />
    </>
  ),
  branch: (
    <>
      <line x1="6" x2="6" y1="3" y2="15" />
      <circle cx="18" cy="6" r="3" />
      <circle cx="6" cy="18" r="3" />
      <path d="M18 9a9 9 0 0 1-9 9" />
    </>
  ),
  archive: (
    <>
      <rect width="20" height="5" x="2" y="3" rx="1" />
      <path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8" />
      <path d="M10 12h4" />
    </>
  ),
};

function iconFor(n: TNode, expanded: boolean) {
  if (n.kind === "ws") return ICON.branch;
  if (n.kind === "removed") return ICON.archive;
  if (n.kind === "dir") return expanded ? ICON.folderOpen : ICON.folder;
  return /\.(md|mmd|txt)$/i.test(n.name) ? ICON.doc : ICON.file;
}

export function WsSidebar({ w }: { w: Ws }) {
  return (
    <>
      <div className="doit-section doit-section-row">
        <span>projects</span>
        <span className="doit-section-tools">
          <button
            type="button"
            className="toggle-pill"
            onClick={() => w.setProjSort(w.projSort === "name" ? "updated" : "name")}
            title="sort projects"
          >
            ↕ {w.projSort === "name" ? "name" : "last updated"}
          </button>
        </span>
      </div>
      <div className="ws-side-filter">
        <input
          type="search"
          placeholder="filter projects…"
          value={w.projectFilter}
          onChange={(e) => w.setProjectFilter(e.target.value)}
        />
      </div>
      {w.stars.folders.length > 0 && (
        <>
          <div
            data-nav
            className={`facet-row doit-list-row ${w.mode === "starred" ? "selected" : ""}`}
            onClick={() => w.setMode("starred")}
            title="every starred folder across projects"
          >
            <span className="doit-list-name">
              <span className="ws-star on">★</span> all starred
            </span>
            <span className="doit-list-right">
              <span className="count">{w.stars.folders.length}</span>
            </span>
          </div>
          <div className="doit-sep" />
        </>
      )}
      {w.sidebarProjects.map((p) => {
        const starred = w.stars.projects.includes(p.root);
        const removed = (p.workspaces || []).every((x) => x.dead);
        return (
          <div
            key={p.root || p.name}
            data-nav
            className={`facet-row doit-list-row ${p.root === w.selRoot && w.mode !== "starred" ? "selected" : ""} ${removed ? "empty" : ""}`}
            onClick={() => w.pickProject(p.root)}
            title={p.root}
          >
            <span className="doit-list-name">
              <button
                type="button"
                className={`ws-star ${starred ? "on" : ""}`}
                title={starred ? "unstar project" : "star project"}
                onClick={(e) => {
                  e.stopPropagation();
                  w.toggleProjectStar(p.root);
                }}
              >
                {starred ? "★" : "☆"}
              </button>
              <Hl text={p.name} q={w.projectFilter.trim().toLowerCase()} />
              {removed && <span className="doit-snip"> removed</span>}
            </span>
            <span className="doit-list-right">
              <span className="doit-age">{ago(p.mtime)}</span>
              <span className="count">{(p.workspaces || []).length}</span>
            </span>
          </div>
        );
      })}
      {!w.sidebarProjects.length && <div className="detail-empty">{w.projects.length ? "no match" : "loading…"}</div>}
    </>
  );
}

function TreeRow({ w, n, depth }: { w: Ws; n: TNode; depth: number }) {
  const folder = n.kind !== "file";
  const expanded = folder && (w.open.has(n.key) || w.forced.has(n.key));
  const starred = isFolder(n) && w.stars.folders.includes(n.path);
  const status = n.kind === "dir" && n.parent?.kind === "dir" && n.parent.rel === "features" ? n.ws?.features?.[n.name] || "" : "";
  const dropDir = n.dead || n.kind === "removed" ? "" : n.kind === "file" ? n.path.slice(0, n.path.lastIndexOf("/")) : n.path;
  const selected = n.kind === "file" && n.path === w.selectedPath;
  const focused = folder && n.key === w.focusKey;
  const muted = n.dead || (n.kind === "dir" && n.name === "history" && n.parent?.kind !== "dir") || FEATURE_DONE.has(status);
  const feature = n.kind === "ws" && n.ws?.activeFeature && n.ws.activeFeature !== n.name ? n.ws.activeFeature : "";
  return (
    <div
      data-nav={folder ? "open" : ""}
      data-path={n.path || undefined}
      data-drop-dir={dropDir || undefined}
      aria-expanded={folder ? expanded : undefined}
      tabIndex={-1}
      className={`ws-row ws-${n.kind} ${selected ? "selected" : ""} ${focused ? "focused" : ""} ${muted ? "ws-dim" : ""}`}
      style={dropDir ? ({ "--wails-drop-target": "drop" } as React.CSSProperties) : undefined}
      onClick={() => {
        if (folder) {
          w.toggleOpen(n);
          if (isFolder(n)) w.setFocusKey(n.key);
        } else {
          w.onOpenFile(n.path);
        }
      }}
      onKeyDown={(e) => {
        if (e.key === "ArrowLeft" && folder && expanded) {
          e.preventDefault();
          w.toggleOpen(n);
        } else if (e.key === "s" && isFolder(n) && !e.metaKey && !e.ctrlKey) {
          e.preventDefault();
          w.toggleFolderStar(n.path);
        }
      }}
      onContextMenu={(e) => {
        e.preventDefault();
        if (n.kind !== "removed") w.setMenu({ node: n, x: e.clientX, y: e.clientY });
      }}
      title={n.path || n.name}
    >
      {Array.from({ length: depth }, (_, i) => (
        <span key={i} className="ws-indent" />
      ))}
      <span className="ws-chev">{folder && <Svg size={14}>{expanded ? ICON.chevDown : ICON.chevRight}</Svg>}</span>
      <span className="ws-icon">
        <Svg>{iconFor(n, expanded)}</Svg>
      </span>
      <span className="ws-name">
        <Hl text={n.name} q={w.filterText} />
      </span>
      {starred && <span className="ws-star on">★</span>}
      {feature && <span className="tok tok-scope">{feature}</span>}
      {n.kind === "ws" && n.dead && <span className="tok due-over">gone</span>}
      {status && <span className={`tok ${status === "in_progress" ? "status-in_progress" : ""}`}>{status.replace("_", " ")}</span>}
      {n.kind === "file" && !n.indexed && !n.dead && (
        <span className="ws-unindexed" title="not in the search index (binary, empty, or over the size cap)">
          not indexed
        </span>
      )}
      <span className="ws-right">{folder ? n.files : ago(n.mtime)}</span>
    </div>
  );
}

function WsMenu({ w, menu }: { w: Ws; menu: Menu }) {
  const n = menu.node;
  const close = () => w.setMenu(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") w.setMenu(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [w]);
  const act = (fn: () => void) => () => {
    close();
    fn();
  };
  const folder = isFolder(n);
  const starred = w.stars.folders.includes(n.path);
  const top = Math.min(menu.y, window.innerHeight - 260);
  return (
    <div className="ctx-backdrop" onClick={close} onContextMenu={(e) => { e.preventDefault(); close(); }}>
      <div className="ctx-menu" style={{ left: menu.x, top }} onClick={(e) => e.stopPropagation()}>
        <div className="ctx-title" title={n.path}>{relPath(n)}</div>
        <button type="button" className="ctx-item" onClick={act(() => w.copy(n.path))}>
          Copy absolute path
        </button>
        <button type="button" className="ctx-item" onClick={act(() => w.copy(relPath(n)))}>
          Copy relative path
        </button>
        {!folder && (
          <button type="button" className="ctx-item" onClick={act(() => w.copy(n.path.slice(0, n.path.lastIndexOf("/"))))}>
            Copy folder path
          </button>
        )}
        {!folder && (
          <button type="button" className="ctx-item" onClick={act(() => w.download(n.path))}>
            Download…
          </button>
        )}
        {folder && !n.dead && (
          <button type="button" className="ctx-item" onClick={act(() => w.pickAndUpload(n.path))}>
            Upload files here…
          </button>
        )}
        {folder && (
          <button type="button" className="ctx-item" onClick={act(() => w.toggleFolderStar(n.path))}>
            {starred ? "Unstar folder" : "Star folder"}
          </button>
        )}
        {!n.dead && (
          <button type="button" className="ctx-item" onClick={act(() => OpenPath(n.path, true))}>
            Reveal in Finder
          </button>
        )}
      </div>
    </div>
  );
}

function ConflictModal({ w, c }: { w: Ws; c: Conflict }) {
  const where = w.wsOf(c.dest);
  const rel = where ? c.dest.slice(where.w.dir.length + 1) : c.dest;
  return (
    <div className="modal-backdrop" onClick={() => w.setConflict(null)}>
      <div className="modal confirm-modal" onClick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
        <h2 className="settings-title">Already there</h2>
        <p className="confirm-text">
          {c.names.join(", ")} {c.names.length === 1 ? "already exists" : "already exist"} in <code>{rel}</code>.
        </p>
        <div className="confirm-actions">
          <button type="button" onClick={() => w.setConflict(null)}>
            Cancel
          </button>
          <button type="button" onClick={() => w.upload(c.dest, c.files, "skip")}>
            Skip
          </button>
          <button type="button" onClick={() => w.upload(c.dest, c.files, "keep")}>
            Keep both
          </button>
          <button type="button" className="danger" onClick={() => w.upload(c.dest, c.files, "overwrite")}>
            Overwrite
          </button>
        </div>
      </div>
    </div>
  );
}

function StarredFolder({ w, path }: { w: Ws; path: string }) {
  const hit = w.wsOf(path);
  const gone = !hit || hit.w.dead;
  const rel = hit ? path.slice(hit.w.dir.length + 1) : path;
  return (
    <div
      data-nav
      className={`ws-row ws-starred-row ${gone ? "ws-dim" : ""}`}
      onClick={() => !gone && w.goTo(path)}
      title={path}
    >
      <span className="ws-icon ws-star on">★</span>
      <span className="ws-name">
        {w.mode === "starred" && hit && <span className="ws-proj">{hit.p.name} · </span>}
        {hit && hit.w.label && !hit.p.flat && <span className="ws-mono">{hit.w.label} · </span>}
        {rel}
      </span>
      {gone && <span className="tok due-over">gone</span>}
      {gone && (
        <button
          type="button"
          className="toggle-pill"
          onClick={(e) => {
            e.stopPropagation();
            w.toggleFolderStar(path);
          }}
        >
          unstar
        </button>
      )}
    </div>
  );
}

function Results({ w }: { w: Ws }) {
  const groups = useMemo(() => {
    const by = new Map<string, search.Hit[]>();
    for (const h of w.hits) {
      const hit = w.wsOf(h.filepath);
      const rel = hit ? h.filepath.slice(hit.w.dir.length + 1) : h.filepath;
      const dir = rel.slice(0, rel.lastIndexOf("/"));
      const key = hit ? [w.scope === "all" ? hit.p.name : "", hit.p.flat ? "" : hit.w.label, dir].filter(Boolean).join(" · ") : dir;
      const list = by.get(key);
      if (list) list.push(h);
      else by.set(key, [h]);
    }
    return [...by.entries()];
  }, [w.hits, w.wsOf, w.scope]);
  if (w.searching) return <div className="doit-section">searching…</div>;
  if (!w.hits.length) return <div className="doit-section">no matches · esc returns to the tree</div>;
  return (
    <>
      {groups.map(([dir, hits]) => (
        <div key={dir}>
          <div className="doit-section ws-mono-head">{dir}</div>
          {hits.map((h) => (
            <div
              key={h.filepath}
              data-nav
              className={`doit-row ${h.filepath === w.selectedPath ? "selected" : ""}`}
              onClick={() => w.onOpenFile(h.filepath)}
              title={h.filepath}
            >
              <span className="ws-icon">
                <Svg>{ICON.doc}</Svg>
              </span>
              <div className="doit-text">
                {h.filepath.split("/").pop()}
                {h.snippet && (
                  <div className="doit-snip">
                    <Snippet text={h.snippet} />
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      ))}
    </>
  );
}

export function WsTree({ w }: { w: Ws }) {
  const p = w.project;
  const title = w.mode === "starred" ? "all starred" : p?.name || "workspace files";
  const sub =
    w.mode === "starred"
      ? `${w.stars.folders.length} folders`
      : w.mode === "content"
        ? `${w.hits.length} matches`
        : p
          ? `${(p.workspaces || []).length} workspace${(p.workspaces || []).length === 1 ? "" : "s"} · ${w.fileCount} files`
          : "";
  return (
    <div className="ws-tree">
      <div className="doit-list-head">
        <strong>{title}</strong>
        <span className="doit-snip">{sub}</span>
        {w.reindexing && <span className="doit-snip">reindexing…</span>}
        <button type="button" className="toggle-pill" onClick={w.refresh} title="re-read the project from disk and reindex it">
          ↻ refresh
        </button>
      </div>
      <div className="doit-add">
        <input
          ref={w.searchRef}
          type="text"
          placeholder="filter by name… enter searches content (/ to focus)"
          value={w.query}
          onChange={(e) => {
            w.setQuery(e.target.value);
            if (w.mode === "content" && !e.target.value) w.setMode("tree");
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              w.runSearch();
            }
          }}
        />
      </div>
      <div className="doit-facets">
        <select
          className={w.scope !== "project" ? "on" : ""}
          value={w.scope}
          onChange={(e) => w.setScope(e.target.value as Scope)}
          title="content search scope"
        >
          <option value="workspace">search: this workspace</option>
          <option value="project">search: this project</option>
          <option value="all">search: all projects</option>
        </select>
        <select
          className={w.byMtime ? "on" : ""}
          value={w.byMtime ? "modified" : "name"}
          onChange={(e) => w.setByMtime(e.target.value === "modified")}
          title="file order inside each folder"
        >
          <option value="name">files: by name</option>
          <option value="modified">files: last modified</option>
        </select>
      </div>
      {w.mode === "starred" &&
        w.stars.folders.map((f) => <StarredFolder key={f} w={w} path={f} />)}
      {w.mode === "content" && <Results w={w} />}
      {w.mode === "tree" && (
        <>
          {w.projectStars.length > 0 && (
            <>
              <div className="doit-section">starred</div>
              {w.projectStars.map((f) => (
                <StarredFolder key={f} w={w} path={f} />
              ))}
              <div className="doit-section">tree</div>
            </>
          )}
          <div className="ws-rows">
            {w.flat.map(({ n, depth }) => (
              <TreeRow key={n.key} w={w} n={n} depth={depth} />
            ))}
          </div>
          {!w.flat.length && (
            <div className="doit-section">{p ? (w.filterText ? "no match" : "empty") : "pick a project"}</div>
          )}
        </>
      )}
      {w.menu && <WsMenu w={w} menu={w.menu} />}
      {w.conflict && <ConflictModal w={w} c={w.conflict} />}
      {w.toast && <div className="ws-toast">{w.toast}</div>}
    </div>
  );
}
