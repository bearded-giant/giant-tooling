import { useEffect, useMemo, useRef, useState } from "react";
import { DBSizes, PruneBuckets, PruneRun } from "../wailsjs/go/main/App";
import { EventsOff, EventsOn } from "../wailsjs/runtime/runtime";
import { main, prune } from "../wailsjs/go/models";

type Stage = "plan" | "confirm" | "reclaim" | "running" | "done";

// label + the script's --older-than arg, keyed to prune.Bands on the Go side.
const SPANS: { days: number; label: string; arg: string }[] = [
  { days: 0, label: "All time", arg: "" },
  { days: 30, label: "30+ days", arg: "30d" },
  { days: 90, label: "90+ days", arg: "90d" },
  { days: 180, label: "6+ months", arg: "180d" },
  { days: 365, label: "1+ year", arg: "365d" },
];

function band(b: prune.Bucket, days: number): prune.Band {
  return b.bands.find((x) => x.days === days) || ({ days, docs: 0, bytes: 0 } as prune.Band);
}

function bytes(n: number): string {
  if (n >= 1e9) return (n / 1073741824).toFixed(1) + " GB";
  if (n >= 1e6) return (n / 1048576).toFixed(0) + " MB";
  if (n >= 1e3) return (n / 1024).toFixed(0) + " KB";
  return n + " B";
}

function num(n: number): string {
  return n.toLocaleString();
}

function ageLabel(mtime: number): string {
  if (!mtime) return "";
  const days = Math.floor((Date.now() / 1000 - mtime) / 86400);
  if (days <= 0) return "today";
  if (days === 1) return "1d";
  if (days < 30) return days + "d";
  if (days < 365) return Math.round(days / 30) + "mo";
  return (days / 365).toFixed(1) + "y";
}

export default function PruneModal({
  onClose,
  onPruned,
}: {
  onClose: () => void;
  onPruned: () => void;
}) {
  const [buckets, setBuckets] = useState<prune.Bucket[] | null>(null);
  const [spanIdx, setSpanIdx] = useState(0);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [vacuum, setVacuum] = useState(true);
  const [stopDaemon, setStopDaemon] = useState(true);
  const [stage, setStage] = useState<Stage>("plan");
  const [log, setLog] = useState<string[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [dbBefore, setDbBefore] = useState<main.DBSizes | null>(null);
  const [dbAfter, setDbAfter] = useState<main.DBSizes | null>(null);
  const logRef = useRef<HTMLDivElement | null>(null);

  const span = SPANS[spanIdx];

  useEffect(() => {
    PruneBuckets().then(setBuckets).catch((e) => setErr(String(e)));
    DBSizes("live.db").then(setDbBefore).catch(() => {});
  }, []);

  useEffect(() => {
    EventsOn("prune:line", (line: string) => setLog((prev) => [...prev, line]));
    return () => EventsOff("prune:line");
  }, []);

  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [log]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && stage !== "running") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [stage, onClose]);

  const rows = useMemo(() => {
    if (!buckets) return [];
    return buckets
      .map((b) => ({ b, total: band(b, 0), match: band(b, span.days) }))
      .filter((r) => r.total.docs > 0);
  }, [buckets, span.days]);

  const maxTotal = Math.max(1, ...rows.map((r) => r.total.bytes));
  const picked = rows.filter((r) => selected.has(r.b.repo) && r.match.docs > 0);
  const totalDocs = picked.reduce((n, r) => n + r.match.docs, 0);
  const totalBytes = picked.reduce((n, r) => n + r.match.bytes, 0);

  const toggle = (repo: string, enabled: boolean) => {
    if (!enabled) return;
    setSelected((prev) => {
      const next = new Set(prev);
      next.has(repo) ? next.delete(repo) : next.add(repo);
      return next;
    });
  };

  const opts = () =>
    new prune.Options({
      repos: picked.map((r) => r.b.repo),
      olderThan: span.arg,
      vacuum,
      db: "live.db",
      vacuumOnly: false,
      stopDaemon: false,
    });

  const runReclaim = () => {
    setLog([]);
    setErr(null);
    setStage("running");
    PruneRun(
      new prune.Options({
        repos: [],
        olderThan: "",
        vacuum: true,
        db: "live.db",
        vacuumOnly: true,
        stopDaemon,
      }),
      false,
    )
      .then(() => {
        DBSizes("live.db")
          .then(setDbAfter)
          .catch(() => {});
        setStage("done");
        onPruned();
      })
      .catch((e) => {
        setErr(String(e));
        setStage("done");
      });
  };

  const run = (dryRun: boolean) => {
    setLog([]);
    setErr(null);
    setStage("running");
    PruneRun(opts(), dryRun)
      .then(() => {
        DBSizes("live.db")
          .then(setDbAfter)
          .catch(() => {});
        setStage("done");
        if (!dryRun) onPruned();
      })
      .catch((e) => {
        setErr(String(e));
        setStage("done");
      });
  };

  return (
    <div className="modal-backdrop" onClick={() => stage !== "running" && onClose()}>
      <div
        className="modal prune-modal"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label="Prune live index"
      >
        <div className="prune-head">
          <h2 className="settings-title">Prune live index</h2>
          <span className="prune-dbsize" title="live.db plus its WAL on disk">
            live.db {bytes(dbBefore?.main || 0)}
          </span>
        </div>

        {stage === "plan" && (
          <>
            <div className="settings-section">How far back</div>
            <div className="prune-span">
              <div className="segmented">
                {SPANS.map((s, i) => (
                  <button
                    key={s.days}
                    type="button"
                    className={i === spanIdx ? "active" : ""}
                    onClick={() => setSpanIdx(i)}
                  >
                    {s.label}
                  </button>
                ))}
              </div>
              <div className="settings-desc">
                {span.days === 0
                  ? "Every doc in the repos you pick."
                  : `Only docs untouched for ${span.label.replace("+", " or more ")}.`}
              </div>
            </div>

            <div className="settings-section">
              Repos
              <span className="prune-select-all">
                <button type="button" onClick={() => setSelected(new Set(rows.filter((r) => r.match.docs > 0).map((r) => r.b.repo)))}>
                  all
                </button>
                <button type="button" onClick={() => setSelected(new Set())}>
                  none
                </button>
              </span>
            </div>

            {!buckets && <div className="settings-desc">reading index…</div>}
            <div className="prune-rows">
              {rows.map(({ b, total, match }) => {
                const enabled = match.docs > 0;
                const on = selected.has(b.repo) && enabled;
                const totalPct = (total.bytes / maxTotal) * 100;
                const matchPct = total.bytes > 0 ? (match.bytes / total.bytes) * 100 : 0;
                return (
                  <div
                    key={b.repo}
                    className={`prune-row${on ? " on" : ""}${enabled ? "" : " disabled"}`}
                    onClick={() => toggle(b.repo, enabled)}
                    title={
                      enabled
                        ? `${num(match.docs)} of ${num(total.docs)} docs match`
                        : `nothing older than ${span.label.toLowerCase()}`
                    }
                  >
                    <span className="prune-check">{on ? "x" : ""}</span>
                    <span className="prune-repo">{b.repo}</span>
                    <span className="prune-bar" style={{ width: `${Math.max(4, totalPct)}%` }}>
                      <span className="prune-bar-match" style={{ width: `${matchPct}%` }} />
                    </span>
                    <span className="prune-figs">
                      {num(match.docs)} docs · ~{bytes(match.bytes)}
                    </span>
                    <span className="prune-age" title="newest / oldest doc">
                      {ageLabel(b.newest)}–{ageLabel(b.oldest)}
                    </span>
                  </div>
                );
              })}
            </div>

            <div className="settings-row prune-vacuum">
              <div className="settings-info">
                <div className="settings-label">Reclaim disk after (VACUUM)</div>
                <div className="settings-desc">
                  Rewrites the whole db so freed pages return to the filesystem. Takes a few
                  minutes on a large db. Off leaves the file the same size, reusing the space
                  for new docs.
                </div>
              </div>
              <SmallSwitch checked={vacuum} onChange={setVacuum} />
            </div>

            <div className="prune-total">
              {totalDocs > 0 ? (
                <>
                  Archive <strong>{num(totalDocs)}</strong> docs (~{bytes(totalBytes)}) from{" "}
                  {picked.length} {picked.length === 1 ? "repo" : "repos"}
                </>
              ) : (
                <span className="settings-desc">Pick a repo to see what would be archived.</span>
              )}
            </div>

            <div className="confirm-actions">
              <button type="button" onClick={onClose}>
                Cancel
              </button>
              <button
                type="button"
                className="prune-reclaim-btn"
                onClick={() => setStage("reclaim")}
                title="checkpoint the WAL and VACUUM without deleting anything"
              >
                Reclaim space
              </button>
              <button type="button" disabled={totalDocs === 0} onClick={() => run(true)}>
                Dry run
              </button>
              <button
                type="button"
                className="danger"
                disabled={totalDocs === 0}
                onClick={() => setStage("confirm")}
              >
                Prune…
              </button>
            </div>
          </>
        )}

        {stage === "confirm" && (
          <>
            <p className="confirm-text">
              Export <strong>{num(totalDocs)}</strong> docs (~{bytes(totalBytes)}) to an
              encrypted archive, then delete those rows from live.db.
            </p>
            <div className="prune-confirm-list">
              {picked.map((r) => (
                <div key={r.b.repo}>
                  <span className="prune-repo">{r.b.repo}</span>
                  <span className="prune-figs">
                    {num(r.match.docs)} docs · ~{bytes(r.match.bytes)}
                  </span>
                </div>
              ))}
            </div>
            <div className="settings-desc prune-note">
              Span: {span.label}. One gpg archive per repo lands next to your db backups; the
              rows go only after the ciphertext verifies. {vacuum ? "VACUUM runs after." : "No VACUUM."}
            </div>
            <div className="confirm-actions">
              <button type="button" onClick={() => setStage("plan")}>
                Back
              </button>
              <button type="button" className="danger" onClick={() => run(false)}>
                Archive &amp; delete
              </button>
            </div>
          </>
        )}

        {stage === "reclaim" && (
          <>
            <p className="confirm-text">
              Reclaim free pages in <strong>live.db</strong> ({bytes(dbBefore?.main || 0)}). Deletes
              nothing — checkpoints the WAL, then rewrites the file so pages freed by earlier
              prunes return to the filesystem.
            </p>
            <div className="settings-row">
              <div className="settings-info">
                <div className="settings-label">Stop the daemon while it runs</div>
                <div className="settings-desc">
                  VACUUM needs an exclusive lock and giantmemd holds live.db open for its
                  whole life, so without this the rewrite usually loses the race and gets
                  skipped. It restarts when the vacuum finishes.
                </div>
              </div>
              <SmallSwitch checked={stopDaemon} onChange={setStopDaemon} />
            </div>
            <div className="settings-desc prune-note">
              Needs about as much free scratch space as the db is big. Your sweep ingest and
              statusline keep reading during the run; the vacuum waits up to 10 minutes for a
              gap and reports honestly if it never gets one.
            </div>
            <div className="confirm-actions">
              <button type="button" onClick={() => setStage("plan")}>
                Back
              </button>
              <button type="button" className="danger" onClick={runReclaim}>
                Reclaim now
              </button>
            </div>
          </>
        )}

        {(stage === "running" || stage === "done") && (
          <>
            <div className="settings-section">
              {stage === "running" ? "Working" : err ? "Failed" : "Done"}
            </div>
            <div className="prune-log" ref={logRef}>
              {log.map((l, i) => (
                <div key={i}>{l}</div>
              ))}
              {stage === "running" && <div className="prune-log-live">…</div>}
            </div>
            {err && <div className="prune-err">{err}</div>}
            {stage === "done" && !err && dbAfter && dbAfter.main > 0 && (
              <div className="prune-total">
                live.db {bytes(dbBefore?.main || 0)} → <strong>{bytes(dbAfter.main)}</strong>
                {dbBefore && dbAfter.main < dbBefore.main && (
                  <> (freed {bytes(dbBefore.main - dbAfter.main)})</>
                )}
                {dbAfter.wal > 64 * 1024 * 1024 && (
                  <div className="settings-desc">
                    plus a {bytes(dbAfter.wal)} WAL still waiting on a checkpoint — it folds back
                    into the db on the next one, it is not new data.
                  </div>
                )}
              </div>
            )}
            <div className="confirm-actions">
              <button type="button" disabled={stage === "running"} onClick={onClose}>
                Close
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function SmallSwitch({
  checked,
  onChange,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <button
      type="button"
      className={`switch${checked ? " on" : ""}`}
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
    >
      <span className="switch-knob" />
    </button>
  );
}
