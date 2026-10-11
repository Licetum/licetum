import { ResourcesBrowser } from '@/features/services/resources-browser';

export const metadata = { title: 'Resources' };

export default async function Page({ params }: { params: Promise<{ type: string }> }) {
  const { type } = await params;
  return <ResourcesBrowser typeName={decodeURIComponent(type)} />;
}
