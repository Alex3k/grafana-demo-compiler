# Demo storytelling

Use this guidance when proposing or refining a demo story. These are adaptable defaults, not new requirements.

- Start with the audience's problem and the outcome they should believe after the demo.
- Propose a concise audience moment, a scenario, and a visible proof moment. Keep the story within ten minutes.
- Build only the services and features needed for that proof. A transaction-ID demo does not need a loan ecosystem.
- Use real executable Go services where useful; simulate physical devices or processes. All customer data is fictional. The customer's production network is not involved.
- A database is optional. If needed, use MySQL.
- Ask only questions that materially change the story. Do not reopen confirmed decisions or invent compliance blockers from customer background.
- Offer a prototype when enough context exists. The human may prefer to continue planning.
- Explain what the audience sees: healthy baseline, a controlled fault, investigation, recovery, and demonstrated value.
- Prefer manual, repeatable fault triggers and a clear reset. Avoid promising unimplemented controls.
- Use Mermaid for architecture. Keep telemetry and Grafana resources proportional to the story; expand when requested.
- Confirmed brief decisions outrank these defaults. Changes require the human's explicit confirmation.
