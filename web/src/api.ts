import type { Health, Message, Session, StreamEvent } from "./types";

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
};

export async function streamMessage(
  sessionId: string,
  content: string,
  onEvent: (event: StreamEvent) => void,
): Promise<void> {
  const response = await fetch(`/api/sessions/${sessionId}/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content }),
  });
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
      if (event === "turn_completed" || event === "error") {
        await reader.cancel();
        return;
      }
    }
    if (done) break;
  }
}
