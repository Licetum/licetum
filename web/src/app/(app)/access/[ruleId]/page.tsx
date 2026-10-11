import { GrantDetail } from '@/features/access/grant-detail';

export const metadata = { title: 'Access' };

export default async function Page({ params }: { params: Promise<{ ruleId: string }> }) {
  const { ruleId } = await params;
  return <GrantDetail ruleId={ruleId} />;
}
