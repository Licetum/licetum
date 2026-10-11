import type { Metadata, Viewport } from 'next';
import type { ReactNode } from 'react';
import '@fontsource-variable/inter';

import '@fontsource/jetbrains-mono/400.css';
import '@fontsource/jetbrains-mono/500.css';

import { Providers } from '@/components/providers';
import { getPrefs } from '@/lib/prefs';

import './globals.css';

export const metadata: Metadata = {
  title: { default: 'Licetum', template: '%s · Licetum' },
  description: 'One place to say who may do what in your services, and a way to prove it.',
  icons: { icon: '/icon.svg' },
};

export const viewport: Viewport = { width: 'device-width', initialScale: 1 };

export default async function RootLayout({ children }: { children: ReactNode }) {
  const { lang, mode, scheme } = await getPrefs();
  return (
    <html lang={lang} data-theme={scheme} suppressHydrationWarning>
      <body>
        <Providers lang={lang} mode={mode} scheme={scheme}>
          {children}
        </Providers>
      </body>
    </html>
  );
}
