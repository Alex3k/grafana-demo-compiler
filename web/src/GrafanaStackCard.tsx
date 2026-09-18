import { useEffect, useRef, useState } from "react";
import { api } from "./api";
import type { GrafanaStack, Session } from "./types";

export function GrafanaStackCard({ session, onUpdated }: { session: Session; onUpdated: (stack: GrafanaStack) => void }) {
  const stack = session.grafanaStack;
  const [region, setRegion] = useState(stack?.region ?? "prod-us-east-0");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const updatedRef = useRef(onUpdated);
  updatedRef.current = onUpdated;
  const busy = submitting || stack?.status === "provisioning" || stack?.status === "awaiting_auth";
  const statusLabel = stack && ({ provisioning: "Creating stack", awaiting_auth: "Awaiting browser approval", needs_auth: "Created, needs login", ready: "Ready", failed: "Stack creation failed" })[stack.status];

  useEffect(() => {
    if (stack?.status !== "provisioning" && stack?.status !== "awaiting_auth") return;
    let stopped = false;
    let timer = 0;
    async function poll() {
      try {
        const next = await api.session(session.id);
        if (stopped) return;
        setError("");
        if (next.grafanaStack) updatedRef.current(next.grafanaStack);
        if (next.grafanaStack?.status !== "provisioning" && next.grafanaStack?.status !== "awaiting_auth") return;
      } catch (reason) {
        if (stopped) return;
        setError(reason instanceof Error ? reason.message : "Could not refresh stack progress.");
      }
      timer = window.setTimeout(poll, 1200);
    }
    timer = window.setTimeout(poll, 500);
    return () => { stopped = true; window.clearTimeout(timer); };
  }, [session.id, stack?.id, stack?.status]);

  return <section className={`deployment-card stack-card status-${stack?.status ?? "empty"}`}>
    <span>GRAFANA CLOUD STACK</span>
    {!stack && <p>Create a dedicated stack for this demo’s Grafana resources and telemetry.</p>}
    {stack && <div className="deployment-status" aria-live="polite"><strong>{statusLabel}</strong><small>{busy && <span className="activity-pulse" />}{stack.progress?.at(-1) || stack.stackName}</small></div>}
    {stack?.status === "awaiting_auth" && <p>Approve Grafana access in the browser window opened on this computer. This panel updates automatically when access is verified.</p>}
    {(stack?.status === "needs_auth" || stack?.status === "ready") && <div className="deployment-form">
      {stack.status === "needs_auth" && <p>The stack exists. Connect Grafana to enable this demo’s Grafana actions. No terminal command is needed.</p>}
      <button type="button" disabled={busy} onClick={async () => {
        if (busy) return;
        setSubmitting(true);
        setError("");
        try { onUpdated(await api.connectStack(session.id)); }
        catch (reason) { setError(reason instanceof Error ? reason.message : "Grafana connection could not be started."); }
        finally { setSubmitting(false); }
      }}>{submitting ? "Opening browser…" : stack.status === "ready" ? "Reconnect Grafana" : "Connect Grafana"}</button>
    </div>}
    {stack?.stackUrl && <a className="deployment-stack-link" href={stack.stackUrl} target="_blank" rel="noreferrer">Open {stack.stackSlug} ↗</a>}
    {stack?.error && <p role="alert" className="deployment-error">{stack.error}</p>}
    {error && <p role="alert" className="deployment-error">{error}</p>}
    {(!stack || stack.status === "failed") && <form className="deployment-form" onSubmit={async (event) => {
      event.preventDefault();
      if (busy || !region.trim()) return;
      setSubmitting(true);
      setError("");
      try { onUpdated(await api.createStack(session.id, region.trim())); }
      catch (reason) { setError(reason instanceof Error ? reason.message : "The stack could not be created."); }
      finally { setSubmitting(false); }
    }}>
      <label>Grafana Cloud region<input value={region} disabled={submitting || !!stack?.stackUrl} onChange={(event) => setRegion(event.target.value)} placeholder="for example prod-gb-south-0" autoComplete="off" /></label>
      <button type="submit" disabled={busy || !region.trim()}>{submitting ? "Creating stack…" : stack ? "Retry stack creation" : "Create stack"}</button>
      <small>Creating a Grafana Cloud stack may incur usage costs. Delete protection remains enabled.</small>
    </form>}
  </section>;
}
