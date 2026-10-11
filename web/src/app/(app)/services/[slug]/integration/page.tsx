import { IntegrationTab } from '@/features/services/integration-tab';
import { env } from '@/lib/env';

export const metadata = { title: 'Integration' };

export default function Page() {
  return <IntegrationTab checkUrl={env.checkApiPublicUrl()} />;
}
