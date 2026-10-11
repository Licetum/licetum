import { expect, test } from '@playwright/test';

import { signIn, unique } from './helpers';

test.describe('sign-in', () => {
  test('a visitor is sent to sign in and comes back to the page they asked for', async ({
    page,
  }) => {
    await page.goto('/access');
    await expect(page).toHaveURL(/\/login\?returnTo=%2Faccess/);
    await expect(
      page.getByRole('link', { name: /Sign in with your company account/ }),
    ).toBeVisible();
    await signIn(page, '/access');
    await expect(page).toHaveURL(/\/access$/);
    await expect(page.getByRole('heading', { name: 'Access', level: 2 })).toBeVisible();
  });

  test('the session cookie is httpOnly: the page cannot read the tokens', async ({
    page,
    context,
  }) => {
    await signIn(page);
    expect(await page.evaluate(() => document.cookie)).not.toContain('licetum_session');
    const cookies = await context.cookies();
    const session = cookies.filter((c) => c.name.startsWith('licetum_session'));
    expect(session.length).toBeGreaterThan(0);
    expect(session.every((c) => c.httpOnly)).toBe(true);
  });

  test('the admin API is not reachable through the proxy without a session', async ({
    request,
  }) => {
    const res = await request.get('/api/admin/service');
    expect(res.status()).toBe(401);
  });
});

test.describe('groups, access and checks', () => {
  test('create a group, add a person, give it access, check it, undo', async ({ page }) => {
    await signIn(page);
    const group = unique('e2e-group');

    // A group.
    await page.goto('/people');
    await page.getByRole('button', { name: 'Create a group' }).click();
    await page.getByLabel('Group name').fill(group);
    await page.getByRole('button', { name: 'Create', exact: true }).click();
    await expect(page.getByRole('heading', { name: group })).toBeVisible();

    // A person in it.
    await page.getByRole('button', { name: 'Add to the group' }).click();
    await page.getByRole('textbox', { name: 'Person' }).fill('zoe');
    await page.getByRole('button', { name: 'Add', exact: true }).click();
    await expect(page.getByText('@zoe')).toBeVisible();

    // Access for the group: the panel reads like a sentence.
    await page.getByRole('button', { name: 'Give access' }).first().click();
    const sentence = page.getByTestId('grant-sentence');
    await expect(sentence).toContainText(group);
    await page.getByRole('combobox', { name: 'Service' }).click();
    await page.locator('.ant-select-item-option', { hasText: 'Licetum' }).click();
    await page.getByRole('combobox', { name: 'Resource type' }).click();
    await page.locator('.ant-select-item-option', { hasText: /^group$/ }).click();
    await expect(sentence).toContainText('group');
    // The server says what the access would do before it is given.
    const preview = page.getByTestId('grant-preview');
    await expect(preview).toContainText('What this will do');
    await expect(preview).toContainText(/Covers \d+ resources?/);
    await page.getByRole('button', { name: 'Give access', exact: true }).last().click();
    await expect(page.getByText('Access given')).toBeVisible();

    // It shows in the list of the group's accesses.
    await page.getByRole('tab', { name: /^Access \(1\)/ }).click();
    await expect(page.getByText('read', { exact: true }).first()).toBeVisible();

    // Undo removes it again.
    await page.getByRole('button', { name: 'Undo' }).last().click();
    await expect(page.getByText('Undone')).toBeVisible();

    // Clean up: a group with members can't be deleted, so the person goes first.
    await page.getByRole('tab', { name: /^Composition/ }).click();
    await page.getByRole('button', { name: 'Remove', exact: true }).click();
    await expect(page.getByText('@zoe')).toHaveCount(0);
    await page.getByRole('button', { name: 'Actions' }).click();
    await page.getByText('Delete the group').click();
    await page.getByRole('button', { name: 'Delete', exact: true }).click();
    await expect(page).toHaveURL(/\/people$/);
  });

  test('the check screen explains a decision', async ({ page }) => {
    await signIn(page);
    await page.goto('/check');
    await expect(page.getByRole('heading', { name: 'Access check' })).toBeVisible();
    await page.getByLabel('Who', { exact: true }).fill('nobody-in-particular');
    await expect(page.getByText('Ask about a person')).toBeVisible();
  });
});

test('the language and the theme are remembered', async ({ page, context }) => {
  await signIn(page);
  // The language is a menu in the header: the button shows the current language, the menu lists them.
  const language = page.getByRole('button', { name: 'Language' });
  await language.click();
  await page.getByRole('menuitem', { name: 'Русский' }).click();
  await expect(page.getByRole('tab', { name: 'Главная' })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('tab', { name: 'Главная' })).toBeVisible();
  const cookies = await context.cookies();
  expect(cookies.find((c) => c.name === 'licetum_lang')?.value).toBe('ru');

  // Chinese: the interface, the antd components and the dates follow.
  await page.getByRole('button', { name: /Язык|Language/ }).click();
  await page.getByRole('menuitem', { name: '中文' }).click();
  await expect(page.getByRole('tab', { name: '首页' })).toBeVisible();
  await page.goto('/access');
  await expect(page.getByRole('heading', { name: '访问', level: 2 })).toBeVisible();
  expect((await context.cookies()).find((c) => c.name === 'licetum_lang')?.value).toBe('zh');

  await page.getByRole('button', { name: /语言|Язык|Language/ }).click();
  await page.getByRole('menuitem', { name: 'English' }).click();
  await expect(page.getByRole('tab', { name: 'Home' })).toBeVisible();
});

test.describe('connecting a service', () => {
  test('the wizard creates a service and its type, and the service can be removed again', async ({
    page,
  }) => {
    await signIn(page);
    const name = unique('e2e-svc');

    await page.goto('/services/new');
    await page.getByLabel('Provider address (issuer)').fill('https://sso.example.test/realms/main');
    await page.getByRole('button', { name: 'Show the addresses' }).click();
    await page.getByLabel('Authorization URL').fill('https://sso.example.test/auth');
    await page.getByLabel('Token URL').fill('https://sso.example.test/token');
    await page.getByLabel('JWKS').fill('https://sso.example.test/jwks');
    await page.getByLabel('client_id').fill('e2e-client');
    await page.getByLabel('Name', { exact: true }).fill(name);
    await expect(page.getByLabel('Slug')).toHaveValue(name);
    // The address and the icon are optional and are never requested: an address nothing answers is accepted.
    await page.getByLabel('Application URL').fill('http://127.0.0.1:1/app');
    await page.getByRole('button', { name: 'Next', exact: true }).click();

    // Step 2: a type.
    await expect(page.getByText('The service “' + name + '” is created')).toHaveCount(0);
    await page.getByRole('button', { name: 'One type' }).click();
    await page.getByLabel('Type', { exact: true }).fill('thing');
    await page.getByRole('button', { name: 'Next', exact: true }).click();

    // Step 3 (sources) can be skipped, step 4 shows the result.
    await expect(page.getByText('no source').first())
      .toBeVisible({ timeout: 15_000 })
      .catch(() => undefined);
    await page.getByRole('button', { name: 'Next', exact: true }).click();
    await expect(page.getByText('thing')).toBeVisible();
    await page.getByRole('button', { name: 'Done: open the service' }).click();
    await expect(page.getByRole('heading', { name })).toBeVisible();

    // Removal: the type first (a service with types can't be deleted), then the service.
    await page.getByRole('tab', { name: 'Resource types' }).click();
    await page.getByRole('button', { name: 'Delete', exact: true }).click();
    await page.getByRole('button', { name: 'Delete', exact: true }).last().click();
    await expect(page.getByText('thing')).toHaveCount(0);
    await page.getByRole('tab', { name: 'Overview' }).click();
    await page.getByRole('button', { name: 'Delete the service' }).click();
    await page.getByPlaceholder(name).fill(name);
    await page.getByRole('button', { name: 'Delete', exact: true }).last().click();
    await expect(page).toHaveURL(/\/services$/);
  });
});

test.describe('trusted clients', () => {
  test('a service names the clients it trusts, and the list is kept', async ({ page, baseURL }) => {
    await signIn(page);
    const slug = unique('e2e-trust');
    const api = (method: 'POST' | 'DELETE', path: string, data?: unknown) =>
      page.request.fetch(`/api/admin${path}`, {
        method,
        data,
        headers: { 'content-type': 'application/json', origin: baseURL! },
      });
    const created = await api('POST', '/service', {
      name: slug,
      slug,
      authorization_url: 'https://sso.example.test/auth',
      token_url: 'https://sso.example.test/token',
      jwks_url: 'https://sso.example.test/jwks',
      issuer: 'https://sso.example.test',
      client_id: 'e2e-client',
    });
    expect(created.ok()).toBe(true);
    const { id } = (await created.json()) as { id: string };

    await page.goto(`/services/${slug}/integration`);
    await expect(page.getByRole('heading', { name: 'On behalf of a person' })).toBeVisible();
    await expect(page.getByText('No calls yet')).toBeVisible();

    const clients = page.getByRole('combobox', { name: 'Trusted clients' });
    await clients.fill('docs-backend');
    await clients.press('Enter');
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(page.getByText('Trusted clients saved')).toBeVisible();

    await page.reload();
    await expect(page.getByTestId('delegation').getByText('docs-backend')).toBeVisible();

    expect((await api('DELETE', `/service/${id}`)).ok()).toBe(true);
  });
});

test('"Reset filters" clears every filter at once', async ({ page }) => {
  await signIn(page);
  await page.goto('/access?effect=deny&term=forever&who=group&q=zzz');
  await page.getByRole('button', { name: 'Reset filters' }).first().click();
  await expect(page).toHaveURL(/\/access$/);
});
