import { beforeAll, describe, expect, it } from 'vitest';

beforeAll(() => {
  process.env.SESSION_SECRET = 'x'.repeat(40);
  process.env.APP_BASE_URL = 'https://licetum.example';
});

type Cookie = { value: string; options: Record<string, unknown> };

describe('the session cookie', () => {
  it('survives a round trip and is unreadable without the secret', async () => {
    const { seal, unseal } = await import('@/lib/session');
    const session = {
      accessToken: 'a.b.c',
      accessExpiresAt: Date.now() + 60_000,
      refreshToken: 'r',
    };
    const sealed = await seal(session);
    expect(sealed).not.toContain('a.b.c');
    expect(await unseal(sealed)).toEqual(session);

    process.env.SESSION_SECRET = 'y'.repeat(40);
    expect(await unseal(sealed)).toBeNull();
    process.env.SESSION_SECRET = 'x'.repeat(40);
    expect(await unseal('not a token')).toBeNull();
  });

  it('is split into chunks that each fit a cookie, httpOnly and secure over HTTPS', async () => {
    const { writeSession, readSessionFrom } = await import('@/lib/session');
    const jar = new Map<string, Cookie>();
    const target = {
      set: (name: string, value: string, options: Record<string, unknown>) =>
        void jar.set(name, { value, options }),
      delete: (name: string) => void jar.delete(name),
    };
    // A token as long as the ones some providers issue.
    const big = `${'h'.repeat(40)}.${'p'.repeat(3600)}.${'s'.repeat(300)}`;
    await writeSession(target, {
      accessToken: big,
      accessExpiresAt: Date.now() + 300_000,
      refreshToken: 'r'.repeat(1200),
    });

    expect(jar.size).toBeGreaterThan(1);
    for (const { value, options } of jar.values()) {
      expect(value.length).toBeLessThanOrEqual(3600);
      expect(options).toMatchObject({ httpOnly: true, secure: true, sameSite: 'lax', path: '/' });
    }
    const read = await readSessionFrom({
      get: (name) => (jar.has(name) ? { value: jar.get(name)!.value } : undefined),
    });
    expect(read?.accessToken).toBe(big);
  });
});
