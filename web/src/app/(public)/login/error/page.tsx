import { SignInErrorScreen } from '@/features/auth/sign-in';

export const metadata = { title: 'Sign-in failed' };

export default async function LoginErrorPage({
  searchParams,
}: {
  searchParams: Promise<{ code?: string }>;
}) {
  const { code } = await searchParams;
  return <SignInErrorScreen code={code ?? 'unknown'} />;
}
