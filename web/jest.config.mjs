import nextJest from 'next/jest.js';

// next/jest compiles the sources with the Next.js compiler and maps CSS and images to stubs, so the tests see the
// modules as the app does. Tests of the interface opt into the DOM with a `@jest-environment jsdom` docblock.
const createJestConfig = nextJest({ dir: './' });

// jose ships only ES modules: it is compiled for the tests, the other dependencies are used as they are.
const transformIgnorePatterns = [
  '/node_modules/(?!\\.pnpm/)(?!jose/)',
  '/node_modules/\\.pnpm/(?!jose@)',
  '\\.module\\.(css|sass|scss)$',
];

/** @type {import('jest').Config} */
const config = {
  testEnvironment: 'node',
  testMatch: ['<rootDir>/tests/**/*.test.{ts,tsx}'],
  moduleNameMapper: {
    '^@/(.*)$': '<rootDir>/src/$1',
    // `server-only` throws outside of the Next.js server build; the modules it guards are tested directly.
    '^server-only$': '<rootDir>/tests/server-only-stub.ts',
  },
  clearMocks: true,
};

const nextConfig = createJestConfig(config);

export default async () => {
  const resolved = await nextConfig();
  // next/jest adds its own patterns (among them every node_modules), so the list is replaced as a whole.
  resolved.transformIgnorePatterns = transformIgnorePatterns;
  return resolved;
};
