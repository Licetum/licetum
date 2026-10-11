import { expect, type Page } from '@playwright/test';

/** Signs in through the identity provider of the platform (the local Dex of the development stack by default). */
export async function signIn(page: Page, returnTo = '/') {
  await page.goto(`/auth/login?returnTo=${encodeURIComponent(returnTo)}`);
  await page.fill('input[name=login]', process.env.E2E_USER ?? 'admin@example.com');
  await page.fill('input[name=password]', process.env.E2E_PASSWORD ?? 'password');
  await page.click('button[type=submit]');
  await page.waitForURL((url) => !url.pathname.startsWith('/auth') && !url.href.includes('/dex/'));
  await expect(
    page.getByRole('navigation', { name: /Main navigation|Основная навигация/ }),
  ).toBeVisible();
}

export const unique = (prefix: string) => `${prefix}-${Date.now().toString(36)}`;
