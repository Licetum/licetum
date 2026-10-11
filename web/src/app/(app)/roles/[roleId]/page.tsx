import { RolePage } from '@/features/roles/role-page';

export const metadata = { title: 'Role' };

export default async function Page({ params }: { params: Promise<{ roleId: string }> }) {
  const { roleId } = await params;
  return <RolePage roleId={roleId} />;
}
