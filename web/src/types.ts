export type SessionState = "Draft" | "Ready" | "Generated" | "Running" | "Verified";

export interface ContextUsage {
  role: string;
  estimatedTokens: number;
  maxInputTokens: number;
  truncated: boolean;
  updatedAt: string;
}

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
  brief?: LivingBrief;
  prototypes?: PrototypeIteration[];
  deployments?: Deployment[];
}

export interface Deployment {
  id: string;
  sessionId: string;
  prototypeIterationId: string;
  target: "local";
  region: string;
  stackName: string;
  stackSlug: string;
  stackUrl?: string;
  otlpEndpoint?: string;
  instanceId?: string;
  status: "provisioning" | "needs_token" | "starting" | "running" | "verifying" | "verified" | "failed" | "interrupted";
  progress: string[];
  error?: string;
  createdAt: string;
  updatedAt: string;
}

export interface PrototypeIteration {
  id: string;
  sessionId: string;
  number: number;
  briefVersion: number;
  status: "generating" | "complete" | "failed";
  rootPath: string;
  summary: string;
  artifacts: Array<{ path: string; size: number }>;
  checks: Array<{ name: string; status: "passed" | "failed"; detail: string }>;
  progress: string[];
  error?: string;
  createdAt: string;
  updatedAt: string;
}

export type BriefStatus = "unknown" | "proposed" | "confirmed";

export interface BriefItem {
  id: string;
  name: string;
  value: string;
  status: BriefStatus;
}

export interface BriefFocus {
  topicId: string;
  label: string;
  value: string;
  status: BriefStatus;
}

export interface BriefThread {
  id: string;
  sessionId: string;
  focus: BriefFocus;
  candidateValue: string;
  state: "draft" | "confirmed";
  createdAt: string;
  updatedAt: string;
  messages: Message[];
}

export interface NarrativeBeat {
  stage: string;
  detail: string;
  minutes: number;
}

export interface LivingBrief {
  version: number;
  updatedAt: string;
  content: {
    changes: string[];
    audience: BriefItem;
    company: BriefItem;
    outcome: BriefItem;
    stakes: BriefItem;
    scenario: BriefItem;
    journey: BriefItem;
    proofPoints: BriefItem[];
    scope: {
      included: BriefItem[];
      excluded: BriefItem[];
      simulationBoundary: BriefItem;
    };
    services: BriefItem[];
    telemetry: BriefItem[];
    grafanaResources: BriefItem[];
    narrative: NarrativeBeat[];
    mermaid: string;
    openQuestions: string[];
    decisions: Array<{ summary: string; status: BriefStatus; evidence: string }>;
    prototypeOffer: {
      ready: boolean;
      summary: string;
      included: string[];
      excluded: string[];
      realComponents: string[];
      simulatedComponents: string[];
      services: string[];
      scenario: string;
      telemetry: string[];
      grafanaResources: string[];
      assumptions: string[];
    };
    acceptance: {
      accepted: boolean;
      evidence: string;
      evaluation: {
        result: "" | "meets" | "partially_meets" | "does_not_meet";
        explanation: string;
        missing: string[];
        contradictions: string[];
        unnecessaryScope: string[];
        risks: string[];
        evidence: string[];
      };
    };
  };
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
