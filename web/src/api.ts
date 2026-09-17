import type { BriefFocus, BriefThread, Deployment, Health, LivingBrief, Message, PrototypeIteration, Session, StreamEvent } from "./types";

async function json<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
  });
  if (!response.ok) {
    const payload = (await response.json().catch(() => ({}))) as { error?: string };
    throw new Error(payload.error ?? `Request failed (${response.status})`);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

export const api = {
  health: () => json<Health>("/api/health"),
  sessions: () => json<Session[]>("/api/sessions"),
  session: (id: string) => json<Session>(`/api/sessions/${id}`),
  createSession: () =>
    json<Session>("/api/sessions", { method: "POST", body: JSON.stringify({}) }),
  renameSession: (id: string, title: string) =>
    json<void>(`/api/sessions/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ title }),
    }),
  openBriefThread: (sessionId: string, focus: BriefFocus) =>
    json<BriefThread>(`/api/sessions/${sessionId}/brief-threads`, {
      method: "POST",
      body: JSON.stringify({ focus }),
    }),
  confirmBriefThread: (sessionId: string, threadId: string) =>
    json<{ thread: BriefThread; brief: LivingBrief; activity: Message }>(`/api/sessions/${sessionId}/brief-threads/${threadId}/confirm`, {
      method: "POST",
      body: JSON.stringify({}),
    }),
  createPrototype: (sessionId: string) =>
    json<PrototypeIteration>(`/api/sessions/${sessionId}/prototypes`, { method: "POST" }),
  createDeployment: (sessionId: string, organization: string, region: string) =>
    json<Deployment>(`/api/sessions/${sessionId}/deployments`, {
      method: "POST",
      body: JSON.stringify({ target: "local", organization, region }),
    }),
  configureDeploymentToken: (sessionId: string, deploymentId: string, token: string) =>
    json<Deployment>(`/api/sessions/${sessionId}/deployments/${deploymentId}/token`, {
      method: "POST",
      body: JSON.stringify({ token }),
    }),
};

export async function streamMessage(
  sessionId: string,
  content: string,
  onEvent: (event: StreamEvent) => void,
): Promise<void> {
  return stream(`/api/sessions/${sessionId}/messages`, content, onEvent);
}

export async function streamBriefThreadMessage(
  sessionId: string,
  threadId: string,
  content: string,
  onEvent: (event: StreamEvent) => void,
): Promise<void> {
  return stream(`/api/sessions/${sessionId}/brief-threads/${threadId}/messages`, content, onEvent);
}

async function stream(path: string, content: string, onEvent: (event: StreamEvent) => void): Promise<void> {
  const response = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content }),
  });
  return readStream(response, onEvent, new Set(["turn_completed", "error"]));
}

async function readStream(response: Response, onEvent: (event: StreamEvent) => void, terminalEvents: Set<string>): Promise<void> {
  if (!response.ok) {
    const payload = (await response.json().catch(() => ({}))) as { error?: string };
    throw new Error(payload.error ?? `Request failed (${response.status})`);
  }
  if (!response.body) throw new Error("The server did not provide a response stream.");

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { done, value } = await reader.read();
    buffer += decoder.decode(value, { stream: !done });
    const blocks = buffer.split("\n\n");
    buffer = blocks.pop() ?? "";
    for (const block of blocks) {
      const eventLine = block.split("\n").find((line) => line.startsWith("event: "));
      const dataLine = block.split("\n").find((line) => line.startsWith("data: "));
      if (!eventLine || !dataLine) continue;
      const event = eventLine.slice(7);
      onEvent({
        event,
        data: JSON.parse(dataLine.slice(6)) as Message | Record<string, string>,
      });
      if (terminalEvents.has(event)) {
        await reader.cancel();
        return;
      }
    }
    if (done) break;
  }
}
