export type SessionState = "Draft" | "Ready" | "Generated" | "Running" | "Verified";

export interface Message {
  id: string;
  sessionId: string;
  role: "user" | "assistant" | "system";
  kind: "message" | "activity";
  content: string;
  status: "streaming" | "complete" | "failed" | "interrupted";
  createdAt: string;
}

export interface Session {
  id: string;
  title: string;
  state: SessionState;
  createdAt: string;
  updatedAt: string;
  messages?: Message[];
}

export interface Health {
  status: string;
  sqlite: { status: string };
  bedrock: { status: string; modelId: string };
  agentObservability: { status: string; detail: string };
}

export interface StreamEvent {
  event: string;
  data: unknown;
}
