import { defineConfig, devices } from '@playwright/test'

/**
 * GUI smoke tests over the mock preview (npm run dev:mock): the in-repo fake
 * runtime (src/dev/mockRuntime.ts + mockData.ts) answers every backend binding,
 * so the whole app runs in a plain browser and the specs can drive it black-box.
 *
 * The webServer reuses the existing dev:mock script on a fixed port (5174, kept
 * clear of the interactive dev server's 5173) so `npm run test:e2e` needs no
 * extra orchestration. Chromium only: the smoke tier guards reachable app
 * behavior, not cross-browser layout.
 */
export default defineConfig({
  testDir: './e2e',
  // *.e2e.ts instead of the default *.spec.ts: vitest's default include would
  // otherwise collect these files too (frontend has no vitest exclude for the
  // e2e dir), and Playwright's own runner is the only one that may drive them.
  testMatch: '**/*.e2e.ts',
  // Streaming chat relies on the mock's paced SSE; keep generous room.
  timeout: 30_000,
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: 'http://localhost:5174',
    // The mock's canned chat reply switches on navigator.language; pin it so
    // the streamed answer text is deterministic across dev machines and CI.
    locale: 'en-US',
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: {
    command: 'npm run dev:mock -- --port 5174 --strictPort',
    url: 'http://localhost:5174',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
})
