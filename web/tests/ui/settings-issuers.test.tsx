/**
 * @jest-environment jsdom
 */
import { beforeAll, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';

import type { PEPIssuer } from '@/api/admin/model';
import { SettingsIssuers } from '@/features/settings/settings-issuers';
import { AccessProvider } from '@/lib/access';

import { navigation } from '../next-navigation-stub';
import { buttonOf, dialogOf, failure, mockApi, renderScreen, setupBrowser } from './harness';

// antd draws a table and a drawer slowly in jsdom.
jest.setTimeout(30_000);

beforeAll(() => {
  setupBrowser();
  navigation.path = '/settings/issuers';
});

const main: PEPIssuer = {
  issuer: 'https://idp.example/realms/main',
  jwks_url: 'https://idp.example/realms/main/protocol/openid-connect/certs',
  audiences: ['licetum', 'pdp'],
  algs: ['RS256', 'ES256'],
  require_typ: true,
};
const plain: PEPIssuer = {
  issuer: 'https://login.example',
  jwks_url: 'https://login.example/keys',
};

function api(levels: string[], extra: Record<string, unknown> = {}) {
  return mockApi({
    'GET /platform/me': {
      subject: 'admin',
      types: { service: { whole_type: levels, entries: [], denied: [] } },
    },
    'GET /platform/pep-issuers': [main, plain],
    ...extra,
  });
}

const open = () =>
  renderScreen(
    <AccessProvider>
      <SettingsIssuers />
    </AccessProvider>,
  );

const row = async (text: string) =>
  within((await screen.findByText(text)).closest('tr') as HTMLElement);

describe('the PEP issuers of the platform', () => {
  it('lists each issuer with where its keys are, the audiences and algorithms it accepts, or the defaults', async () => {
    api(['read', 'write']);
    open();

    const first = await row('https://idp.example/realms/main');
    expect(first.getByText('licetum')).toBeTruthy();
    expect(first.getByText('ES256')).toBeTruthy();
    expect(first.getByText('at+jwt')).toBeTruthy();
    const second = await row('https://login.example');
    expect(second.getByText('the audience of the PDP')).toBeTruthy();
    expect(second.getByText('default (RS256, ES256, EdDSA)')).toBeTruthy();
  });

  it('registers an issuer with only what was filled in, and the defaults left to the PDP', async () => {
    const server = api(['read', 'write'], {
      'PUT /platform/pep-issuers': (call: { body: unknown }) => call.body,
    });
    open();

    fireEvent.click(await screen.findByText('Register an issuer', { selector: 'button span' }));
    const drawer = await dialogOf('Register an issuer');
    const create = buttonOf(drawer, 'Create');
    expect(create.disabled).toBe(true);

    fireEvent.change(drawer.getByLabelText('Issuer (iss)'), {
      target: { value: ' https://sso.example/realms/new ' },
    });
    // The address of the keys has to be an http(s) address.
    fireEvent.change(drawer.getByLabelText('Keys (JWKS URL)'), {
      target: { value: 'sso.example/keys' },
    });
    expect(create.disabled).toBe(true);
    fireEvent.change(drawer.getByLabelText('Keys (JWKS URL)'), {
      target: { value: 'https://sso.example/keys' },
    });
    expect(create.disabled).toBe(false);
    fireEvent.click(create);

    await waitFor(() => expect(server.to('PUT', '/platform/pep-issuers')).toHaveLength(1));
    expect(server.to('PUT', '/platform/pep-issuers')[0]?.body).toEqual({
      issuer: 'https://sso.example/realms/new',
      jwks_url: 'https://sso.example/keys',
      require_typ: false,
    });
  });

  it('finds the keys of an issuer from its discovery document', async () => {
    api(['read', 'write'], {
      'GET /api/discover': {
        issuer: 'https://sso.example/realms/new',
        jwks_url: 'https://sso.example/realms/new/certs',
      },
    });
    open();

    fireEvent.click(await screen.findByText('Register an issuer', { selector: 'button span' }));
    const drawer = await dialogOf('Register an issuer');
    fireEvent.change(drawer.getByLabelText('Issuer (iss)'), {
      target: { value: 'https://sso.example/realms/new/' },
    });
    fireEvent.click(buttonOf(drawer, 'Detect automatically'));

    expect(await drawer.findByText('Found where its keys are.')).toBeTruthy();
    expect((drawer.getByLabelText('Keys (JWKS URL)') as HTMLInputElement).value).toBe(
      'https://sso.example/realms/new/certs',
    );
    // The name is the one the document gives: a token's iss is compared with it exactly.
    expect((drawer.getByLabelText('Issuer (iss)') as HTMLInputElement).value).toBe(
      'https://sso.example/realms/new',
    );
  });

  it('keeps the name of an issuer that is being changed', async () => {
    api(['read', 'write']);
    open();

    fireEvent.click((await row('https://login.example')).getByText('Edit'));
    const drawer = await dialogOf('Change the issuer');
    const name = drawer.getByLabelText('Issuer (iss)') as HTMLInputElement;
    expect(name.value).toBe('https://login.example');
    expect(name.disabled).toBe(true);
  });

  it('says when registering a name that exists replaces its settings', async () => {
    api(['read', 'write']);
    open();

    fireEvent.click(await screen.findByText('Register an issuer', { selector: 'button span' }));
    const drawer = await dialogOf('Register an issuer');
    fireEvent.change(drawer.getByLabelText('Issuer (iss)'), {
      target: { value: 'https://login.example' },
    });
    expect(drawer.getByText(/saving replaces its settings/)).toBeTruthy();
  });

  it('shows why an issuer cannot be deleted', async () => {
    const server = api(['read', 'write'], {
      'DELETE /platform/pep-issuers': failure(409, 'A client of a service still uses the issuer'),
    });
    open();

    fireEvent.click((await row('https://idp.example/realms/main')).getByText('Delete'));
    const confirm = await dialogOf(/Delete the issuer/);
    fireEvent.click(buttonOf(confirm, 'Delete'));

    expect(await screen.findByText('A client of a service still uses the issuer')).toBeTruthy();
    expect(server.to('DELETE', '/platform/pep-issuers?issuer=')[0]?.path).toBe(
      `/platform/pep-issuers?issuer=${encodeURIComponent(main.issuer)}`,
    );
  });

  it('is read-only for someone who can only read', async () => {
    api(['read']);
    open();

    await row('https://login.example');
    expect(screen.queryByText('Register an issuer')).toBeNull();
    expect(screen.queryByText('Edit')).toBeNull();
    expect(screen.queryByText('Delete')).toBeNull();
  });
});
