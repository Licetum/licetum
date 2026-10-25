/**
 * @jest-environment jsdom
 */
import { beforeAll, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';

import type { PEPClient, PEPKey, PEPKeyIssued } from '@/api/admin/model';
import { PepTab } from '@/features/services/pep-tab';
import { ServiceFrame } from '@/features/services/service-frame';
import { AccessProvider } from '@/lib/access';

import { navigation } from '../next-navigation-stub';
import { buttonOf, dialogOf, failure, mockApi, renderScreen, setupBrowser } from './harness';

// antd draws a table and a drawer slowly in jsdom.
jest.setTimeout(30_000);

beforeAll(() => {
  setupBrowser();
  navigation.path = '/services/shop/pep';
});

const SECRET = 'lct_3f9a1c0d5b7e2a64_s3cr3t-value-that-must-not-stay';

const apiKeyClient: PEPClient = {
  id: 'c1',
  service_id: 's1',
  name: 'checkout',
  kind: 'api_key',
  issuer: null,
  client_id: null,
  operations: ['evaluate', 'search_resource'],
  enabled: true,
  created_at: '2026-10-01T10:00:00Z',
};
const jwtClient: PEPClient = {
  id: 'c2',
  service_id: 's1',
  name: 'backend',
  kind: 'jwt',
  issuer: 'https://idp.example/realms/main',
  client_id: 'checkout-backend',
  operations: ['evaluate'],
  enabled: false,
  created_at: '2026-10-02T10:00:00Z',
};
const revokedKey: PEPKey = {
  key_id: '1111111111111111',
  created_at: '2026-09-01T10:00:00Z',
  expires_at: null,
  revoked_at: '2026-09-15T10:00:00Z',
  active: false,
};
const liveKey: PEPKey = {
  key_id: '2222222222222222',
  created_at: '2026-10-01T10:00:00Z',
  expires_at: null,
  revoked_at: null,
  active: true,
};

/** What the screen asks for around the tab (the service, the platform and what the caller may do) and for the tab itself. */
function api(levels: string[], extra: Record<string, unknown> = {}) {
  return mockApi({
    'GET /service': [{ id: 's1', name: 'Shop', slug: 'shop', url: null, icon: null }],
    'GET /type_resource': [],
    'GET /platform': { service_id: 'platform', admin_group_id: 'g1', types: [] },
    'GET /platform/me': {
      subject: 'admin',
      types: { service: { whole_type: levels, entries: [], denied: [] } },
    },
    'GET /service/s1/pep-clients': [apiKeyClient, jwtClient],
    'GET /service/s1/policy/properties': [
      { name: 'department', source: 'claim', rules: 2, targets: 0, memberships: 1 },
      { name: 'ip', source: 'context', rules: 1, targets: 0, memberships: 0 },
    ],
    'GET /platform/pep-issuers': [
      { issuer: 'https://idp.example/realms/main', jwks_url: 'https://idp.example/keys' },
    ],
    ...extra,
  });
}

function open() {
  return renderScreen(
    <AccessProvider>
      <ServiceFrame slug="shop">
        <PepTab />
      </ServiceFrame>
    </AccessProvider>,
  );
}

/** The row of the table that holds this text. */
const row = async (text: string) =>
  within((await screen.findByText(text)).closest('tr') as HTMLElement);

const switchOf = (scope: ReturnType<typeof within>) => scope.getByRole('switch');

describe('the PEP clients of a service', () => {
  it('lists each client with how it is known, what it may call and whether it is enabled', async () => {
    const server = api(['read', 'write']);
    open();

    const checkout = await row('checkout');
    expect(checkout.getByText('API key')).toBeTruthy();
    expect(checkout.getByText('search_resource')).toBeTruthy();
    expect(switchOf(checkout).getAttribute('aria-checked')).toBe('true');

    const backend = await row('backend');
    expect(backend.getByText('https://idp.example/realms/main')).toBeTruthy();
    expect(backend.getByText('checkout-backend')).toBeTruthy();
    expect(switchOf(backend).getAttribute('aria-checked')).toBe('false');
    // Only an API key client has keys to manage.
    expect(checkout.getByText('Keys')).toBeTruthy();
    expect(backend.queryByText('Keys')).toBeNull();

    // What the PEPs have to send is read from the policy.
    const properties = within(await screen.findByTestId('pep-properties'));
    expect(await properties.findByText('subject.properties.department')).toBeTruthy();
    expect(properties.getByText('context.ip')).toBeTruthy();
    expect(server.unrouted).toEqual([]);
  });

  it('registers an API key client with the name and the operations that were chosen', async () => {
    const server = api(['read', 'write'], {
      'POST /service/s1/pep-clients': { ...apiKeyClient, id: 'c3', name: 'reports' },
      // The keys of the new client, which the screen opens next.
      'GET /service/s1/pep-clients/c3/keys': [],
    });
    open();

    fireEvent.click(await screen.findByText('Register a client'));
    const drawer = await dialogOf('Register a PEP client');
    const create = buttonOf(drawer, 'Create');
    expect(create.disabled).toBe(true);

    fireEvent.change(drawer.getByLabelText('Name'), { target: { value: ' reports ' } });
    fireEvent.click(drawer.getByLabelText(/search_action/));
    fireEvent.click(drawer.getByLabelText(/explain/));
    fireEvent.click(drawer.getByLabelText(/explain/)); // and not that one after all
    expect(create.disabled).toBe(false);
    fireEvent.click(create);

    await waitFor(() => expect(server.to('POST', '/service/s1/pep-clients')).toHaveLength(1));
    expect(server.to('POST', '/service/s1/pep-clients')[0]?.body).toEqual({
      name: 'reports',
      kind: 'api_key',
      operations: ['evaluate', 'search_action'],
      enabled: true,
    });
    // The new client has no key yet: the keys are what the screen shows next.
    expect(await screen.findByText(/API keys of “reports”/)).toBeTruthy();
  });

  it('registers a client of an issuer with the issuer it picked and its client id', async () => {
    const server = api(['read', 'write'], {
      'POST /service/s1/pep-clients': { ...jwtClient, id: 'c4', name: 'etl' },
    });
    open();

    fireEvent.click(await screen.findByText('Register a client'));
    const drawer = await dialogOf('Register a PEP client');
    fireEvent.change(drawer.getByLabelText('Name'), { target: { value: 'etl' } });
    fireEvent.click(drawer.getByText('Issuer token'));

    // Only the issuers that are registered can be picked.
    fireEvent.mouseDown(drawer.getByRole('combobox'));
    fireEvent.click(
      await screen.findByText('https://idp.example/realms/main', {
        selector: '.ant-select-item-option-content',
      }),
    );
    fireEvent.change(drawer.getByLabelText('Client id'), { target: { value: 'etl-job' } });
    fireEvent.click(buttonOf(drawer, 'Create'));

    await waitFor(() => expect(server.to('POST', '/service/s1/pep-clients')).toHaveLength(1));
    expect(server.to('POST', '/service/s1/pep-clients')[0]?.body).toEqual({
      name: 'etl',
      kind: 'jwt',
      operations: ['evaluate'],
      enabled: true,
      issuer: 'https://idp.example/realms/main',
      client_id: 'etl-job',
    });
  });

  it('disables a client with one request that carries nothing else', async () => {
    const server = api(['read', 'write'], {
      'PATCH /service/s1/pep-clients/c1': { ...apiKeyClient, enabled: false },
    });
    open();

    fireEvent.click(switchOf(await row('checkout')));
    await waitFor(() => expect(server.to('PATCH', '/service/s1/pep-clients/c1')).toHaveLength(1));
    expect(server.to('PATCH', '/service/s1/pep-clients/c1')[0]?.body).toEqual({ enabled: false });
  });

  it('shows what the caller may not change as plain text and offers no write', async () => {
    api(['read']);
    open();

    const backend = await row('backend');
    expect(backend.getByText('Disabled')).toBeTruthy();
    expect(backend.queryByRole('switch')).toBeNull();
    expect(screen.queryByText('Register a client')).toBeNull();
    expect(screen.queryByText('Delete')).toBeNull();
    expect(screen.queryByText('Edit')).toBeNull();
    // The keys can be looked at, not issued.
    expect((await row('checkout')).getByText('Keys')).toBeTruthy();
  });
});

describe('the API keys of a client', () => {
  const issued: PEPKeyIssued = { ...liveKey, key_id: '3f9a1c0d5b7e2a64', key: SECRET };
  const keysRoutes = {
    'GET /service/s1/pep-clients/c1/keys': [liveKey, revokedKey],
    'POST /service/s1/pep-clients/c1/keys': issued,
  };

  async function openKeys(extra: Record<string, unknown> = keysRoutes, levels = ['read', 'write']) {
    const server = api(levels, extra);
    const view = open();
    fireEvent.click((await row('checkout')).getByText('Keys'));
    const drawer = await dialogOf(/API keys of “checkout”/);
    return { ...view, server, drawer };
  }

  const keyRow = async (drawer: ReturnType<typeof within>, id: string) =>
    within((await drawer.findByText(`lct_${id}`)).closest('tr') as HTMLElement);

  it('lists the keys with their state and never a secret', async () => {
    const { drawer } = await openKeys();

    const live = await keyRow(drawer, '2222222222222222');
    expect(live.getByText('active')).toBeTruthy();
    expect(live.getByText('Revoke')).toBeTruthy();
    const revoked = await keyRow(drawer, '1111111111111111');
    expect(revoked.getByText('revoked')).toBeTruthy();
    expect(revoked.queryByText('Revoke')).toBeNull();
  });

  it('shows the key it issues once, and forgets it when the dialog is closed', async () => {
    const { drawer, client, server } = await openKeys();
    await keyRow(drawer, '2222222222222222');

    fireEvent.click(buttonOf(drawer, 'Issue a key'));
    const dialog = await dialogOf('API key issued');
    expect(server.to('POST', '/service/s1/pep-clients/c1/keys')).toHaveLength(1);
    expect(dialog.getByTestId('issued-key').textContent).toBe(SECRET);
    // The warning says that this is the only time.
    expect(dialog.getByText(/shown only once and cannot be read again/)).toBeTruthy();

    fireEvent.click(buttonOf(dialog, 'I have saved the key'));
    await waitFor(() => expect(screen.queryByTestId('issued-key')).toBeNull());
    expect(document.body.textContent).not.toContain(SECRET);

    // Nothing of TanStack Query keeps the answer that carried the key: no mutation, no cached query.
    const kept = () =>
      JSON.stringify([
        ...client
          .getMutationCache()
          .getAll()
          .map((m) => m.state.data),
        ...client
          .getQueryCache()
          .getAll()
          .map((q) => q.state.data),
      ]);
    await waitFor(() => expect(kept()).not.toContain(SECRET));
  });

  it('revokes a key after asking, naming the key in the request', async () => {
    const { drawer, server } = await openKeys({
      ...keysRoutes,
      'DELETE /service/s1/pep-clients/c1/keys/2222222222222222': '2222222222222222',
    });

    fireEvent.click((await keyRow(drawer, '2222222222222222')).getByText('Revoke'));
    const confirm = await dialogOf(/Revoke the key lct_2222222222222222/);
    fireEvent.click(buttonOf(confirm, 'Revoke'));

    await waitFor(() =>
      expect(server.to('DELETE', '/service/s1/pep-clients/c1/keys/2222222222222222')).toHaveLength(
        1,
      ),
    );
  });

  it('offers no key when the client already has two active ones', async () => {
    const { drawer } = await openKeys({
      'GET /service/s1/pep-clients/c1/keys': [liveKey, { ...liveKey, key_id: '4444444444444444' }],
    });
    await keyRow(drawer, '4444444444444444');
    expect(buttonOf(drawer, 'Issue a key').disabled).toBe(true);
  });

  it('shows the error of an issue that failed, and no key', async () => {
    const { drawer } = await openKeys({
      'GET /service/s1/pep-clients/c1/keys': [],
      'POST /service/s1/pep-clients/c1/keys': failure(
        409,
        'The client already has two active keys',
      ),
    });
    await drawer.findByText('No keys yet');

    fireEvent.click(buttonOf(drawer, 'Issue a key'));
    expect(await drawer.findByText('The client already has two active keys')).toBeTruthy();
    expect(screen.queryByTestId('issued-key')).toBeNull();
  });

  it('does not let a reader issue or revoke', async () => {
    const { drawer } = await openKeys(keysRoutes, ['read']);
    const live = await keyRow(drawer, '2222222222222222');
    expect(live.queryByText('Revoke')).toBeNull();
    expect(drawer.queryByText('Issue a key')).toBeNull();
  });
});
