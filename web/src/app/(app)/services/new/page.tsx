import { ConnectWizard } from '@/features/services/connect-wizard';

export const metadata = { title: 'Connect a service' };

export default function Page() {
  return <ConnectWizard />;
}
