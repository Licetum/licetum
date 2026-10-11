import { PersonPage } from '@/features/people/person-page';

export const metadata = { title: 'Person' };

export default async function Page({ params }: { params: Promise<{ username: string }> }) {
  const { username } = await params;
  return <PersonPage username={decodeURIComponent(username)} />;
}
