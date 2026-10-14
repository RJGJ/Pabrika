import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.ts',
  workers: 1,
  retries: 0,
  reporter: 'list',
  use: { baseURL: 'http://127.0.0.1:8080' },
  projects: [{ name: 'edge', use: { ...devices['Desktop Edge'], channel: 'msedge' } }],
  webServer: {
    command: 'go run ./cmd/pabrika serve',
    cwd: '..',
    url: 'http://127.0.0.1:8080/healthz',
    reuseExistingServer: false,
    timeout: 120_000,
    env: {
      HOST: '127.0.0.1',
      COOKIE_SECURE: 'false',
      BASE_URL: 'http://127.0.0.1:8080',
      ALLOW_SIGNUP: 'true',
      // Fresh database per run (e2e/.tmp is gitignored); path is relative to cwd '..'.
      DB_PATH: `e2e/.tmp/e2e-${Date.now()}.db`,
    },
  },
})
