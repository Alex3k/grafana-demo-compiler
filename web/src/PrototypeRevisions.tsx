import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import "./PrototypeRevisions.css";
import type { PrototypeIteration } from "./types";
type Revision = { id: string; baseIterationId: string; iterationId: string; briefVersion: number; goal: string; evidence: string; files: string[]; status: string; error: string };

function statusLabel(status: string) {
  return ({ pending: "Awaiting approval", approved: "Approved", building: "Building", generating: "Building", complete: "Complete", failed: "Failed", rejected: "Rejected" } as Record<string, string>)[status] ?? status;
}

function RevisionDialog({ item, iteration, base, onClose, children }: { item: Revision; iteration?: PrototypeIteration; base?: PrototypeIteration; onClose: () => void; children: React.ReactNode }) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const element = dialog.current;
    const previous = document.activeElement;
    element?.showModal();
    return () => { element?.close(); if (previous instanceof HTMLElement && previous.isConnected) previous.focus(); };
  }, []);
  return createPortal(<dialog ref={dialog} className="revision-dialog" aria-labelledby="revision-title" onCancel={onClose}>
    <header><div><p>Local files only · Does not deploy</p><h2 id="revision-title">Revision details</h2></div><button type="button" autoFocus onClick={onClose}>Close</button></header>
    <div className="revision-dialog-body">
      <span className={`revision-status status-${item.status}`}>{statusLabel(item.status)}</span><h3>{item.goal}</h3>
      <p className="revision-meta">{base ? `Based on iteration ${base.number}` : "Base iteration unavailable"} · Brief version {item.briefVersion}{iteration && ` · Result: iteration ${iteration.number}`}</p>
      {iteration?.summary && <><h3>Build summary</h3><Markdown remarkPlugins={[remarkGfm]} skipHtml>{iteration.summary}</Markdown></>}
      {!!iteration?.checks?.length && <><h3>Validation checks</h3><ul className="revision-checks">{iteration.checks.map((check, index) => <li key={index}><strong className={`check-${check.status}`}>{check.status === "passed" ? "✓" : "✕"} {check.name}</strong><span>{check.detail}</span></li>)}</ul></>}
      {(item.error || iteration?.error) && <p role="alert" className="revision-error">{item.error || iteration?.error}</p>}
      <details><summary>Technical context</summary>
        <Markdown remarkPlugins={[remarkGfm]} skipHtml>{item.evidence}</Markdown>
        {!!iteration?.progress?.length && <><h4>Recorded build activity</h4><ol>{iteration.progress.map((step, index) => <li key={index}>{step}</li>)}</ol></>}
        <p>Creates a new local iteration. Confirmed requirements and deployed resources are unchanged.</p>
        {iteration?.rootPath && <p>Local output: <code>{iteration.rootPath}</code></p>}
      </details>
      <details><summary>Files ({item.files.length} in approved scope)</summary><ul>{item.files.map(path => <li key={path}><code>{path}</code></li>)}</ul>
        {!!iteration?.artifacts?.length && <><h4>Generated files</h4><ul>{iteration.artifacts.map(file => <li key={file.path}><code>{file.path}</code></li>)}</ul></>}
      </details>
    </div><footer>{children}</footer>
  </dialog>, document.body);
}

export function PrototypeRevisions({ sessionId, iterations, onStarted }: { sessionId: string; iterations: PrototypeIteration[]; onStarted: (iteration: PrototypeIteration) => void }) {
  const [items, setItems] = useState<Revision[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const requests = useRef<AbortController | null>(null);
  const url = `/api/sessions/${encodeURIComponent(sessionId)}/revisions`;
  useEffect(() => {
    const controller = new AbortController(); let timer: ReturnType<typeof setTimeout>;
    requests.current = controller;
    setItems([]); setSelectedId(null); setBusy(false); setError("");
    async function refresh() {
      try { const response = await fetch(url, { signal: controller.signal }); if (!response.ok) throw new Error("Could not load revision proposals"); const result = await response.json(); if (!controller.signal.aborted) setItems(result); }
      catch (e) { if (!controller.signal.aborted) setError(String(e)); }
      if (!controller.signal.aborted) timer = setTimeout(refresh, 2000);
    }
    void refresh(); return () => { controller.abort(); clearTimeout(timer); };
  }, [url]);
  async function decide(item: Revision, decision: "approve" | "reject") {
    const controller = requests.current;
    if (!controller || controller.signal.aborted || busy) return;
    setBusy(true); setError("");
    try {
      const response = await fetch(`${url}/${item.id}`, { signal: controller.signal, method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ decision }) });
      const result = await response.json(); if (controller.signal.aborted) return; if (!response.ok) throw new Error(result.error || "Could not submit revision decision");
      setItems(previous => previous.map(p => p.id === item.id ? { ...p, status: decision === "approve" ? "generating" : "rejected", iterationId: decision === "approve" ? result.id : p.iterationId } : p));
      if (decision === "approve") onStarted(result);
    } catch (e) { if (!controller.signal.aborted) setError(String(e)); } finally { if (!controller.signal.aborted) setBusy(false); }
  }
  const selected = items.find(item => item.id === selectedId);
  function actions(item: Revision) {
    return item.status === "pending" && <div className="revision-actions"><button type="button" disabled={busy} onClick={() => void decide(item, "reject")}>Reject</button><button type="button" disabled={busy} onClick={() => void decide(item, "approve")}>{busy ? "Submitting…" : "Approve and build"}</button></div>;
  }
  if (!items.length && !error) return null;
  return <section className="prototype-revisions"><p className="eyebrow rail-section">PROTOTYPE REVISIONS</p>
    {error && <p role="alert" className="topic-error">{error}</p>}
    {items.map(item => {
      const iteration = iterations.find(candidate => candidate.id === item.iterationId);
      const base = iterations.find(candidate => candidate.id === item.baseIterationId);
      const checks = iteration?.checks ?? [];
      const progress = iteration?.progress?.at(-1);
      return <article key={item.id} className="revision-card">
        <div className="revision-card-heading"><strong>Prototype revision</strong><span className={`revision-status status-${item.status}`}>{statusLabel(item.status)}</span></div>
        <p className="revision-goal" title={item.goal}>{item.goal}</p>
        <p className="revision-meta">{item.status === "complete" ? "Local revision generated." : item.status === "rejected" ? "Revision declined." : item.status === "pending" ? "Review the proposed local changes." : item.status === "failed" ? "Revision build failed. Open details for the error." : "Building local revision."}</p>
        {progress && item.status === "generating" && <p className="revision-progress" role="status">{progress}</p>}
        {!!checks.length && <p className="revision-meta">{checks.filter(check => check.status === "passed").length}/{checks.length} checks passed</p>}
        <p className="revision-meta">{base ? `Based on iteration ${base.number}` : "Base iteration unavailable"}</p>
        <p className="revision-meta">Local files only · Does not deploy</p>
        <div className="revision-card-controls"><button type="button" onClick={() => setSelectedId(item.id)}>Details</button>{actions(item)}</div>
      </article>;
    })}
    {selected && <RevisionDialog key={selected.id} item={selected} iteration={iterations.find(item => item.id === selected.iterationId)} base={iterations.find(item => item.id === selected.baseIterationId)} onClose={() => setSelectedId(null)}>{error && <p role="alert" className="revision-error">{error}</p>}{actions(selected)}</RevisionDialog>}
  </section>;
}
