import type { ReactNode } from 'react';

import { ServiceFrame } from '@/features/services/service-frame';

export default async function Layout({
  children,
  params,
}: {
  children: ReactNode;
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  return <ServiceFrame slug={slug}>{children}</ServiceFrame>;
}
