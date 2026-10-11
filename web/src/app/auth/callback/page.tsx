'use client';

import { Spin } from 'antd';
import { useEffect, useRef, useState } from 'react';

import { AuthCard } from '@/components/auth-card';
import { SignInErrorScreen } from '@/features/auth/sign-in';
import { sessionHandoffAnswerSchema } from '@/lib/schemas';

/**
 * The admin API sends the browser back here with the tokens in the URL fragment. The fragment is read once, removed
 * from the address bar and handed to the server (POST /auth/session), which keeps it in an encrypted cookie.
 */
export default function AuthCallbackPage() {
  const [failed, setFailed] = useState(false);
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;

    const params = new URLSearchParams(window.location.hash.replace(/^#/, ''));
    window.history.replaceState(null, '', window.location.pathname);
    const accessToken = params.get('access_token');
    if (!accessToken) {
      setFailed(true);
      return;
    }

    fetch('/auth/session', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        access_token: accessToken,
        expires_in: params.get('expires_in') ?? undefined,
        refresh_token: params.get('refresh_token') ?? undefined,
        refresh_expires_in: params.get('refresh_expires_in') ?? undefined,
      }),
    })
      .then(async (response) => {
        if (!response.ok) throw new Error(String(response.status));
        const { returnTo } = sessionHandoffAnswerSchema.parse(await response.json());
        window.location.replace(returnTo || '/');
      })
      .catch(() => setFailed(true));
  }, []);

  if (failed) return <SignInErrorScreen code="unknown" />;
  return (
    <AuthCard>
      <div style={{ display: 'grid', placeItems: 'center', padding: 24 }}>
        <Spin size="large" />
      </div>
    </AuthCard>
  );
}
