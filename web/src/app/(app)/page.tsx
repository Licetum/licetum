import { Home } from '@/features/home/home';
import { identityFromToken } from '@/lib/jwt';
import { readSession } from '@/lib/session';

export const metadata = { title: 'Home' };

export default async function Page() {
  const session = await readSession();
  const identity = session ? identityFromToken(session.accessToken) : null;
  return <Home name={identity?.name ?? ''} />;
}
