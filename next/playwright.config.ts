import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/browser",
  outputDir: "./dist/playwright/test-results",
  reporter: "line",
  timeout: 15_000,
  use: {
    baseURL: "http://127.0.0.1:4173",
    browserName: "chromium",
    deviceScaleFactor: 1,
    headless: true,
    viewport: { height: 900, width: 1440 },
  },
  webServer: {
    command:
      "corepack pnpm --filter @teddycloud-next/web exec vite preview --host 127.0.0.1 --port 4173",
    reuseExistingServer: false,
    timeout: 30_000,
    url: "http://127.0.0.1:4173",
  },
});
