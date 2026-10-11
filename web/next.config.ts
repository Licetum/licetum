import type { NextConfig } from 'next';

const config: NextConfig = {
  // A self-contained server for the container image (apps/web in the Docker build copies .next/standalone).
  output: 'standalone',
  reactStrictMode: true,
  poweredByHeader: false,
  // The admin API is reached through the app's own route handlers (src/app/api/admin), so the browser never
  // sees the token and the API needs no CORS entry for the UI beyond the sign-in redirect.
  typedRoutes: false,
  // Next.js writes an AGENTS.md next to the app by default; this repository keeps its own agent guide.
  agentRules: false,
};

export default config;
