import { FormEvent, useEffect, useId, useRef, useState } from "react";
import mermaid from "mermaid";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { api, streamBriefThreadMessage, streamMessage } from "./api";
import type { BriefFocus, BriefItem, BriefThread, Health, LivingBrief, Message, PrototypeIteration, Session, StreamEvent } from "./types";

mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: "dark" });

const EMPTY_HEALTH: Health = {
  status: "loading",
  sqlite: { status: "checking" },
  bedrock: { status: "checking", modelId: "" },
  agentObservability: { status: "checking", detail: "Checking connection" },
};

interface QueuedMessage {
  id: string;
  content: string;
}

function App() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [active, setActive] = useState<Session | null>(null);
  const [health, setHealth] = useState<Health>(EMPTY_HEALTH);
  const [focused, setFocused] = useState(() => localStorage.getItem("layout") === "focused");
  const [sending, setSending] = useState(false);
  const [messageQueues, setMessageQueues] = useState<Record<string, QueuedMessage[]>>({});
  const [error, setError] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [railOpen, setRailOpen] = useState(false);
  const [topicThread, setTopicThread] = useState<BriefThread | null>(null);
  const [topicSending, setTopicSending] = useState(false);
  const [topicConfirming, setTopicConfirming] = useState(false);
  const [topicError, setTopicError] = useState("");
  const [showBuildDetails, setShowBuildDetails] = useState(() => localStorage.getItem("showBuildDetails") !== "false");
  const conversationRef = useRef<HTMLElement>(null);
  const stickToBottomRef = useRef(true);
  const sendingRef = useRef(false);
  const activePrototype = active?.prototypes?.[0];
  const prototypeBusy = activePrototype?.status === "generating";
  const prototypeActivity = activePrototype?.progress ?? [];

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

  useEffect(() => {
    if (!active || sendingRef.current) return;
    const next = messageQueues[active.id]?.[0];
    if (!next) return;
    setMessageQueues((current) => ({ ...current, [active.id]: (current[active.id] ?? []).filter((message) => message.id !== next.id) }));
    void sendNow(active.id, next.content);
  }, [active?.id, messageQueues, sending]);

  useEffect(() => {
    if (!active || activePrototype?.status !== "generating") return;
    const sessionId = active.id;
    const iterationId = activePrototype.id;
    let stopped = false;
    let timer = 0;
    const poll = async () => {
      try {
        const next = await api.session(sessionId);
        if (stopped) return;
        setActive((current) => current?.id === sessionId ? next : current);
        if (next.prototypes?.[0]?.id === iterationId && next.prototypes[0].status === "generating") {
          timer = window.setTimeout(poll, 1200);
        } else {
          await refreshSessions();
        }
      } catch (reason) {
        if (!stopped) {
          setError(reason instanceof Error ? reason.message : "Could not refresh prototype progress.");
          timer = window.setTimeout(poll, 2500);
        }
      }
    };
    timer = window.setTimeout(poll, 500);
    return () => { stopped = true; window.clearTimeout(timer); };
  }, [active?.id, activePrototype?.id, activePrototype?.status]);

  async function refreshSessions() {
    const items = await api.sessions();
    setSessions(items);
  }

  async function selectSession(session: Session) {
    setError("");
    stickToBottomRef.current = true;
    setActive(await api.session(session.id));
    setTopicThread(null);
    setSidebarOpen(false);
  }

  async function newSession() {
    setError("");
    stickToBottomRef.current = true;
    const session = await api.createSession();
    setActive({ ...session, messages: [] });
    setTopicThread(null);
    await refreshSessions();
    setSidebarOpen(false);
  }

  function send(content: string) {
    if (!active) return;
    if (sendingRef.current) {
      const queued = { id: crypto.randomUUID(), content };
      setMessageQueues((current) => ({ ...current, [active.id]: [...(current[active.id] ?? []), queued] }));
      return;
    }
    void sendNow(active.id, content);
  }

  function removeQueuedMessage(sessionId: string, messageId: string) {
    setMessageQueues((current) => ({ ...current, [sessionId]: (current[sessionId] ?? []).filter((message) => message.id !== messageId) }));
  }

  async function sendNow(sessionId: string, content: string) {
    if (sendingRef.current) return;
    sendingRef.current = true;
    stickToBottomRef.current = true;
    setSending(true);
    setError("");
    try {
      await streamMessage(sessionId, content, (streamEvent) => applyStreamEvent(sessionId, streamEvent));
      await refreshSessions();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "The message could not be sent.");
    } finally {
      sendingRef.current = false;
      setSending(false);
      void api.health().then(setHealth).catch(() => undefined);
    }
  }

  async function openTopic(focus: BriefFocus) {
    if (!active) return;
    setTopicError("");
    try {
      setTopicThread(await api.openBriefThread(active.id, focus));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "The focused chat could not be opened.");
    }
  }

  async function sendTopic(content: string) {
    if (!active || !topicThread || topicSending || topicConfirming) return;
    setTopicSending(true);
    setTopicError("");
    try {
      await streamBriefThreadMessage(active.id, topicThread.id, content, applyTopicStreamEvent);
    } catch (reason) {
      setTopicError(reason instanceof Error ? reason.message : "The focused message could not be sent.");
    } finally {
      setTopicSending(false);
    }
  }

  async function confirmTopic() {
    if (!active || !topicThread || topicSending || topicConfirming) return;
    setTopicConfirming(true);
    setTopicError("");
    try {
      const result = await api.confirmBriefThread(active.id, topicThread.id);
      setActive((current) => current ? { ...current, brief: result.brief, updatedAt: result.brief.updatedAt, messages: [...(current.messages ?? []), result.activity] } : current);
      setTopicThread(null);
      await refreshSessions();
    } catch (reason) {
      setTopicError(reason instanceof Error ? reason.message : "The topic could not be confirmed.");
    } finally {
      setTopicConfirming(false);
    }
  }

  async function buildPrototype() {
    if (!active || prototypeBusy) return;
    const sessionId = active.id;
    setError("");
    try {
      const iteration = await api.createPrototype(sessionId);
      setActive((current) => current && current.id === sessionId
        ? { ...current, prototypes: [iteration, ...(current.prototypes ?? []).filter((item) => item.id !== iteration.id)] }
        : current);
      await refreshSessions();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "The prototype could not be generated.");
    }
  }

  function applyTopicStreamEvent(streamEvent: StreamEvent) {
    if (streamEvent.event === "turn_completed" || streamEvent.event === "error") setTopicSending(false);
    setTopicThread((current) => {
      if (!current) return current;
      const messages = [...current.messages];
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
        setTopicError(failure.message);
      } else if (streamEvent.event === "candidate_updated") {
        return { ...current, candidateValue: (streamEvent.data as { value: string }).value, messages };
      }
      return { ...current, messages };
    });
  }

  function applyStreamEvent(sessionId: string, streamEvent: StreamEvent) {
    setActive((current) => {
      if (!current || current.id !== sessionId) return current;
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
              {prototypeBusy && <BuildActivity steps={prototypeActivity} detailed={showBuildDetails} onToggle={() => {
                const next = !showBuildDetails;
                setShowBuildDetails(next);
                localStorage.setItem("showBuildDetails", String(next));
              }} />}
              {error && <div className="error-banner">{error}</div>}
              <Composer
                busy={sending}
                queued={messageQueues[active.id] ?? []}
                onSend={send}
                onRemoveQueued={(messageId) => removeQueuedMessage(active.id, messageId)}
              />
            </>
          ) : (
            <EmptyState onNew={() => void newSession()} />
          )}
        </main>
        <ContextRail
          open={railOpen}
          session={active}
          health={health}
          onClose={() => setRailOpen(false)}
          onFocusTopic={(focus) => void openTopic(focus)}
          prototypeBusy={prototypeBusy}
          prototypeProgress={prototypeActivity[prototypeActivity.length - 1] ?? ""}
          onBuildPrototype={() => void buildPrototype()}
        />
        {topicThread && active && (
          <TopicChat
            thread={topicThread}
            busy={topicSending}
            confirming={topicConfirming}
            error={topicError}
            onSend={(content) => void sendTopic(content)}
            onConfirm={() => void confirmTopic()}
            onClose={() => setTopicThread(null)}
          />
        )}
      </div>
    </div>
  );
}

function BuildActivity({ steps, detailed, onToggle }: { steps: string[]; detailed: boolean; onToggle: () => void }) {
  const visibleSteps = detailed ? steps : steps.slice(-1);
  return <section className="build-activity" aria-live="polite">
    <div className="build-activity-header"><span className="activity-pulse" /><div><strong>Building your demo</strong><small>{detailed ? "Milestones, tool activity, and validation" : "Current build status"}</small></div><button type="button" onClick={onToggle}>{detailed ? "Hide details" : "Show details"}</button></div>
    <ol>{visibleSteps.map((step, index) => {
      const active = !detailed || index === visibleSteps.length - 1;
      return <li key={`${index}-${step}`} className={active ? "is-active" : "is-complete"}><span>{active ? "●" : "✓"}</span>{step}</li>;
    })}</ol>
  </section>;
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

function Composer({ busy, queued, onSend, onRemoveQueued }: { busy: boolean; queued: QueuedMessage[]; onSend: (content: string) => void; onRemoveQueued: (messageId: string) => void }) {
  const [content, setContent] = useState("");
  function submit(event: FormEvent) {
    event.preventDefault();
    const value = content.trim();
    if (!value) return;
    setContent("");
    onSend(value);
  }
  return (
    <div className="composer-area">
      {queued.length > 0 && (
        <section className="message-queue" aria-label="Queued messages">
          <div className="message-queue-header"><strong>Queued</strong><span>{queued.length}</span></div>
          {queued.map((message, index) => (
            <div className="queued-message" key={message.id}>
              <span className="queued-position">{index + 1}</span>
              <p>{message.content}</p>
              <button type="button" onClick={() => onRemoveQueued(message.id)} aria-label={`Remove queued message ${index + 1}`}>×</button>
            </div>
          ))}
        </section>
      )}
      <form className="composer" onSubmit={submit}>
        <textarea value={content} onChange={(event) => setContent(event.target.value)} onKeyDown={(event) => {
          if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); event.currentTarget.form?.requestSubmit(); }
        }} placeholder={busy ? "Add a message to the queue…" : "Describe the demo you want to build…"} rows={2} />
        <button type="submit" disabled={!content.trim()} aria-label={busy ? "Queue message" : "Send message"}>↑</button>
        <span className="composer-help">{busy ? "Enter to queue · Shift+Enter for a new line" : "Enter to send · Shift+Enter for a new line"}</span>
      </form>
    </div>
  );
}

function TopicChat({ thread, busy, confirming, error, onSend, onConfirm, onClose }: { thread: BriefThread; busy: boolean; confirming: boolean; error: string; onSend: (content: string) => void; onConfirm: () => void; onClose: () => void }) {
  const [content, setContent] = useState("");
  const threadRef = useRef<HTMLDivElement>(null);
  const messages = thread.messages;
  const focus = thread.focus;
  useEffect(() => {
    threadRef.current?.scrollTo({ top: threadRef.current.scrollHeight, behavior: "auto" });
  }, [messages]);
  function submit(event: FormEvent) {
    event.preventDefault();
    const value = content.trim();
    if (!value || busy) return;
    setContent("");
    onSend(value);
  }
  return (
    <aside className="topic-chat" aria-label={`Discuss ${focus.label}`}>
      <header>
        <div><span>FOCUSED BRIEF CHAT</span><strong>{focus.label}</strong></div>
        <button type="button" onClick={onClose} aria-label="Close focused chat">×</button>
      </header>
      <div className="topic-context">
        <StatusPill status={focus.status} />
        <p>{focus.value}</p>
        <small>This draft is isolated from the main session until you confirm it.</small>
      </div>
      <div className="topic-thread" ref={threadRef}>
        {!messages.length && <div className="topic-empty"><strong>What would you like to change?</strong><p>Respond to this topic, challenge the proposal, or add missing context.</p></div>}
        {messages.map((message) => message.kind === "activity" ? (
          <div className="topic-activity" key={message.id}><span className="activity-pulse" />{message.content}</div>
        ) : (
          <article className={`topic-message topic-message-${message.role}`} key={message.id}>
            <span>{message.role === "user" ? "You" : "Demo Compiler"}</span>
            <div>{message.role === "assistant" ? <Markdown remarkPlugins={[remarkGfm]} skipHtml>{message.content}</Markdown> : message.content}</div>
          </article>
        ))}
      </div>
      {error && <div className="topic-error">{error}</div>}
      <div className="topic-confirm-bar">
        <div><strong>Ready to lock this in?</strong><small>Only the agreed result is applied to the living brief.</small></div>
        <button type="button" onClick={onConfirm} disabled={busy || confirming || !thread.candidateValue.trim()}>{confirming ? "Applying…" : "Confirm and apply"}</button>
      </div>
      <form className="topic-composer" onSubmit={submit}>
        <textarea value={content} onChange={(event) => setContent(event.target.value)} onKeyDown={(event) => {
          if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); event.currentTarget.form?.requestSubmit(); }
        }} placeholder={`Discuss ${focus.label.toLowerCase()}…`} rows={3} autoFocus />
        <button type="submit" disabled={busy || !content.trim()} aria-label="Send focused message">↑</button>
        <small>{busy ? "Refining this draft · keep typing" : "Enter to send · isolated until confirmed"}</small>
      </form>
    </aside>
  );
}

function ContextRail({ open, session, health, onClose, onFocusTopic, prototypeBusy, prototypeProgress, onBuildPrototype }: { open: boolean; session: Session | null; health: Health; onClose: () => void; onFocusTopic: (focus: BriefFocus) => void; prototypeBusy: boolean; prototypeProgress: string; onBuildPrototype: () => void }) {
  return (
    <aside className={`context-rail ${open ? "drawer-open" : ""}`}>
      <div className="panel-mobile-header"><strong>Session context</strong><button onClick={onClose}>×</button></div>
      <p className="eyebrow">SESSION STATUS</p>
      <div className="status-card"><span className="status-dot good" /><div><strong>{session?.state ?? "No session"}</strong><small>Conversation and decisions are saved locally</small></div></div>
      {session?.brief && <PrototypeCard offer={session.brief.content.prototypeOffer} iteration={session.prototypes?.[0]} busy={prototypeBusy} progress={prototypeProgress} onBuild={onBuildPrototype} />}
      <p className="eyebrow rail-section">LIVING BRIEF</p>
      {session?.brief ? <BriefPanel brief={session.brief} onFocusTopic={onFocusTopic} /> : <div className="brief-empty"><strong>Building shared context</strong><p>The brief, narrative, and architecture will appear after the next exchange.</p></div>}
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

function BriefPanel({ brief, onFocusTopic }: { brief: LivingBrief; onFocusTopic: (focus: BriefFocus) => void }) {
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
      <div className="brief-core">{coreItems.map(([label, item]) => <BriefItemView key={label} label={label} item={item} onFocusTopic={onFocusTopic} />)}</div>
      <BriefItems title="Proof points" items={content.proofPoints} onFocusTopic={onFocusTopic} />
      <BriefItems title="Included scope" items={content.scope?.included} onFocusTopic={onFocusTopic} />
      <BriefItems title="Deliberately excluded" items={content.scope?.excluded} onFocusTopic={onFocusTopic} />
      {content.scope?.simulationBoundary?.value && <BriefItemView label="Simulation boundary" item={content.scope.simulationBoundary} onFocusTopic={onFocusTopic} />}
      <BriefItems title="Services" items={content.services} onFocusTopic={onFocusTopic} />
      <BriefItems title="Telemetry" items={content.telemetry} onFocusTopic={onFocusTopic} />
      <BriefItems title="Grafana resources" items={content.grafanaResources} onFocusTopic={onFocusTopic} />
      {!!content.narrative.length && <BriefSection title={`Narrative · ${content.narrative.reduce((total, beat) => total + beat.minutes, 0)} min`}><ol className="narrative-list">{content.narrative.map((beat) => <li key={`${beat.stage}-${beat.detail}`}><strong>{beat.stage}</strong><span>{beat.detail}</span><small>{beat.minutes}m</small></li>)}</ol></BriefSection>}
      <ArchitectureSection source={content.mermaid} />
      {!!content.openQuestions.length && <BriefSection title={`Open questions · ${content.openQuestions.length}`} open><ul className="question-list">{content.openQuestions.map((question) => <li key={question}>{question}</li>)}</ul></BriefSection>}
      {!!content.decisions.length && <BriefSection title="Decisions"><ul className="decision-list">{content.decisions.map((decision) => <li key={`${decision.summary}-${decision.evidence}`}><StatusPill status={decision.status} /> <span>{decision.summary}</span></li>)}</ul></BriefSection>}
      {content.acceptance.accepted && <AlignmentCard acceptance={content.acceptance} />}
    </div>
  );
}

function BriefItemView({ label, item, onFocusTopic }: { label: string; item: BriefItem; onFocusTopic: (focus: BriefFocus) => void }) {
  const focusable = item.status === "proposed" || item.status === "confirmed";
  return <button type="button" className={`brief-item ${focusable ? "is-focusable" : ""}`} disabled={!focusable} onClick={() => focusable && onFocusTopic({ label, value: item.value, status: item.status })}><div><span>{label}</span><StatusPill status={item.status} /></div><p>{item.value || "Not understood yet"}</p>{focusable && <small>Discuss this topic →</small>}</button>;
}

function BriefItems({ title, items, onFocusTopic }: { title: string; items?: BriefItem[] | null; onFocusTopic: (focus: BriefFocus) => void }) {
  const visibleItems = items ?? [];
  if (!visibleItems.length) return null;
  return <BriefSection title={`${title} · ${visibleItems.length}`}><ul className="brief-item-list">{visibleItems.map((item) => {
    const focusable = item.status === "proposed" || item.status === "confirmed";
    return <li key={`${item.name}-${item.value}`}><button type="button" disabled={!focusable} onClick={() => focusable && onFocusTopic({ label: `${title}: ${item.name}`, value: item.value, status: item.status })}><div><strong>{item.name}</strong><StatusPill status={item.status} /></div><span>{item.value}</span>{focusable && <small>Discuss →</small>}</button></li>;
  })}</ul></BriefSection>;
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

function PrototypeCard({ offer, iteration, busy, progress, onBuild }: { offer: LivingBrief["content"]["prototypeOffer"]; iteration?: PrototypeIteration; busy: boolean; progress: string; onBuild: () => void }) {
  const artifacts = iteration?.artifacts ?? [];
  const checks = iteration?.checks ?? [];
  return <div className={`prototype-card ${offer.ready ? "is-ready" : ""}`}>
    <span>{offer.ready ? "PROTOTYPE READY" : "PROTOTYPE NOT READY"}</span>
    <p>{offer.summary || "Keep shaping the audience, outcome, and scenario before building."}</p>
    {offer.ready && <button type="button" onClick={onBuild} disabled={busy}>{busy ? "Building…" : iteration ? "Build next iteration" : "Build prototype"}</button>}
    {busy && <small className="prototype-progress"><span className="activity-pulse" />{progress}</small>}
    {iteration && <div className={`prototype-result result-${iteration.status}`}>
      <strong>Iteration {iteration.number} · {iteration.status}</strong>
      {iteration.summary && <p>{iteration.summary}</p>}
      <small>{artifacts.length} files · {checks.filter((check) => check.status === "passed").length}/{checks.length} checks passed</small>
      {iteration.error && <small className="prototype-error">{iteration.error}</small>}
      <details><summary>Validation and files</summary><ul>{checks.map((check) => <li key={check.name} className={`check-${check.status}`}><strong>{check.name}</strong><span>{check.detail}</span></li>)}{artifacts.map((artifact) => <li key={artifact.path}><code>{artifact.path}</code></li>)}</ul></details>
      <small className="prototype-path">Saved at {iteration.rootPath}</small>
    </div>}
  </div>;
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
