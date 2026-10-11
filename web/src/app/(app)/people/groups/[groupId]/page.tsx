import { GroupPage } from '@/features/people/group-page';

export const metadata = { title: 'Group' };

export default async function Page({ params }: { params: Promise<{ groupId: string }> }) {
  const { groupId } = await params;
  return <GroupPage groupId={groupId} />;
}
