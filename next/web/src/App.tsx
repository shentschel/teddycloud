import { useState } from "react";

import { getHealth, type HealthResponse } from "@teddycloud-next/sdk";

export function App() {
  const [health, setHealth] = useState<HealthResponse | null>(null);

  async function loadHealth() {
    setHealth(await getHealth({ baseUrl: "/api" }));
  }

  return (
    <main>
      <h1>TeddyCloud Next</h1>
      <p>PI-03 build scaffold</p>
      <button type="button" onClick={loadHealth}>
        Check typed client
      </button>
      {health ? <output>{`${health.status} (${health.version})`}</output> : null}
    </main>
  );
}
