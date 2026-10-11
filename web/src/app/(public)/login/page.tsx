import { redirect } from 'next/navigation';

import { SignInScreen } from '@/features/auth/sign-in';
import { safeReturnTo } from '@/lib/safe-return';
import { readSession } from '@/lib/session';

export const metadata = { title: 'Sign in' };

export default async function LoginPage({
  searchParams,
}: {
  searchParams: Promise<{ returnTo?: string }>;
}) {
  const { returnTo } = await searchParams;
  const next = safeReturnTo(returnTo);
  if (await readSession()) redirect(next);
  return <SignInScreen returnTo={next} />;
}
