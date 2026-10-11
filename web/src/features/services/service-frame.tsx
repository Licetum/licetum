'use client';

import { Avatar, Flex, Tabs } from 'antd';
import { usePathname, useRouter } from 'next/navigation';
import { createContext, useContext, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import type { ServiceResponse } from '@/api/admin/model';
import { useGetPlatform } from '@/api/admin/platform/platform';
import { EmptyBlock, ErrorBlock, IdChip, LoadingBlock, PageHeader } from '@/components/kit';
import { DecisionTag } from '@/components/kit';

import { useAllTypes, useServices } from '../data/queries';

type ServiceContextValue = { service: ServiceResponse; system: boolean };
const ServiceContext = createContext<ServiceContextValue | null>(null);

export const useService = (): ServiceContextValue => {
  const value = useContext(ServiceContext);
  if (!value) throw new Error('useService outside ServiceFrame');
  return value;
};

const TABS = ['', 'types', 'sources', 'integration', 'policy'] as const;

/** The page of one service: the header with its status and the tabs Overview / Types / Sources / Integration / Policy. */
export function ServiceFrame({ slug, children }: { slug: string; children: ReactNode }) {
  const { t } = useTranslation();
  const router = useRouter();
  const pathname = usePathname();
  const services = useServices();
  const types = useAllTypes();
  const platform = useGetPlatform({
    query: { select: (r) => (r.status === 200 ? r.data : undefined) },
  });

  if (services.isPending)
    return (
      <div className="page">
        <LoadingBlock rows={5} />
      </div>
    );
  if (services.isError)
    return (
      <div className="page">
        <ErrorBlock error={services.error} onRetry={() => void services.refetch()} />
      </div>
    );

  const service = services.data?.find((s) => s.slug === slug || s.id === slug);
  if (!service) {
    return (
      <div className="page">
        <EmptyBlock
          title={t('service.notFound.title')}
          hint={t('service.notFound.hint')}
          action={<a href="/services">{t('service.notFound.back')}</a>}
        />
      </div>
    );
  }

  const base = `/services/${service.slug}`;
  const activeTab = TABS.find((tab) => tab && pathname.startsWith(`${base}/${tab}`)) ?? '';
  const hasTypes = (types.data ?? []).some((x) => x.service_id === service.id);

  return (
    <ServiceContext.Provider value={{ service, system: service.id === platform.data?.service_id }}>
      <div className="page">
        <PageHeader
          crumbs={[{ label: t('nav.services'), href: '/services' }, { label: service.name }]}
          title={
            <Flex align="center" gap={12}>
              <Avatar
                shape="square"
                size={36}
                src={service.icon || undefined}
                style={{
                  background: 'var(--licetum-inherit-bg)',
                  color: 'var(--licetum-accent)',
                  fontWeight: 600,
                }}
              >
                {service.name.slice(0, 1).toUpperCase()}
              </Avatar>
              {service.name}
            </Flex>
          }
          subtitle={<IdChip value={service.slug} />}
          tag={
            hasTypes ? (
              <DecisionTag decision="allow">{t('services.status.ready')}</DecisionTag>
            ) : (
              <DecisionTag decision="except">{t('services.status.incomplete')}</DecisionTag>
            )
          }
        />
        <Tabs
          activeKey={activeTab}
          onChange={(key) => router.push(key ? `${base}/${key}` : base)}
          items={TABS.map((key) => ({ key, label: t(`service.tab.${key || 'overview'}`) }))}
          style={{ marginBottom: 8 }}
        />
        {children}
      </div>
    </ServiceContext.Provider>
  );
}
