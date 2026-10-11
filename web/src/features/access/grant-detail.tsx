'use client';

import { Alert, Button, DatePicker, Descriptions, Flex, Modal, Timeline, Typography } from 'antd';
import dayjs from 'dayjs';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useTranslation } from 'react-i18next';

import { EditableTitle } from '@/components/editable-title';
import { EmptyBlock, ErrorBlock, LoadingBlock, PageHeader } from '@/components/kit';
import { usePreferences } from '@/components/providers';
import { formatDateTime } from '@/domain/format';
import { describeConditions, describeWho, hasId, ruleDecision, type Rule } from '@/domain/rule';
import { useAccess } from '@/lib/access';

import { useChangeLog, useGroupNames, useRules } from '../data/queries';
import { useGrant } from '../grant/grant-context';
import { useRuleActions } from './rule-actions';
import { StatusTag, useRuleTitle, WhatCell, WhereCell, WhoCell } from './rule-cells';

export function GrantDetail({ ruleId }: { ruleId: string }) {
  const { t } = useTranslation();
  const { lang } = usePreferences();
  const router = useRouter();
  const access = useAccess();
  const grant = useGrant();
  const rules = useRules();
  const groups = useGroupNames();
  const log = useChangeLog({ entity: 'assignment_rule' });
  const actions = useRuleActions();
  const ruleTitle = useRuleTitle();

  const rule = rules.data?.find((r) => r.id === ruleId);
  const canWrite =
    access.whole('assignment_rule', 'write') || access.any('assignment_rule', 'write');

  if (rules.isPending)
    return (
      <div className="page">
        <LoadingBlock rows={6} />
      </div>
    );
  if (rules.isError)
    return (
      <div className="page">
        <ErrorBlock error={rules.error} onRetry={() => void rules.refetch()} />
      </div>
    );
  if (!rule) {
    return (
      <div className="page">
        <EmptyBlock
          title={t('grantDetail.gone.title')}
          hint={t('grantDetail.gone.hint')}
          action={
            <Flex gap={8} justify="center">
              <Button href="/history">{t('grantDetail.gone.history')}</Button>
              <Button type="primary" href="/access">
                {t('grantDetail.gone.list')}
              </Button>
            </Flex>
          }
        />
      </div>
    );
  }

  const who = describeWho(rule, groups.name, t);
  const title = ruleTitle(rule);
  const history = (log.data ?? [])
    .filter((r) => r.entity_id === rule.id)
    .slice()
    .reverse();

  const extend = (days: number | null, date?: string) => {
    const base =
      rule.expires_at && Date.parse(rule.expires_at) > Date.now()
        ? new Date(rule.expires_at)
        : new Date();
    let next: string | null;
    if (date) next = new Date(`${date}T23:59:59`).toISOString();
    else if (days === null) next = null;
    else {
      const d = new Date(base);
      d.setDate(d.getDate() + days);
      next = d.toISOString();
    }
    void actions.update(rule, { expires_at: next }, t('grantDetail.termChanged'));
  };

  const confirmDelete = () =>
    Modal.confirm({
      title: t('rule.deleteTitle', { count: 1 }),
      content: t('rule.deleteBody'),
      okText: t('common.delete'),
      okButtonProps: { danger: true },
      cancelText: t('common.cancel'),
      onOk: async () => {
        if (await actions.remove([rule])) router.push('/access');
      },
    });

  const duplicate = () =>
    grant.open({
      who:
        who.kind === 'group' && who.groupId
          ? { kind: 'group', id: who.groupId }
          : who.kind === 'person' && who.username
            ? { kind: 'person', username: who.username }
            : undefined,
      serviceId: rule.service_id ?? undefined,
      typeId: hasId(rule.type_resource_id) ? rule.type_resource_id : undefined,
      roleId: hasId(rule.role_id) ? rule.role_id : undefined,
    });

  return (
    <div className="page">
      <PageHeader
        crumbs={[{ label: t('nav.access'), href: '/access' }, { label: title }]}
        title={
          canWrite ? (
            <EditableTitle
              value={rule.name ?? ''}
              display={title}
              label={t('grantDetail.rename')}
              onSave={(value) =>
                void actions.update(rule, { name: value.trim() || null }, t('grantDetail.renamed'))
              }
            />
          ) : (
            title
          )
        }
        tag={<StatusTag rule={rule} />}
        extra={
          <>
            {canWrite && (
              <Button
                disabled={actions.busy}
                onClick={() => void actions.setEnabled([rule], !rule.enabled)}
              >
                {rule.enabled ? t('rule.disable') : t('rule.enable')}
              </Button>
            )}
            <Button
              href={
                who.kind === 'person' && who.username
                  ? `/check?subject=${encodeURIComponent(who.username)}`
                  : '/check'
              }
            >
              {t('grantDetail.check')}
            </Button>
            {canWrite && <Button onClick={duplicate}>{t('grantDetail.duplicate')}</Button>}
            {canWrite && (
              <Button danger onClick={confirmDelete} disabled={actions.busy}>
                {t('common.delete')}
              </Button>
            )}
          </>
        }
      />

      {!rule.enabled && (
        <Alert
          type="warning"
          showIcon
          title={t('grantDetail.disabledNote')}
          style={{ marginBottom: 16 }}
        />
      )}
      {rule.enabled && !rule.target_valid && (
        <Alert
          type="error"
          showIcon
          title={t('grantDetail.deadNote')}
          style={{ marginBottom: 16 }}
        />
      )}
      {ruleDecision(rule) === 'except' && (
        <Alert
          type="info"
          showIcon
          title={t('grantDetail.exceptionNote')}
          style={{ marginBottom: 16 }}
        />
      )}

      <Flex gap={16} wrap align="flex-start">
        <div
          style={{
            flex: '1 1 420px',
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: 16,
          }}
        >
          <Typography.Title level={5} style={{ marginTop: 0 }}>
            {t('grantDetail.rule')}
          </Typography.Title>
          <Descriptions
            column={1}
            size="small"
            styles={{ label: { width: 120, color: 'var(--licetum-muted)' } }}
          >
            <Descriptions.Item label={t('access.col.who')}>
              <WhoCell rule={rule} />
            </Descriptions.Item>
            <Descriptions.Item label={t('access.col.what')}>
              <WhatCell rule={rule} />
            </Descriptions.Item>
            <Descriptions.Item label={t('access.col.where')}>
              <WhereCell rule={rule} />
            </Descriptions.Item>
            <Descriptions.Item label={t('access.col.conditions')}>
              <Conditions rule={rule} lang={lang} />
            </Descriptions.Item>
            {rule.subject_kind && (
              <Descriptions.Item label={t('grantDetail.kind')}>
                {t(`rule.subjectKind.${rule.subject_kind}`, { defaultValue: rule.subject_kind })}
              </Descriptions.Item>
            )}
            {rule.description && (
              <Descriptions.Item label={t('grantDetail.description')}>
                {rule.description}
              </Descriptions.Item>
            )}
          </Descriptions>
        </div>

        <div
          style={{
            flex: '1 1 420px',
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: 16,
          }}
        >
          <Typography.Title level={5} style={{ marginTop: 0 }}>
            {t('grantDetail.term')}
          </Typography.Title>
          <Typography.Paragraph>
            {rule.expires_at
              ? t('grantDetail.untilDate', { date: formatDateTime(rule.expires_at, lang) })
              : t('grantDetail.forever')}
            {rule.valid_from
              ? ` · ${t('rule.from', { date: formatDateTime(rule.valid_from, lang) })}`
              : ''}
          </Typography.Paragraph>
          {canWrite && (
            <Flex gap={8} wrap align="center">
              <span style={{ color: 'var(--licetum-muted)' }}>{t('grantDetail.extend')}</span>
              <Button size="small" onClick={() => extend(7)}>
                {t('grantDetail.plus7')}
              </Button>
              <Button size="small" onClick={() => extend(30)}>
                {t('grantDetail.plus30')}
              </Button>
              <DatePicker
                size="small"
                aria-label={t('grantDetail.untilPick')}
                placeholder={t('grantDetail.untilPick')}
                value={rule.expires_at ? dayjs(rule.expires_at) : null}
                onChange={(d) => d && extend(null, d.format('YYYY-MM-DD'))}
              />
              <Button size="small" onClick={() => extend(null)} disabled={!rule.expires_at}>
                {t('grantDetail.forever')}
              </Button>
            </Flex>
          )}
          <Typography.Paragraph
            type="secondary"
            style={{ fontSize: 12, marginTop: 12, marginBottom: 0 }}
          >
            {t('grantDetail.undoNote')}
          </Typography.Paragraph>
        </div>
      </Flex>

      <div
        style={{
          marginTop: 16,
          background: 'var(--licetum-surface)',
          border: '1px solid var(--licetum-border)',
          borderRadius: 8,
          padding: 16,
        }}
      >
        <Flex justify="space-between" align="center" wrap>
          <Typography.Title level={5} style={{ margin: 0 }}>
            {t('grantDetail.history')}
          </Typography.Title>
          <Link href="/history">{t('grantDetail.allHistory')}</Link>
        </Flex>
        {log.isPending ? (
          <LoadingBlock rows={2} />
        ) : history.length === 0 ? (
          <Typography.Text type="secondary">{t('grantDetail.noHistory')}</Typography.Text>
        ) : (
          <Timeline
            style={{ marginTop: 16 }}
            items={history.map((h) => ({
              content: (
                <span>
                  <span style={{ color: 'var(--licetum-muted)' }}>
                    {formatDateTime(h.at, lang)} · {h.actor || t('history.system')}
                  </span>{' '}
                  — {t(`history.verb.${h.action}`)}
                </span>
              ),
            }))}
          />
        )}
      </div>
    </div>
  );
}

function Conditions({ rule, lang }: { rule: Rule; lang: string }) {
  const { t } = useTranslation();
  const lines: string[] = [];
  if (rule.valid_from)
    lines.push(t('rule.from', { date: new Date(rule.valid_from).toLocaleDateString(lang) }));
  if (rule.expires_at)
    lines.push(t('rule.until', { date: new Date(rule.expires_at).toLocaleDateString(lang) }));
  if (rule.requires_predicate?.length)
    lines.push(t('rule.onlyIf', { cond: describeConditions(rule.requires_predicate) }));
  if (hasId(rule.requires_group_id)) lines.push(t('rule.guardGroup'));
  if (rule.effect === 'deny') lines.push(t(`grantDetail.onMissing.${rule.on_missing_attribute}`));
  return lines.length ? <>{lines.join(' · ')}</> : <>—</>;
}
