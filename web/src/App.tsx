import { FormEvent, useEffect, useId, useRef, useState } from "react";
import mermaid from "mermaid";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { api, streamMessage } from "./api";
import type { BriefItem, Health, LivingBrief, Message, Session, StreamEvent } from "./types";

mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: "dark" });

const EMPTY_HEALTH: Health = {
  status: "loading",
  sqlite: { status: "checking" },
  bedrock: { status: "checking", modelId: "" },
  agentObservability: { status: "checking", detail: "Checking connection" },
};

function App() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [active, setActive] = useState<Session | null>(null);
  const [health, setHealth] = useState<Health>(EMPTY_HEALTH);
  const [focused, setFocused] = useState(() => localStorage.getItem("layout") === "focused");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [railOpen, setRailOpen] = useState(false);
  const conversationRef = useRef<HTMLElement>(null);
  const stickToBottomRef = useRef(true);

  useEffect(() => {
    void Promise.all([api.sessions(), api.health()])
      .then(async ([items, nextHealth]) => {
        setSessions(items);
        setHealth(nextHealth);
        if (items[0]) setActive(await api.session(items[0].id));
      })
      .catch((reason: Error) => setError(reason.message));
  }, []);

  useEffect(() => {
    if (!stickToBottomRef.current) return;
    const conversation = conversationRef.current;
    conversation?.scrollTo({ top: conversation.scrollHeight, behavior: "auto" });
  }, [active?.messages]);

  async function refreshSessions() {
    const items = await api.sessions();
    setSessions(items);
  }

  async function selectSession(session: Session) {
    setError("");
    stickToBottomRef.current = true;
    setActive(await api.session(session.id));
    setSidebarOpen(false);
  }

  async function newSession() {
    setError("");
    stickToBottomRef.current = true;
    const session = await api.createSession();
    setActive({ ...session, messages: [] });
    await refreshSessions();
    setSidebarOpen(false);
  }

  async function send(content: string) {
    if (!active || sending) return;
    stickToBottomRef.current = true;
    setSending(true);
    setError("");
    try {
      await streamMessage(active.id, content, applyStreamEvent);
      await refreshSessions();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "The message could not be sent.");
    } finally {
      setSending(false);
      void api.health().then(setHealth).catch(() => undefined);
    }
  }

  function applyStreamEvent(streamEvent: StreamEvent) {
    if (streamEvent.event === "turn_completed" || streamEvent.event === "error") setSending(false);
    setActive((current) => {
      if (!current) return current;
      const messages = [...(current.messages ?? [])];
      if (["user_message", "activity", "message_started", "message_completed"].includes(streamEvent.event)) {
        const incoming = streamEvent.data as Message;
        const index = messages.findIndex((message) => message.id === incoming.id);
        if (index >= 0) messages[index] = incoming;
        else messages.push(incoming);
      } else if (streamEvent.event === "delta") {
        const delta = streamEvent.data as { messageId: string; delta: string };
        const index = messages.findIndex((message) => message.id === delta.messageId);
        if (index >= 0) messages[index] = { ...messages[index], content: messages[index].content + delta.delta };
      } else if (streamEvent.event === "error") {
        const failure = streamEvent.data as { messageId: string; message: string };
        const index = messages.findIndex((message) => message.id === failure.messageId);
        if (index >= 0) messages[index] = { ...messages[index], status: "failed", content: messages[index].content || failure.message };
        setError(failure.message);
      } else if (streamEvent.event === "brief_updated") {
        const brief = streamEvent.data as LivingBrief;
        return { ...current, brief, updatedAt: brief.updatedAt, messages };
      } else if (streamEvent.event === "session_state") {
        const state = (streamEvent.data as { state: Session["state"] }).state;
        return { ...current, state, messages };
      }
      return { ...current, messages };
    });
  }

  function toggleLayout() {
    const next = !focused;
    setFocused(next);
    localStorage.setItem("layout", next ? "focused" : "workspace");
  }

  return (
    <div className={`app ${focused ? "is-focused" : ""}`}>
      <TopBar
        focused={focused}
        onToggleLayout={toggleLayout}
        onOpenSessions={() => setSidebarOpen(true)}
        onOpenContext={() => setRailOpen(true)}
      />
      <div className="shell">
        <SessionSidebar
          open={sidebarOpen}
          sessions={sessions}
          activeId={active?.id}
          onNew={() => void newSession()}
          onSelect={(session) => void selectSession(session)}
          onClose={() => setSidebarOpen(false)}
        />
        <main className="workspace">
          {active ? (
            <>
              <SessionHeader session={active} />
              <Conversation
                messages={active.messages ?? []}
                containerRef={conversationRef}
                onScrollPositionChange={(atBottom) => { stickToBottomRef.current = atBottom; }}
              />
              {error && <div className="error-banner">{error}</div>}
              <Composer busy={sending} onSend={(content) => void send(content)} />
            </>
          ) : (
            <EmptyState onNew={() => void newSession()} />
          )}
        </main>
        <ContextRail open={railOpen} session={active} health={health} onClose={() => setRailOpen(false)} />
      </div>
    </div>
  );
}

function TopBar({ focused, onToggleLayout, onOpenSessions, onOpenContext }: { focused: boolean; onToggleLayout: () => void; onOpenSessions: () => void; onOpenContext: () => void }) {
  return (
    <header className="topbar">
      <button className="mobile-only icon-button" onClick={onOpenSessions} aria-label="Open sessions">☰</button>
      <div className="brand"><span className="brand-mark">G</span><span>Grafana Demo Compiler</span></div>
      <div className="topbar-status"><span className="status-dot good" /> Local workspace <span className="saved">Saved</span></div>
      <div className="topbar-actions">
        <button className="layout-button" onClick={onToggleLayout}>{focused ? "Workspace" : "Focused chat"}</button>
        <button className="mobile-only icon-button" onClick={onOpenContext} aria-label="Open session context">ⓘ</button>
      </div>
    </header>
  );
}

function SessionSidebar({ open, sessions, activeId, onNew, onSelect, onClose }: { open: boolean; sessions: Session[]; activeId?: string; onNew: () => void; onSelect: (session: Session) => void; onClose: () => void }) {
  return (
    <aside className={`sidebar ${open ? "drawer-open" : ""}`}>
      <div className="panel-mobile-header"><strong>Demo sessions</strong><button onClick={onClose}>×</button></div>
      <button className="new-demo" onClick={onNew}><span>＋</span> New demo</button>
      <div className="section-label">Recent sessions</div>
      <nav className="session-list">
        {sessions.map((session) => (
          <button key={session.id} className={`session-item ${session.id === activeId ? "active" : ""}`} onClick={() => onSelect(session)}>
            <span className="session-title">{session.title}</span>
            <span className="session-meta"><span className={`state-dot state-${session.state.toLowerCase()}`} />{session.state}<span>{relativeTime(session.updatedAt)}</span></span>
          </button>
        ))}
        {!sessions.length && <p className="empty-copy">No demos yet.</p>}
      </nav>
    </aside>
  );
}

function SessionHeader({ session }: { session: Session }) {
  return (
    <div className="session-header">
      <div><p className="eyebrow">DEMO SESSION</p><h1>{session.title}</h1></div>
      <span className="state-pill">{session.state}</span>
    </div>
  );
}

function Conversation({ messages, containerRef, onScrollPositionChange }: { messages: Message[]; containerRef: React.RefObject<HTMLElement | null>; onScrollPositionChange: (atBottom: boolean) => void }) {
  function trackScroll(element: HTMLElement) {
    const distanceFromBottom = element.scrollHeight - element.scrollTop - element.clientHeight;
    onScrollPositionChange(distanceFromBottom < 80);
  }
  if (!messages.length) {
    return (
      <section className="conversation welcome" ref={containerRef} onScroll={(event) => trackScroll(event.currentTarget)}>
        <div className="assistant-avatar">G</div>
        <h2>What demo should we build?</h2>
        <p>Tell me who the audience is, what you want them to understand, and any scenario already in mind. We’ll shape it together.</p>
      </section>
    );
  }
  return (
    <section className="conversation" ref={containerRef} onScroll={(event) => trackScroll(event.currentTarget)}>
      {messages.map((message) => message.kind === "activity" ? (
        <div className="activity" key={message.id}><span className="activity-pulse" />{message.content}</div>
      ) : (
        <article className={`message message-${message.role}`} key={message.id}>
          <div className="avatar">{message.role === "user" ? "You" : "G"}</div>
          <div className="message-body">
            <div className="message-label">{message.role === "user" ? "You" : "Demo Compiler"}</div>
            <div className="message-content">
              {message.content ? (
                message.role === "assistant" ? <Markdown remarkPlugins={[remarkGfm]} skipHtml>{message.content}</Markdown> : message.content
              ) : message.status === "streaming" ? <span className="typing">Thinking</span> : ""}
            </div>
            {message.status === "failed" && <div className="message-status">Response interrupted. Your message is saved.</div>}
          </div>
        </article>
      ))}
    </section>
  );
}

function Composer({ busy, onSend }: { busy: boolean; onSend: (content: string) => void }) {
  const [content, setContent] = useState("");
  function submit(event: FormEvent) {
    event.preventDefault();
    const value = content.trim();
    if (!value || busy) return;
    setContent("");
    onSend(value);
  }
  return (
    <form className="composer" onSubmit={submit}>
      <textarea value={content} onChange={(event) => setContent(event.target.value)} onKeyDown={(event) => {
        if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); event.currentTarget.form?.requestSubmit(); }
      }} placeholder="Describe the demo you want to build…" rows={2} />
      <button type="submit" disabled={busy || !content.trim()} aria-label="Send message">↑</button>
      <span className="composer-help">{busy ? "Finishing the current turn · keep typing" : "Enter to send · Shift+Enter for a new line"}</span>
    </form>
  );
}

function ContextRail({ open, session, health, onClose }: { open: boolean; session: Session | null; health: Health; onClose: () => void }) {
  return (
    <aside className={`context-rail ${open ? "drawer-open" : ""}`}>
      <div className="panel-mobile-header"><strong>Session context</strong><button onClick={onClose}>×</button></div>
      <p className="eyebrow">SESSION STATUS</p>
      <div className="status-card"><span className="status-dot good" /><div><strong>{session?.state ?? "No session"}</strong><small>Conversation and decisions are saved locally</small></div></div>
      <p className="eyebrow rail-section">LIVING BRIEF</p>
      {session?.brief ? <BriefPanel brief={session.brief} /> : <div className="brief-empty"><strong>Building shared context</strong><p>The brief, narrative, and architecture will appear after the next exchange.</p></div>}
      <p className="eyebrow rail-section">CONNECTIONS</p>
      <Connection name="SQLite" status={health.sqlite.status} detail="Persistent session store" />
      <Connection name="Amazon Bedrock" status={health.bedrock.status} detail={health.bedrock.modelId || "Model not configured"} />
      <Connection name="Agent telemetry" status={health.agentObservability.status} detail={health.agentObservability.detail} />
    </aside>
  );
}

function Connection({ name, status, detail }: { name: string; status: string; detail: string }) {
  const healthy = status === "connected" || status === "configured";
  return <div className="connection"><span className={`status-dot ${healthy ? "good" : status === "error" ? "bad" : "warn"}`} /><div><strong>{name}</strong><small>{detail}</small></div></div>;
}

function BriefPanel({ brief }: { brief: LivingBrief }) {
  const content = brief.content;
  const coreItems: Array<[string, BriefItem]> = [
    ["Audience", content.audience],
    ["Company", content.company],
    ["Outcome", content.outcome],
    ["Stakes", content.stakes],
    ["Scenario", content.scenario],
    ["Journey", content.journey],
  ];
  return (
    <div className="brief-panel">
      <div className="brief-version"><span>Version {brief.version}</span><span>{relativeTime(brief.updatedAt)}</span></div>
      {!!content.changes.length && <BriefSection title="Changed this turn"><ul>{content.changes.map((change) => <li key={change}>{change}</li>)}</ul></BriefSection>}
      <div className="brief-core">{coreItems.map(([label, item]) => <BriefItemView key={label} label={label} item={item} />)}</div>
      <BriefItems title="Proof points" items={content.proofPoints} />
      <BriefItems title="Included scope" items={content.scope?.included} />
      <BriefItems title="Deliberately excluded" items={content.scope?.excluded} />
      {content.scope?.simulationBoundary?.value && <BriefItemView label="Simulation boundary" item={content.scope.simulationBoundary} />}
      <BriefItems title="Services" items={content.services} />
      <BriefItems title="Telemetry" items={content.telemetry} />
      <BriefItems title="Grafana resources" items={content.grafanaResources} />
      {!!content.narrative.length && <BriefSection title={`Narrative · ${content.narrative.reduce((total, beat) => total + beat.minutes, 0)} min`}><ol className="narrative-list">{content.narrative.map((beat) => <li key={`${beat.stage}-${beat.detail}`}><strong>{beat.stage}</strong><span>{beat.detail}</span><small>{beat.minutes}m</small></li>)}</ol></BriefSection>}
      <ArchitectureSection source={content.mermaid} />
      {!!content.openQuestions.length && <BriefSection title={`Open questions · ${content.openQuestions.length}`} open><ul className="question-list">{content.openQuestions.map((question) => <li key={question}>{question}</li>)}</ul></BriefSection>}
      {!!content.decisions.length && <BriefSection title="Decisions"><ul className="decision-list">{content.decisions.map((decision) => <li key={`${decision.summary}-${decision.evidence}`}><StatusPill status={decision.status} /> <span>{decision.summary}</span></li>)}</ul></BriefSection>}
      <PrototypeCard offer={content.prototypeOffer} />
      {content.acceptance.accepted && <AlignmentCard acceptance={content.acceptance} />}
    </div>
  );
}

function BriefItemView({ label, item }: { label: string; item: BriefItem }) {
  return <div className="brief-item"><div><span>{label}</span><StatusPill status={item.status} /></div><p>{item.value || "Not understood yet"}</p></div>;
}

function BriefItems({ title, items }: { title: string; items?: BriefItem[] | null }) {
  const visibleItems = items ?? [];
  if (!visibleItems.length) return null;
  return <BriefSection title={`${title} · ${visibleItems.length}`}><ul className="brief-item-list">{visibleItems.map((item) => <li key={`${item.name}-${item.value}`}><div><strong>{item.name}</strong><StatusPill status={item.status} /></div><span>{item.value}</span></li>)}</ul></BriefSection>;
}

function BriefSection({ title, open = false, children }: { title: string; open?: boolean; children: React.ReactNode }) {
  return <details className="brief-section" open={open}><summary>{title}</summary><div className="brief-section-body">{children}</div></details>;
}

function StatusPill({ status }: { status: string }) {
  return <span className={`brief-status status-${status}`}>{status}</span>;
}

function ArchitectureSection({ source }: { source: string }) {
  const [expanded, setExpanded] = useState(false);
  useEffect(() => {
    if (!expanded) return;
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") setExpanded(false);
    }
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [expanded]);
  return (
    <>
      <BriefSection title="Architecture">
        {source && <div className="architecture-actions"><button type="button" onClick={() => setExpanded(true)}>Open large</button></div>}
        <MermaidDiagram source={source} />
      </BriefSection>
      {expanded && (
        <div className="architecture-overlay" role="presentation" onMouseDown={(event) => { if (event.currentTarget === event.target) setExpanded(false); }}>
          <section className="architecture-dialog" role="dialog" aria-modal="true" aria-label="Demo architecture">
            <header><div><span>DEMO ARCHITECTURE</span><strong>System view</strong></div><button type="button" onClick={() => setExpanded(false)} aria-label="Close architecture">×</button></header>
            <div className="architecture-canvas"><MermaidDiagram source={source} /></div>
          </section>
        </div>
      )}
    </>
  );
}

function MermaidDiagram({ source }: { source: string }) {
  const id = useId().replace(/[^a-zA-Z0-9_-]/g, "");
  const [svg, setSvg] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    if (!source) { setError(""); return; }
    let cancelled = false;
    void mermaid.render(`brief-${id}`, source).then(({ svg: next }) => {
      if (!cancelled) { setSvg(next); setError(""); }
    }).catch(() => {
      if (!cancelled) setError("The latest Mermaid source could not be rendered. The last valid architecture is still shown.");
    });
    return () => { cancelled = true; };
  }, [id, source]);
  if (!source && !svg) return <p className="brief-muted">Architecture will appear once the service responsibilities are understood.</p>;
  return <div className="mermaid-wrap">{error && <p className="mermaid-error">{error}</p>}{svg && <div className="mermaid-svg" dangerouslySetInnerHTML={{ __html: svg }} />}</div>;
}

function PrototypeCard({ offer }: { offer: LivingBrief["content"]["prototypeOffer"] }) {
  return <div className={`prototype-card ${offer.ready ? "is-ready" : ""}`}><span>{offer.ready ? "PROTOTYPE READY" : "PROTOTYPE NOT READY"}</span><p>{offer.summary || "Keep shaping the audience, outcome, and scenario before building."}</p>{offer.ready && <small>Ask to prototype now, reduce the slice, or continue planning.</small>}</div>;
}

function AlignmentCard({ acceptance }: { acceptance: LivingBrief["content"]["acceptance"] }) {
  const evaluation = acceptance.evaluation;
  const missing = evaluation.missing ?? [];
  const contradictions = evaluation.contradictions ?? [];
  const unnecessaryScope = evaluation.unnecessaryScope ?? [];
  const risks = evaluation.risks ?? [];
  return <div className={`alignment-card result-${evaluation.result}`}><span>REQUIREMENT ALIGNMENT</span><strong>{evaluation.result.replaceAll("_", " ")}</strong><p>{evaluation.explanation}</p>{!!missing.length && <small>Missing: {missing.join("; ")}</small>}{!!contradictions.length && <small>Contradictions: {contradictions.join("; ")}</small>}{!!unnecessaryScope.length && <small>Unnecessary scope: {unnecessaryScope.join("; ")}</small>}{!!risks.length && <small>Risks: {risks.join("; ")}</small>}</div>;
}

function EmptyState({ onNew }: { onNew: () => void }) {
  return <div className="empty-state"><span className="brand-mark large">G</span><h1>Start with the story</h1><p>Create a demo session and describe the outcome you want for your audience.</p><button className="new-demo inline" onClick={onNew}>＋ New demo</button></div>;
}

function relativeTime(value: string) {
  const difference = Date.now() - new Date(value).getTime();
  const minutes = Math.max(0, Math.floor(difference / 60_000));
  if (minutes < 1) return "now";
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

export default App;
