import { defineConfig } from '@playwright/test';

// End-to-end tests run against a running stack (the admin API, an identity provider, this app):
//   E2E_BASE_URL=http://localhost:3000 E2E_USER=admin@example.com E2E_PASSWORD=password pnpm e2e
// They create what they need under unique names and remove it again, so they can be repeated.
export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:3000',
    locale: 'en-US',
    colorScheme: 'light',
    trace: 'retain-on-failure',
  },
});
