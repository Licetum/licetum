import { redirect } from 'next/navigation';
import type { ReactNode } from 'react';

import { AppShell } from '@/features/shell/app-shell';
import { identityFromToken } from '@/lib/jwt';
import { readSession } from '@/lib/session';

export default async function AppLayout({ children }: { children: ReactNode }) {
  const session = await readSession();
  const identity = session ? identityFromToken(session.accessToken) : null;
  if (!session || !identity) redirect('/login');
  return <AppShell identity={identity}>{children}</AppShell>;
}
