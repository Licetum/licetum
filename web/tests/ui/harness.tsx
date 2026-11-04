import { afterEach, jest } from '@jest/globals';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { configure, render, waitFor, within } from '@testing-library/react';
import { Modal } from 'antd';
import type { ReactElement } from 'react';
import { I18nextProvider } from 'react-i18next';

import { createI18n } from '@/i18n';

/*
 * What the tests of the screens share: a browser as antd expects it, the admin API answered from a table, and the providers
 * the screens are rendered in. The screens run as they do in the application — the generated client, TanStack Query, the
 * dictionaries — and only the network is replaced.
 */

// A confirmation opened with Modal.confirm lives outside the tree the test renders: close it, or the next test finds it.
afterEach(() => Modal.destroyAll());

/** jsdom has neither matchMedia nor ResizeObserver, nor the styles of a pseudo-element (the scroll bar of a table). */
export function setupBrowser() {
  // Looking for a role asks the styles of every element whether it is hidden, which jsdom does slowly; nothing in these
  // screens hides an element by style.
  configure({ defaultHidden: true });
  const computedStyle = window.getComputedStyle.bind(window);
  window.getComputedStyle = (element: Element) => computedStyle(element);
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
  // React's scheduler falls back to a MessageChannel where there is no setImmediate, which is the case in jsdom.
  globalThis.MessageChannel ??= class {
    port1: { onmessage: ((event: { data: unknown }) => void) | null } = { onmessage: null };
    port2 = { postMessage: () => void setTimeout(() => this.port1.onmessage?.({ data: null }), 0) };
  } as unknown as typeof MessageChannel;
  document.documentElement.lang = 'en';
}

/** The drawer or dialog that has this text (its title, usually) and what is queried inside it. */
export async function dialogOf(text: string | RegExp) {
  return waitFor(() => {
    const found = [...document.querySelectorAll<HTMLElement>('[role="dialog"]')].find((d) =>
      typeof text === 'string' ? d.textContent?.includes(text) : text.test(d.textContent ?? ''),
    );
    if (!found) throw new Error(`no dialog with ${String(text)}`);
    return within(found);
  });
}

/** The button that has this text (a query by role costs seconds on a screen with tables and drawers; the text is quick). */
export function buttonOf(scope: { getByText: (text: string) => HTMLElement }, text: string) {
  return scope.getByText(text).closest('button') as HTMLButtonElement;
}

/** A request the screen made to the admin API: the method, the path under `/api/admin` (with its query) and the JSON body. */
export interface Call {
  method: string;
  path: string;
  body: unknown;
}

/** An answer with a status other than 200. */
export const failure = (status: number, detail: string) => ({
  __status: status,
  body: { detail, extra: { type: 'generic', code: 'test' } },
});

type Answer = unknown | ((call: Call) => unknown);

/**
 * Answers the admin API from a table keyed by `"METHOD /path"` (the query string is not part of the key). An answer is the
 * body of a 200, a `failure(...)`, or a function of the call returning either. A route that is not in the table is a 404 and
 * is reported, so a screen that asks for more than the test expected does not pass silently.
 */
export function mockApi(routes: Record<string, Answer>) {
  const calls: Call[] = [];
  const unrouted: string[] = [];
  const fetchMock = jest.fn(async (input: unknown, init?: { method?: string; body?: unknown }) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    const path = url.replace(/^\/api\/admin/, '');
    const call: Call = {
      method,
      path,
      body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
    };
    calls.push(call);
    const key = `${method} ${path.split('?')[0]}`;
    if (!(key in routes)) {
      unrouted.push(key);
      return reply(404, { detail: `no route in the test: ${key}` });
    }
    const answer = routes[key];
    const result = typeof answer === 'function' ? (answer as (c: Call) => unknown)(call) : answer;
    if (result && typeof result === 'object' && '__status' in result) {
      const { __status, body } = result as { __status: number; body: unknown };
      return reply(__status, body);
    }
    return reply(200, result);
  });
  globalThis.fetch = fetchMock as unknown as typeof fetch;
  return {
    calls,
    unrouted,
    /** The calls made so far with the method to the path (a prefix of the full path, query included). */
    to: (method: string, path: string) =>
      calls.filter((c) => c.method === method && c.path.startsWith(path)),
  };
}

function reply(status: number, body: unknown) {
  return {
    ok: status < 400,
    status,
    headers: new Headers(),
    text: async () => JSON.stringify(body),
    json: async () => body,
  };
}

/** Renders the screen with a fresh query cache that never retries, and the English dictionary. */
export function renderScreen(ui: ReactElement) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0, gcTime: Infinity } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={createI18n('en')}>{ui}</I18nextProvider>
    </QueryClientProvider>,
  );
  return { client, ...view };
}
