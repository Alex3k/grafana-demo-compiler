import { useEffect, useState } from "react";
import type { PrototypeIteration } from "./types";
type Revision = { id: string; baseIterationId: string; briefVersion: number; goal: string; evidence: string; files: string[]; status: string; error: string };
export function PrototypeRevisions({ sessionId, onStarted }: { sessionId: string; onStarted: (iteration: PrototypeIteration) => void }) {
  const [items, setItems] = useState<Revision[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const url = `/api/sessions/${encodeURIComponent(sessionId)}/revisions`;
  useEffect(() => {
    const controller = new AbortController(); let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try { const response = await fetch(url, { signal: controller.signal }); if (!response.ok) throw new Error("Could not load revision proposals"); setItems(await response.json()); }
      catch (e) { if (!controller.signal.aborted) setError(String(e)); }
      if (!controller.signal.aborted) timer = setTimeout(refresh, 2000);
    }
    void refresh(); return () => { controller.abort(); clearTimeout(timer); };
  }, [url]);
  async function decide(item: Revision, decision: "approve" | "reject") {
    setBusy(true); setError("");
    try {
      const response = await fetch(`${url}/${item.id}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ decision }) });
      const result = await response.json(); if (!response.ok) throw new Error(result.error || "Could not submit revision decision");
      setItems(previous => previous.map(p => p.id === item.id ? { ...p, status: decision === "approve" ? "generating" : "rejected" } : p));
      if (decision === "approve") onStarted(result);
    } catch (e) { setError(String(e)); } finally { setBusy(false); }
  }
  if (!items.length && !error) return null;
  return <section className="grafana-actions"><p className="eyebrow rail-section">PROTOTYPE REVISIONS</p>
    {error && <p role="alert" className="topic-error">{error}</p>}
    {items.map(item => <details key={item.id} open={item.status === "pending"}>
      <summary>{item.status} · {item.goal}</summary>
      <p>{item.evidence}</p><p>Base iteration: <code>{item.baseIterationId}</code> · brief version {item.briefVersion}</p>
      <p>Approved edit scope:</p><ul>{item.files.map(path => <li key={path}><code>{path}</code></li>)}</ul>
      <p>Creates a new local iteration. Does not deploy, change Grafana resources, or approve changes to confirmed requirements.</p>
      {item.error && <p role="alert">{item.error}</p>}
      {item.status === "pending" && <div className="grafana-action-buttons"><button disabled={busy} onClick={() => void decide(item, "reject")}>Reject</button><button disabled={busy} onClick={() => void decide(item, "approve")}>{busy ? "Submitting…" : "Approve and build"}</button></div>}
    </details>)}
  </section>;
}
