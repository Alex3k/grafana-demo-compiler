import { useEffect, useState } from "react";

type Action = { id: string; stack: string; request: { args: string[]; manifest?: string; reason: string }; status: string; output: string };
type Snapshot = { stack: string; actions: Action[] };

export function GrafanaActions({ sessionId }: { sessionId: string }) {
  const [snapshot, setSnapshot] = useState<Snapshot>({ stack: "", actions: [] });
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState("");
  const url = `/api/sessions/${encodeURIComponent(sessionId)}/gcx-actions`;
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const response = await fetch(url, { signal: controller.signal });
        if (!response.ok) throw new Error("Could not load Grafana actions");
        setSnapshot(await response.json());
      } catch (e) { if (!controller.signal.aborted) setError(String(e)); }
      if (!controller.signal.aborted) timer = setTimeout(refresh, 2000);
    }
    void refresh();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [url]);

  async function decide(action: Action, decision: "approve" | "reject") {
    setBusy(action.id); setError("");
    try {
      const response = await fetch(`${url}/${action.id}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ decision }) });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || "Could not submit decision");
      setSnapshot(previous => ({ ...previous, actions: previous.actions.map(a => a.id === action.id ? result : a) }));
    } catch (e) { setError(String(e)); }
    finally { setBusy(null); }
  }

  return <section className="grafana-actions">
    <p className="eyebrow rail-section">GRAFANA ACTIONS</p>
    <p>Reads run automatically. Each write needs your approval.</p>
    {snapshot.stack ? <details><summary>One-time gcx login</summary><p>Run in your terminal, then retry your request. Credentials stay out of chat.</p><pre>{`gcx login ${snapshot.stack} --server https://${snapshot.stack}.grafana.net --oauth`}</pre></details> : <p>Create this demo’s stack first to inspect or change its Grafana resources.</p>}
    {error && <p role="alert" className="topic-error">{error}</p>}
    {snapshot.actions.map(action => <details key={action.id} open={action.status === "pending" || action.status === "running"}>
      <summary>{action.status === "pending" ? "Approval required" : action.status} · {action.request.reason || "gcx operation"}</summary>
      <p>Target: <strong>{action.stack || "CLI discovery only"}</strong></p>
      <p>Exact arguments (JSON array; executed directly, not through a shell):</p>
      <pre>{JSON.stringify(["gcx", ...action.request.args, "--context", action.stack], null, 2)}</pre>
      {action.request.manifest && <><p>Exact manifest supplied as @manifest:</p><pre>{action.request.manifest}</pre></>}
      {action.status === "pending" && <><p>This may modify or delete resources. Approval covers only this command and payload.</p><div className="grafana-action-buttons"><button disabled={busy !== null} onClick={() => void decide(action, "reject")}>Reject</button><button disabled={busy !== null} onClick={() => void decide(action, "approve")}>{busy === action.id ? "Executing…" : "Approve and execute"}</button></div></>}
      {action.status === "running" && <p>Executing. If the server restarted, the outcome may be unknown—inspect Grafana before retrying.</p>}
      {action.output && <pre>{action.output}</pre>}
    </details>)}
  </section>;
}
