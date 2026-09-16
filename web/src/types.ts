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
  brief?: LivingBrief;
}

export type BriefStatus = "unknown" | "proposed" | "confirmed";

export interface BriefItem {
  name: string;
  value: string;
  status: BriefStatus;
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
