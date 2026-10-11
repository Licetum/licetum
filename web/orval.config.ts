import { defineConfig } from 'orval';

// The API client is generated from the committed OpenAPI schema of the admin API and is not committed itself
// (src/api is ignored): `pnpm orval` after every change of ../admin-api/schema/admin.yaml.
export default defineConfig({
  admin: {
    input: '../admin-api/schema/admin.yaml',
    output: {
      mode: 'tags-split',
      target: 'src/api/admin/endpoints.ts',
      schemas: 'src/api/admin/model',
      client: 'react-query',
      httpClient: 'fetch',
      override: {
        mutator: { path: 'orval/mutator.ts', name: 'customFetch' },
      },
    },
    hooks: { afterAllFilesWrite: 'oxfmt' },
  },
});
