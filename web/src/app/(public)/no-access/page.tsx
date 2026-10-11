import { redirect } from 'next/navigation';

import { NoAccessScreen } from '@/features/auth/sign-in';
import { identityFromToken } from '@/lib/jwt';
import { readSession } from '@/lib/session';

export const metadata = { title: 'No access' };

export default async function NoAccessPage() {
  const session = await readSession();
  if (!session) redirect('/login');
  const identity = identityFromToken(session.accessToken);
  return <NoAccessScreen name={identity?.name ?? ''} username={identity?.username ?? ''} />;
}
