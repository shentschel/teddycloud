import assert from "node:assert/strict";
import test from "node:test";

import { getHealth } from "../../../dist/sdk/index.js";

test("getHealth uses the typed contract path", async () => {
  let requestedUrl = "";
  const result = await getHealth({
    baseUrl: "http://example.invalid/api/",
    fetchImpl: async (url) => {
      requestedUrl = String(url);
      return new Response(JSON.stringify({ status: "ok", version: "0.0.0-dev" }), {
        headers: { "content-type": "application/json" },
        status: 200,
      });
    },
  });

  assert.equal(requestedUrl, "http://example.invalid/api/health");
  assert.deepEqual(result, { status: "ok", version: "0.0.0-dev" });
});
