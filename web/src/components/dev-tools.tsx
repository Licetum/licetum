'use client';

import { Agentation } from 'agentation';

/**
 * Visual feedback for coding agents (agentation): annotate an element in the browser and the note reaches the agent
 * through its local MCP endpoint. Development only — in a production build the branch is removed, and so is the package
 * (it is a devDependency).
 */
export function DevTools() {
  if (process.env.NODE_ENV !== 'development') return null;
  return <Agentation endpoint="http://localhost:4747" appName="Licetum" />;
}
