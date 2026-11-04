/**
 * @jest-environment jsdom
 */
import { beforeAll, beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';

import { Providers } from '@/components/providers';
import { CheckWorkspace } from '@/features/check/check-workspace';
import { VisibleAccess } from '@/features/check/visible-access';
import { GrantProvider } from '@/features/grant/grant-context';
import { PersonPage } from '@/features/people/person-page';
import { AccessProvider } from '@/lib/access';

import { navigation } from '../next-navigation-stub';
import { buttonOf, mockApi, renderScreen, setupBrowser, type Call } from './harness';

// antd draws a table slowly in jsdom.
jest.setTimeout(30_000);

beforeAll(setupBrowser);
beforeEach(() => {
  navigation.path = '/check';
  navigation.params = new URLSearchParams();
});

/*
 * The service "Shop" has a type with resources (branch, levels read and write, and approve which an inline rule grants) and a
 * type without (feature). The PDP of the simulation answers like the real one: the resource search of a level pages by the
 * ids, and the action search on the type as a whole lists the levels the person has on all of it.
 */
const base = '/service/s1/simulate/access/v1/search';
const type = (id: string, name: string, implies: Record<string, string[]> = {}) => ({
  id,
  name,
  service_id: 's1',
  service_name: 'Shop',
  parents: [],
  access_implies: implies,
  type_resource_parent_id: '',
});
const rule = {
  id: 'r1',
  type_resource_id: 't-branch',
  service_id: 's1',
  access: ['approve'],
  subject: 'alice',
  role_id: '00000000-0000-0000-0000-000000000000',
};
const page = (names: string[], next = '') => ({
  results: names.map((id) => ({ type: 'branch', id, properties: { name: `Branch ${id}` } })),
  page: { count: names.length, next_token: next },
});

type Body = {
  subject?: { type: string; id: string; properties?: Record<string, unknown> };
  resource?: { type: string; id?: string };
  action?: { name: string };
  context?: Record<string, unknown>;
  page?: { limit?: number; token?: string };
};

function api(over: Record<string, unknown> = {}) {
  return mockApi({
    'GET /service': [{ id: 's1', name: 'Shop', slug: 'shop', url: null, icon: null }],
    'GET /type_resource': [
      type('t-branch', 'branch', { write: ['read'] }),
      type('t-feature', 'feature'),
      { ...type('t-doc', 'doc'), service_id: 's2', service_name: 'Docs' },
    ],
    'GET /assignment_rule': [rule],
    'GET /service/s1/type_resource/t-branch/resource/synchronizer': [
      {
        id: 'sy1',
        type_resource_id: 't-branch',
        regions: null,
        config: { kind: 'http', url: 'http://shop/branches' },
      },
    ],
    'GET /service/s1/type_resource/t-feature/resource/synchronizer': [],
    [`POST ${base}/action`]: (call: Call) =>
      (call.body as Body).resource?.type === 'feature'
        ? { results: [{ name: 'read' }], page: { count: 1, next_token: '', total: 1 } }
        : { results: [], page: { count: 0, next_token: '', total: 0 } },
    [`POST ${base}/resource`]: (call: Call) => {
      const { action, page: paging } = call.body as Body;
      if (action?.name === 'read' && paging?.token === 'tok-read') return page(['B7']);
      if (action?.name === 'read') return page(['B1', 'B3'], 'tok-read');
      if (action?.name === 'write') return page(['B1']);
      return page([]);
    },
    ...over,
  });
}

const who = {
  person: 'alice',
  properties: { department: 'sales' },
  context: { ip: '10.0.0.1' },
};

const open = () => renderScreen(<VisibleAccess serviceId="s1" serviceSlug="shop" who={who} />);

const bodies = (server: ReturnType<typeof api>, path: string) =>
  server.to('POST', path).map((c) => c.body as Body);

const row = async (text: string) =>
  within((await screen.findByText(text)).closest('tr') as HTMLElement);

/** The button of the Check page is enabled once the services are in. */
const showEnabled = () => waitFor(() => expect(buttonOf(screen, 'Show').disabled).toBe(false));

const expand = async (name: string) =>
  fireEvent.click((await row(name)).getByRole('button', { name: /expand row/i }));

describe('what a person sees in a service', () => {
  it('asks one action search per type for the levels on the type as a whole, and no resources yet', async () => {
    const server = api();
    open();

    // A type without resources is listed with the levels the person has on it.
    const feature = await row('feature');
    expect(await feature.findByText('read')).toBeTruthy();
    expect(feature.getByText('the type as a whole')).toBeTruthy();
    // A type with resources says they are read when it is opened; the other service's type is not asked about.
    const branch = await row('branch');
    expect(branch.getByText('open the row to list them')).toBeTruthy();
    expect(screen.queryByText('doc')).toBeNull();

    await waitFor(() => expect(bodies(server, `${base}/action`)).toHaveLength(2));
    expect(bodies(server, `${base}/resource`)).toHaveLength(0);
    for (const body of bodies(server, `${base}/action`)) {
      expect(body.subject).toEqual({
        type: 'user',
        id: 'alice',
        properties: { department: 'sales' },
      });
      expect(body.resource?.id).toBe('*');
      expect(body.context).toEqual({ ip: '10.0.0.1' });
      expect('action' in body).toBe(false);
    }
    expect(
      bodies(server, `${base}/action`)
        .map((b) => b.resource?.type)
        .sort(),
    ).toEqual(['branch', 'feature']);
  });

  it('reads the resources of a type when its row is opened: one search per level, the first page of each', async () => {
    const server = api();
    open();
    await expand('branch');

    // read and write come from the hierarchy of the type, approve from the inline rule on it.
    await waitFor(() => expect(bodies(server, `${base}/resource`)).toHaveLength(3));
    const asked = bodies(server, `${base}/resource`);
    expect(asked.map((b) => b.action?.name).sort()).toEqual(['approve', 'read', 'write']);
    for (const body of asked) {
      expect(body.resource).toEqual({ type: 'branch' });
      expect(body.page).toEqual({ limit: 50 });
      expect(body.subject?.id).toBe('alice');
    }

    // B3 is the last resource read has given and read has more: B1 has write too, and that is exact; nothing beyond B3 is shown.
    const b1 = await row('Branch B1');
    expect(b1.getByText('read')).toBeTruthy();
    expect(b1.getByText('write')).toBeTruthy();
    const b3 = await row('Branch B3');
    expect(b3.getByText('read')).toBeTruthy();
    expect(b3.queryByText('write')).toBeNull();
    expect(screen.queryByText('Branch B7')).toBeNull();
    expect(screen.getByText('Showing 2 resources')).toBeTruthy();
  });

  it('links each resource to the single question that says why, with its first level', async () => {
    api();
    open();
    await expand('branch');

    const link = (await row('Branch B1')).getByText(/Why\?/).closest('a');
    expect(link?.getAttribute('href')).toBe(
      '/check?mode=one&service=shop&subject=alice&type=branch&resource=B1&action=read',
    );
  });

  it('reads the next page only for the levels that have one, when asked, and never by itself', async () => {
    const server = api();
    open();
    await expand('branch');
    await screen.findByText('Branch B3');
    expect(bodies(server, `${base}/resource`)).toHaveLength(3);

    fireEvent.click(buttonOf(screen, 'Load more'));
    await screen.findByText('Branch B7');

    const all = bodies(server, `${base}/resource`);
    expect(all).toHaveLength(4);
    // The level that had a next page is asked for it, with the same request and its token; the others are not asked again.
    const next = all[3];
    expect(next?.action?.name).toBe('read');
    expect(next?.page).toEqual({ limit: 50, token: 'tok-read' });
    expect(next?.resource).toEqual({ type: 'branch' });
    expect(next?.subject).toEqual(all[0]?.subject);
    expect(next?.context).toEqual(all[0]?.context);
    // Everything is read: no more button, and B7 has read only.
    expect(screen.queryByText('Load more')).toBeNull();
    expect((await row('Branch B7')).queryByText('write')).toBeNull();
    expect(screen.getByText('Showing 3 resources')).toBeTruthy();
  });

  it('shows nothing from a page that was cut short by the scan budget until it is read on', async () => {
    api({
      [`POST ${base}/resource`]: (call: Call) => {
        const { action, page: paging } = call.body as Body;
        if (action?.name === 'read' && paging?.token === 'more') return page(['B2']);
        if (action?.name === 'read') return page([], 'more'); // nothing yet, but not at the end
        return page([]);
      },
    });
    open();
    await expand('branch');

    expect(
      await screen.findByText(/Nothing found in the part of the resources read so far/),
    ).toBeTruthy();
    fireEvent.click(buttonOf(screen, 'Load more'));
    expect(await screen.findByText('Branch B2')).toBeTruthy();
  });

  it('opens the one type it is asked about, at once, and does not ask about the others', async () => {
    const server = api();
    renderScreen(<VisibleAccess serviceId="s1" serviceSlug="shop" who={who} only="branch" />);

    expect(await screen.findByText('Branch B1')).toBeTruthy();
    expect(screen.queryByText('feature')).toBeNull();
    expect(bodies(server, `${base}/action`).map((b) => b.resource?.type)).toEqual(['branch']);
  });

  it('says that the person sees nothing when no type gives them anything', async () => {
    api({
      'GET /service/s1/type_resource/t-branch/resource/synchronizer': [],
      [`POST ${base}/action`]: { results: [], page: { count: 0, next_token: '' } },
    });
    open();
    expect(await screen.findByText('alice sees nothing in this service.')).toBeTruthy();
  });

  it('shows the error of a type whose levels could not be read, and the others', async () => {
    api({
      [`POST ${base}/action`]: (call: Call) =>
        (call.body as Body).resource?.type === 'feature'
          ? {
              __status: 400,
              body: { detail: 'bad subject', extra: { type: 'generic', code: 'x' } },
            }
          : { results: [], page: { count: 0, next_token: '' } },
    });
    open();
    expect(await screen.findByText('bad subject')).toBeTruthy();
    expect(await screen.findByText('branch')).toBeTruthy();
  });
});

describe('the Check page, what a person sees', () => {
  it('asks nothing until Show is pressed, and then with the person, the attributes and the context that were typed', async () => {
    navigation.params = new URLSearchParams('mode=list&service=shop&subject=alice');
    const server = api({ 'GET /group': [] });
    renderScreen(<CheckWorkspace />);

    fireEvent.click(await screen.findByText('Add an attribute'));
    fireEvent.change(screen.getByLabelText('Attributes: Attribute'), {
      target: { value: 'department' },
    });
    fireEvent.change(screen.getByLabelText('Attributes: Value'), {
      target: { value: '[sales, retail]' },
    });
    expect(server.to('POST', base)).toHaveLength(0);

    await showEnabled();
    fireEvent.click(buttonOf(screen, 'Show'));
    expect(await screen.findByText('feature')).toBeTruthy();
    await waitFor(() => expect(bodies(server, `${base}/action`)).toHaveLength(2));
    for (const body of bodies(server, `${base}/action`)) {
      expect(body.subject).toEqual({
        type: 'user',
        id: 'alice',
        properties: { department: ['sales', 'retail'] },
      });
    }
    expect(bodies(server, `${base}/resource`)).toHaveLength(0);
  });

  it('shows only the type that was chosen, opened', async () => {
    navigation.params = new URLSearchParams('mode=list&service=shop&subject=alice&type=branch');
    const server = api({ 'GET /group': [] });
    renderScreen(<CheckWorkspace />);

    await showEnabled();
    fireEvent.click(buttonOf(screen, 'Show'));
    expect(await screen.findByText('Branch B3')).toBeTruthy();
    expect(screen.queryByText('feature')).toBeNull();
    expect(bodies(server, `${base}/action`)).toHaveLength(1);
  });
});

describe('the effective access of a person', () => {
  it('asks with the properties the application sent for the person, once they are known', async () => {
    navigation.path = '/people/alice';
    navigation.params = new URLSearchParams('service=shop');
    const server = api({
      'GET /platform/me': {
        subject: 'admin',
        types: {
          group_member: { whole_type: ['read'], entries: [], denied: [] },
          assignment_rule: { whole_type: ['read', 'write'], entries: [], denied: [] },
        },
      },
      'GET /group': [],
      'GET /service/s1/subjects/user/alice': {
        id: 'alice',
        name: 'Alice',
        type: 'user',
        properties: { department: 'sales', teams: ['a', 'b'] },
        groups: [],
        sent_at: '2026-10-01T10:00:00Z',
        updated_at: '2026-10-01T10:00:00Z',
      },
    });
    render(
      <Providers lang="en" mode="light" scheme="light">
        <AccessProvider>
          <GrantProvider>
            <PersonPage username="alice" />
          </GrantProvider>
        </AccessProvider>
      </Providers>,
    );

    expect(await screen.findByText('feature')).toBeTruthy();
    // Not before the application's properties are in: the first questions are the real ones.
    const actions = bodies(server, `${base}/action`);
    expect(actions.length).toBeGreaterThan(0);
    for (const body of actions) {
      expect(body.subject).toEqual({
        type: 'user',
        id: 'alice',
        properties: { department: 'sales', teams: ['a', 'b'] },
      });
    }
    expect(server.unrouted).toEqual([]);
  });
});
