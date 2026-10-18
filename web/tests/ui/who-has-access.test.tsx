/**
 * @jest-environment jsdom
 */
import { beforeAll, describe, expect, it, jest } from '@jest/globals';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, within } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';

import type { AccessEntry, RuleRef } from '@/api/admin/model';
import { getWhoHasAccessQueryKey, type whoHasAccessResponse } from '@/api/admin/resource/resource';
import { WhoHasAccess } from '@/features/services/who-has-access';
import { createI18n } from '@/i18n';

beforeAll(() => {
  // antd reads the breakpoints and measures the table; jsdom has neither matchMedia nor ResizeObserver, nor the
  // styles of a pseudo-element (the scroll bar).
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
});

const ref = (
  ruleId: string,
  effect: RuleRef['effect'],
  access: string[],
  resourceId: string | null,
): RuleRef => ({
  rule_id: ruleId,
  effect,
  exception: false,
  access,
  anchor: { type: 'campus', resource_id: resourceId },
  applies_to: 'subject mallory',
});

// The API sends null for an inline rule's role and for the group of a rule that names no group.
const entry = (
  e: Pick<AccessEntry, 'rule' | 'via' | 'levels'> & Partial<AccessEntry>,
): AccessEntry =>
  ({
    name: null,
    role: null,
    expires_at: null,
    valid_from: null,
    assumed: false,
    assumed_levels: [],
    people: [],
    people_total: 0,
    who: { kind: 'user', subject: 'mallory', group: null, predicate: [], guarded: false },
    ...e,
  }) as unknown as AccessEntry;

// Renders the lookup of the room M1 with the given entries, as the API answers it (the answer is in the cache already).
function renderLookup(entries: AccessEntry[]) {
  const client = new QueryClient({
    defaultOptions: { queries: { staleTime: Infinity, retry: false } },
  });
  const answer: whoHasAccessResponse = {
    status: 200,
    headers: new Headers(),
    data: {
      type: 'room',
      resource_id: 'M1',
      name: 'Room 1',
      entries,
      dynamic: [],
      people: { enabled: true, registry: 0, limited: false },
    },
  };
  client.setQueryData(getWhoHasAccessQueryKey('s', 't', { resource_id: 'M1' }), answer);
  return render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={createI18n('en')}>
        <WhoHasAccess serviceId="s" typeId="t" resourceId="M1" />
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

describe('WhoHasAccess', () => {
  it('lists every entry of a rule that reaches the resource several times the same way', () => {
    const errors = jest.spyOn(console, 'error').mockImplementation(() => {});
    // A role with two items on the same type: two entries of one rule, both through the whole type.
    renderLookup([
      entry({ rule: ref('r1', 'allow', ['read'], null), via: 'type', levels: ['read'] }),
      entry({ rule: ref('r1', 'allow', ['write'], null), via: 'type', levels: ['write'] }),
    ]);

    const table = within(screen.getByTestId('who-has-access'));
    expect(table.getByText('read')).toBeTruthy();
    expect(table.getByText('write')).toBeTruthy();
    const duplicateKeys = errors.mock.calls.filter((args) => args.join(' ').includes('same key'));
    expect(duplicateKeys).toEqual([]);
    errors.mockRestore();
  });

  it('marks the levels of a denial that are only assumed', () => {
    renderLookup([
      // write reaches for real, read only through a parent not in the cache yet.
      entry({
        rule: ref('d1', 'deny', ['read', 'write'], 'U1'),
        via: 'ancestor',
        levels: ['read', 'write'],
        assumed_levels: ['read'],
      }),
      // Nothing reaches for real.
      entry({
        rule: ref('d2', 'deny', ['read'], 'U1'),
        via: 'ancestor',
        levels: ['read'],
        assumed: true,
        assumed_levels: ['read'],
      }),
    ]);

    expect(screen.getByText('read: parent not known')).toBeTruthy();
    expect(screen.getByText('parent not known')).toBeTruthy();
  });
});
