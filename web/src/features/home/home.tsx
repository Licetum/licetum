'use client';

import { Alert, Button, Flex, Progress, Steps, Tag, Typography } from 'antd';
import { ChevronRight } from 'lucide-react';
import Link from 'next/link';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useGetPlatform } from '@/api/admin/platform/platform';
import { useLintPolicy } from '@/api/admin/policy/policy';
import { PageHeader } from '@/components/kit';
import { usePreferences } from '@/components/providers';
import { REVERTIBLE } from '@/domain/changes';
import { formatTime } from '@/domain/format';
import { describeWho, hasId, ruleStatus } from '@/domain/rule';
import { useAccess } from '@/lib/access';

import { useChangeLog, useGroupNames, useGroups, useRules, useServices } from '../data/queries';
import { useGrant } from '../grant/grant-context';
import { useChangeSentence } from '../history/change-line';
import { useRevert } from '../history/use-revert';

const HIDE_KEY = 'licetum_hide_setup';

const grid = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))',
  gap: 16,
} as const;

const panel = {
  background: 'var(--licetum-surface)',
  border: '1px solid var(--licetum-border)',
  borderRadius: 8,
  padding: 16,
  minWidth: 0,
} as const;

/* A long list scrolls inside its block, not the whole page. */
const scroller = { maxHeight: 360, overflowY: 'auto', minHeight: 0 } as const;

function ActionCard(props: { title: string; hint: string; onClick?: () => void; href?: string }) {
  const body = (
    <div
      style={{
        background: 'var(--licetum-surface)',
        border: '1px solid var(--licetum-border)',
        borderRadius: 8,
        padding: 16,
        minHeight: 88,
        height: '100%',
      }}
    >
      <div style={{ color: 'var(--licetum-accent)', fontWeight: 600, fontSize: 15 }}>
        {props.title}
      </div>
      <div style={{ color: 'var(--licetum-muted)' }}>{props.hint}</div>
    </div>
  );
  if (props.href)
    return (
      <Link href={props.href} style={{ display: 'block' }}>
        {body}
      </Link>
    );
  return (
    <button
      type="button"
      onClick={props.onClick}
      style={{
        all: 'unset',
        cursor: 'pointer',
        display: 'block',
        boxSizing: 'border-box',
      }}
    >
      {body}
    </button>
  );
}

export function Home({ name }: { name: string }) {
  const { t } = useTranslation();
  const { lang } = usePreferences();
  const access = useAccess();
  const grant = useGrant();
  const platform = useGetPlatform({
    query: { select: (r) => (r.status === 200 ? r.data : undefined) },
  });
  const services = useServices();
  const groups = useGroups();
  const groupNames = useGroupNames();
  const rules = useRules();
  const log = useChangeLog({ enabled: access.whole('service', 'read') });
  const revert = useRevert();
  const sentence = useChangeSentence();

  const [hidden, setHidden] = useState(true);
  useEffect(() => {
    try {
      setHidden(localStorage.getItem(HIDE_KEY) === '1');
    } catch {
      setHidden(false);
    }
  }, []);

  // The hour is read after the first render: the server and the browser do not share a clock or a time zone, so a
  // greeting computed during rendering would not match the HTML the server sent (hydration mismatch).
  const [hour, setHour] = useState<number | null>(null);
  useEffect(() => setHour(new Date().getHours()), []);
  const greeting =
    hour === null
      ? t('home.hello')
      : hour < 5
        ? t('home.night')
        : hour < 12
          ? t('home.morning')
          : hour < 18
            ? t('home.day')
            : t('home.evening');
  const first = name.split(' ')[0] ?? name;

  const platformId = platform.data?.service_id;
  const clientServices = (services.data ?? []).filter((s) => s.id !== platformId);
  const clientRules = (rules.data ?? []).filter(
    (r) => hasId(r.role_id) || r.service_id !== platformId,
  );

  // The administrators' group created by the platform doesn't count as "the first group": every rule it has is a rule on Licetum.
  const ownGroups = (groups.data ?? []).filter((g) => {
    const its = (rules.data ?? []).filter(
      (r) => r.member_of_group_id === g.id || r.subject === `group:${g.id}`,
    );
    return !(its.length > 0 && its.every((r) => r.service_id === platformId && !hasId(r.role_id)));
  });

  const steps = [
    { key: 'idp', done: true, text: t('home.step.idp') },
    {
      key: 'service',
      done: clientServices.length > 0,
      text: t('home.step.service'),
      href: '/services',
      cta: t('home.step.serviceCta'),
    },
    {
      key: 'group',
      done: ownGroups.length > 0,
      text: t('home.step.group'),
      href: '/people',
      cta: t('home.step.groupCta'),
    },
    {
      key: 'grant',
      done: clientRules.length > 0,
      text: t('home.step.grant'),
      cta: t('home.step.grantCta'),
    },
  ];
  const done = steps.filter((s) => s.done).length;
  const showSetup = !hidden && done < steps.length && !services.isPending && !rules.isPending;

  // What needs attention: found in the rules themselves, plus the findings of the policy linter of the first services.
  const lintTargets = clientServices.slice(0, 5);
  const attention = useMemo(() => {
    const items: { key: string; text: string; href: string; tag?: string }[] = [];
    for (const r of rules.data ?? []) {
      if (r.service_id === platformId && !hasId(r.role_id)) continue;
      const who = describeWho(r, groupNames.name, t);
      const { status, days } = ruleStatus(r);
      if (r.enabled && r.effect === 'allow' && who.kind === 'everyone')
        items.push({
          key: `all-${r.id}`,
          text: t('home.attention.everyone'),
          href: `/access/${r.id}`,
          tag: r.name ?? undefined,
        });
      if (status === 'expiring')
        items.push({
          key: `exp-${r.id}`,
          text: t('home.attention.expiring', { count: days ?? 0 }),
          href: `/access/${r.id}`,
          tag: r.name ?? who.label,
        });
      if (status === 'dead')
        items.push({
          key: `dead-${r.id}`,
          text: t('home.attention.dead'),
          href: `/access/${r.id}`,
          tag: r.name ?? who.label,
        });
    }
    return items;
  }, [rules.data, groupNames.name, platformId, t]);

  const recent = (log.data ?? []).slice(-20).reverse();

  return (
    <div className="page">
      <PageHeader title={t('home.title', { greeting, name: first })} />

      <div style={{ ...grid, marginBottom: 16 }}>
        {access.whole('assignment_rule', 'write') && (
          <ActionCard
            title={`＋ ${t('grant.open')}`}
            hint={t('home.grantHint')}
            onClick={() => grant.open()}
          />
        )}
        <ActionCard title={t('home.check')} hint={t('home.checkHint')} href="/check" />
        {access.whole('service', 'write') && (
          <ActionCard title={t('home.connect')} hint={t('home.connectHint')} href="/services" />
        )}
      </div>

      {showSetup && (
        <div
          style={{
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: 16,
            marginBottom: 16,
          }}
        >
          <Flex align="center" gap={12} wrap style={{ marginBottom: 8 }}>
            <Typography.Title level={5} style={{ margin: 0 }}>
              {t('home.setup')}
            </Typography.Title>
            <Tag color="success">{t('home.setupCount', { done, total: steps.length })}</Tag>
            <Progress
              percent={(done / steps.length) * 100}
              showInfo={false}
              style={{ width: 160, margin: 0 }}
              aria-label={t('home.setup')}
            />
            <span style={{ flex: 1 }} />
            <Button
              size="small"
              onClick={() => {
                try {
                  localStorage.setItem(HIDE_KEY, '1');
                } catch {}
                setHidden(true);
              }}
            >
              {t('home.setupHide')}
            </Button>
          </Flex>
          <Steps
            size="small"
            orientation="vertical"
            current={steps.findIndex((x) => !x.done)}
            items={steps.map((x) => ({
              key: x.key,
              title: x.text,
              status: x.done ? 'finish' : undefined,
              content:
                !x.done && x.key === 'grant' && access.whole('assignment_rule', 'write') ? (
                  <Button size="small" onClick={() => grant.open()}>
                    {x.cta} →
                  </Button>
                ) : !x.done && x.href ? (
                  <Button size="small" href={x.href}>
                    {x.cta} →
                  </Button>
                ) : undefined,
            }))}
          />
        </div>
      )}

      <div style={grid}>
        <section aria-labelledby="attention" style={panel}>
          <Typography.Title level={5} id="attention" style={{ marginTop: 0 }}>
            {t('home.attention.title')}
          </Typography.Title>
          <div style={scroller}>
            {attention.length === 0 && lintTargets.length === 0 && (
              <Typography.Text type="secondary">{t('home.attention.none')}</Typography.Text>
            )}
            {attention.map((a) => (
              <Link
                key={a.key}
                href={a.href}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                  padding: '8px 0',
                  borderBottom: '1px solid var(--licetum-border)',
                  color: 'var(--licetum-text)',
                }}
              >
                <span
                  aria-hidden
                  style={{
                    width: 8,
                    height: 8,
                    borderRadius: '50%',
                    background: 'var(--licetum-except-fg)',
                    flex: 'none',
                  }}
                />
                <span style={{ flex: 1 }}>{a.text}</span>
                {a.tag && <Tag style={{ margin: 0 }}>{a.tag}</Tag>}
                <ChevronRight size={14} aria-hidden />
              </Link>
            ))}
            {lintTargets.map((s) => (
              <LintLine key={s.id} serviceId={s.id} serviceName={s.name} slug={s.slug} />
            ))}
          </div>
        </section>

        <section aria-labelledby="recent" style={panel}>
          <Flex justify="space-between" align="center">
            <Typography.Title level={5} id="recent" style={{ margin: 0 }}>
              {t('home.recent')}
            </Typography.Title>
            {access.whole('service', 'read') && <Link href="/history">{t('home.allHistory')}</Link>}
          </Flex>
          <div style={scroller}>
            {!access.whole('service', 'read') ? (
              <Typography.Text type="secondary">{t('home.recentNoRight')}</Typography.Text>
            ) : recent.length === 0 ? (
              <Typography.Text type="secondary">{t('home.recentNone')}</Typography.Text>
            ) : (
              recent.map((r) => (
                <Flex
                  key={r.id}
                  align="center"
                  gap={8}
                  style={{ padding: '8px 0', borderBottom: '1px solid var(--licetum-border)' }}
                >
                  <Link
                    href={`/history?change=${r.id}`}
                    style={{ flex: 1, minWidth: 0, color: 'var(--licetum-text)' }}
                  >
                    <span style={{ color: 'var(--licetum-muted)' }}>{formatTime(r.at, lang)}</span>{' '}
                    · {sentence(r)}
                  </Link>
                  {access.whole('assignment_rule', 'write') &&
                    REVERTIBLE.has(r.entity) &&
                    r.actor && (
                      <Button
                        size="small"
                        disabled={revert.busy}
                        onClick={() => void revert.applyOne(r.id)}
                      >
                        {t('undo.action')}
                      </Button>
                    )}
                </Flex>
              ))
            )}
          </div>
        </section>
      </div>
      {(services.isError || rules.isError) && (
        <Alert style={{ marginTop: 16 }} type="error" showIcon title={t('common.loadFailed')} />
      )}
    </div>
  );
}

function LintLine({
  serviceId,
  serviceName,
  slug,
}: {
  serviceId: string;
  serviceName: string;
  slug: string;
}) {
  const { t } = useTranslation();
  const lint = useLintPolicy(serviceId, {
    query: { select: (r) => (r.status === 200 ? (r.data?.findings ?? []) : []), staleTime: 60_000 },
  });
  const findings = lint.data ?? [];
  const bad = findings.filter((f) => f.severity !== 'info');
  if (bad.length === 0) return null;
  return (
    <Link
      href={`/services/${slug}/policy`}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 8,
        padding: '8px 0',
        borderBottom: '1px solid var(--licetum-border)',
        color: 'var(--licetum-text)',
      }}
    >
      <span
        aria-hidden
        style={{
          width: 8,
          height: 8,
          borderRadius: '50%',
          background: bad.some((f) => f.severity === 'error')
            ? 'var(--licetum-deny-fg)'
            : 'var(--licetum-except-fg)',
          flex: 'none',
        }}
      />
      <span style={{ flex: 1 }}>
        {t('home.attention.lint', { service: serviceName, count: bad.length })}
      </span>
      <ChevronRight size={14} aria-hidden />
    </Link>
  );
}
