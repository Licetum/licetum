'use client';

import { Alert, Button, Drawer, Flex, Modal, Select, Switch, Tag, Typography } from 'antd';
import { useRouter } from 'next/navigation';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { BatchOperation, ChangeRecord } from '@/api/admin/model';
import { EmptyBlock, ErrorBlock, LoadingBlock, PageHeader } from '@/components/kit';
import { usePreferences } from '@/components/providers';
import { diffFields, entityName, printValue, REVERTIBLE } from '@/domain/changes';
import { dayLabel, formatDateTime, formatTime } from '@/domain/format';
import { useAccess } from '@/lib/access';
import { useQueryList, useQueryState } from '@/lib/use-query-state';

import { useChangeLog, useServices } from '../data/queries';
import { useChangeSentence } from './change-line';
import { useRevert } from './use-revert';

const PAGE = 100;
const ENTITIES = [
  'assignment_rule',
  'group',
  'group_member',
  'role',
  'role_permission',
  'access_group',
  'type_resource',
  'synchronizer',
  'service',
  'log_settings',
];

export function HistoryScreen() {
  const { t } = useTranslation();
  const { lang } = usePreferences();
  const router = useRouter();
  const access = useAccess();
  const services = useServices();
  const sentence = useChangeSentence();
  const revert = useRevert();

  const [live, setLive] = useState(false);
  const log = useChangeLog({ liveMs: live ? 5000 : undefined });

  const [selectedParam, setSelected] = useQueryState('change');
  const [svc, setSvc] = useQueryList('service');
  const [kind, setKind] = useQueryList('entity');
  const [actor, setActor] = useQueryList('actor');
  const [period, setPeriod] = useQueryState('period', 'all');
  const [limit, setLimit] = useState(PAGE);
  const [plan, setPlan] = useState<{
    record: ChangeRecord;
    operations: BatchOperation[];
    mode: 'one' | 'to';
  } | null>(null);
  const [planError, setPlanError] = useState<string | null>(null);
  const [raw, setRaw] = useState(false);

  const canRevert = access.whole('assignment_rule', 'write');
  const serviceName = (id: string | null) =>
    id ? (services.data?.find((s) => s.id === id)?.name ?? '') : '';

  const all = useMemo(() => (log.data ?? []).slice().reverse(), [log.data]);
  const actors = useMemo(() => [...new Set(all.map((r) => r.actor).filter(Boolean))].sort(), [all]);

  const filtered = useMemo(() => {
    const now = Date.now();
    const cut =
      period === 'today'
        ? new Date().setHours(0, 0, 0, 0)
        : period === 'week'
          ? now - 7 * 86_400_000
          : 0;
    return all.filter((r) => {
      if (svc.length && !svc.includes(r.service_id ?? '')) return false;
      if (kind.length && !kind.includes(r.entity)) return false;
      if (actor.length && !actor.includes(r.actor)) return false;
      if (cut && Date.parse(r.at) < cut) return false;
      return true;
    });
  }, [all, svc, kind, actor, period]);

  const selected = selectedParam ? all.find((r) => String(r.id) === selectedParam) : undefined;
  const shown = filtered.slice(0, limit);

  const groupedByDay = useMemo(() => {
    const out: { day: string; items: ChangeRecord[] }[] = [];
    for (const r of shown) {
      const day = dayLabel(r.at, lang, t('history.today'), t('history.yesterday'));
      const last = out.at(-1);
      if (last && last.day === day) last.items.push(r);
      else out.push({ day, items: [r] });
    }
    return out;
  }, [shown, lang, t]);

  const filtersOn = svc.length + kind.length + actor.length > 0 || period !== 'all';
  const reset = () => {
    setSvc([]);
    setKind([]);
    setActor([]);
    setPeriod(null);
  };

  const openPlan = async (record: ChangeRecord, mode: 'one' | 'to') => {
    setPlanError(null);
    try {
      const result =
        mode === 'one' ? await revert.planOne(record.id) : await revert.planTo(record.id);
      setPlan({
        record,
        operations: (result as { operations: BatchOperation[] }).operations ?? [],
        mode,
      });
    } catch (e) {
      const detail = (e as { data?: { detail?: string } }).data?.detail;
      setPlan({ record, operations: [], mode });
      setPlanError(detail ?? t('history.cannotRevert'));
    }
  };

  return (
    <div className="page">
      <PageHeader
        title={t('history.title')}
        extra={
          <Flex align="center" gap={8}>
            <Switch checked={live} onChange={setLive} aria-label={t('history.live')} />
            <span>{live ? t('history.liveOn') : t('history.liveOff')}</span>
          </Flex>
        }
      />

      <Flex
        gap={8}
        wrap
        align="center"
        style={{ marginBottom: 16 }}
        role="group"
        aria-label={t('access.filters')}
      >
        <Select
          mode="multiple"
          allowClear
          maxTagCount="responsive"
          aria-label={t('history.filter.service')}
          placeholder={t('history.filter.service')}
          style={{ minWidth: 180 }}
          value={svc}
          onChange={setSvc}
          options={(services.data ?? []).map((s) => ({ value: s.id, label: s.name }))}
          popupMatchSelectWidth={false}
        />
        <Select
          mode="multiple"
          allowClear
          maxTagCount="responsive"
          aria-label={t('history.filter.what')}
          placeholder={t('history.filter.what')}
          style={{ minWidth: 180 }}
          value={kind}
          onChange={setKind}
          options={ENTITIES.map((e) => ({ value: e, label: t(`history.entity.${e}`) }))}
          popupMatchSelectWidth={false}
        />
        <Select
          mode="multiple"
          allowClear
          maxTagCount="responsive"
          aria-label={t('history.filter.who')}
          placeholder={t('history.filter.who')}
          style={{ minWidth: 160 }}
          value={actor}
          onChange={setActor}
          options={actors.map((a) => ({ value: a, label: a }))}
          popupMatchSelectWidth={false}
        />
        <Select
          aria-label={t('history.filter.period')}
          style={{ minWidth: 150 }}
          value={period}
          onChange={setPeriod}
          options={[
            { value: 'all', label: t('history.period.all') },
            { value: 'today', label: t('history.period.today') },
            { value: 'week', label: t('history.period.week') },
          ]}
        />
        {filtersOn && (
          <Button type="link" onClick={reset}>
            {t('common.resetFilters')}
          </Button>
        )}
        <span style={{ flex: 1 }} />
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {t('history.count', { shown: filtered.length, total: all.length })}
        </Typography.Text>
      </Flex>

      {log.isPending ? (
        <LoadingBlock rows={6} />
      ) : log.isError ? (
        <ErrorBlock error={log.error} onRetry={() => void log.refetch()} />
      ) : all.length === 0 ? (
        <EmptyBlock title={t('history.empty.title')} hint={t('history.empty.hint')} />
      ) : filtered.length === 0 ? (
        <EmptyBlock
          title={t('history.noMatch')}
          action={<Button onClick={reset}>{t('common.resetFilters')}</Button>}
        />
      ) : (
        <div
          role="list"
          aria-label={t('history.title')}
          style={{
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
          }}
        >
          {groupedByDay.map((g) => (
            <div key={g.day}>
              <div
                style={{
                  padding: '8px 16px',
                  background: 'var(--licetum-inherit-bg)',
                  color: 'var(--licetum-muted)',
                  fontSize: 12,
                  fontWeight: 600,
                }}
              >
                {g.day}
              </div>
              {g.items.map((r) => (
                <button
                  key={r.id}
                  type="button"
                  role="listitem"
                  onClick={() => setSelected(String(r.id))}
                  style={{
                    all: 'unset',
                    boxSizing: 'border-box',
                    display: 'flex',
                    gap: 12,
                    width: '100%',
                    padding: '10px 16px',
                    borderBottom: '1px solid var(--licetum-border)',
                    cursor: 'pointer',
                  }}
                >
                  <span className="mono" style={{ color: 'var(--licetum-muted)', minWidth: 48 }}>
                    {formatTime(r.at, lang)}
                  </span>
                  <span style={{ flex: 1, minWidth: 0 }}>
                    <b>{r.actor || t('history.system')}</b> {t(`history.verb.${r.action}`)}{' '}
                    {t(`history.entity.${r.entity}`)} «{entityName(r, t)}»
                    <span style={{ color: 'var(--licetum-muted)' }}>
                      {' '}
                      · {serviceName(r.service_id) || t('history.platformWide')}
                    </span>
                  </span>
                  <span style={{ color: 'var(--licetum-muted)', fontSize: 12 }}>#{r.id}</span>
                </button>
              ))}
            </div>
          ))}
          {filtered.length > limit && (
            <div style={{ padding: 12, textAlign: 'center' }}>
              <Button onClick={() => setLimit((n) => n + PAGE)}>{t('history.more')}</Button>
            </div>
          )}
        </div>
      )}

      <Drawer
        open={!!selected}
        onClose={() => {
          setSelected(null);
          setRaw(false);
        }}
        size={520}
        title={selected ? sentence(selected) : ''}
        destroyOnHidden
      >
        {selected && (
          <Flex vertical gap={16}>
            <Typography.Text type="secondary">
              {formatDateTime(selected.at, lang)} ·{' '}
              {serviceName(selected.service_id) || t('history.platformWide')} · #{selected.id}
            </Typography.Text>
            <Diff record={selected} />
            {raw && (
              <pre
                className="mono"
                style={{
                  whiteSpace: 'pre-wrap',
                  background: 'var(--licetum-inherit-bg)',
                  borderRadius: 8,
                  padding: 12,
                  margin: 0,
                }}
              >
                {JSON.stringify({ before: selected.before, after: selected.after }, null, 2)}
              </pre>
            )}
            <Flex gap={8} wrap>
              {REVERTIBLE.has(selected.entity) && selected.actor ? (
                <>
                  <Button
                    disabled={!canRevert || revert.busy}
                    onClick={() => void openPlan(selected, 'one')}
                  >
                    {t('history.revert')}
                  </Button>
                  <Button
                    disabled={!canRevert || revert.busy}
                    onClick={() => void openPlan(selected, 'to')}
                  >
                    {t('history.rollbackTo')}
                  </Button>
                </>
              ) : (
                <Tag color="warning">{t('history.noRevert')}</Tag>
              )}
              <Button onClick={() => setRaw((v) => !v)}>
                {raw ? t('history.hideRaw') : t('history.showRaw')}
              </Button>
              <Button
                onClick={() => {
                  const url = new URL(window.location.href);
                  url.searchParams.set('change', String(selected.id));
                  void navigator.clipboard?.writeText(url.toString());
                }}
              >
                {t('history.copyLink')}
              </Button>
              {selected.entity === 'assignment_rule' &&
                selected.entity_id &&
                selected.action !== 'delete' && (
                  <Button onClick={() => router.push(`/access/${selected.entity_id}`)}>
                    {t('history.openEntity')}
                  </Button>
                )}
            </Flex>
          </Flex>
        )}
      </Drawer>

      <Modal
        open={!!plan}
        title={plan?.mode === 'to' ? t('history.rollbackTitle') : t('history.revertTitle')}
        onCancel={() => setPlan(null)}
        okText={plan?.mode === 'to' ? t('history.rollbackConfirm') : t('history.revertConfirm')}
        okButtonProps={{
          danger: plan?.mode === 'to',
          disabled: !!planError || (plan?.operations.length ?? 0) === 0,
          loading: revert.busy,
        }}
        cancelText={t('common.cancel')}
        onOk={async () => {
          if (!plan) return;
          const ok =
            plan.mode === 'one'
              ? await revert.applyOne(plan.record.id)
              : await revert.applyTo(plan.record.id);
          if (ok) {
            setPlan(null);
            setSelected(null);
          }
        }}
      >
        {planError ? (
          <Alert type="error" showIcon title={planError} />
        ) : (
          <Flex vertical gap={8}>
            <Typography.Text>
              {plan?.mode === 'to'
                ? t('history.rollbackBody', { count: plan.operations.length })
                : t('history.revertBody')}
            </Typography.Text>
            <ul style={{ margin: 0, paddingLeft: 20 }}>
              {plan?.operations.map((op, i) => (
                <li key={i}>
                  {t(`history.op.${op.op}`)} {t(`history.entity.${op.entity}`)}{' '}
                  {op.id ? <span className="mono">{String(op.id).slice(0, 8)}</span> : null}
                </li>
              ))}
            </ul>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {t('history.planNote')}
            </Typography.Text>
          </Flex>
        )}
      </Modal>
    </div>
  );
}

/** "Before → after" by field: only the fields that differ. */
function Diff({ record }: { record: ChangeRecord }) {
  const { t } = useTranslation();
  const fields = diffFields(record);
  if (fields.length === 0)
    return <Typography.Text type="secondary">{t('history.noDiff')}</Typography.Text>;
  return (
    <div style={{ border: '1px solid var(--licetum-border)', borderRadius: 8, overflow: 'hidden' }}>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: '120px 1fr 1fr',
          background: 'var(--licetum-inherit-bg)',
          fontWeight: 600,
          fontSize: 12,
          color: 'var(--licetum-muted)',
        }}
      >
        <div style={{ padding: '6px 10px' }}>{t('history.field')}</div>
        <div style={{ padding: '6px 10px' }}>{t('history.before')}</div>
        <div style={{ padding: '6px 10px' }}>{t('history.after')}</div>
      </div>
      {fields.map((f) => (
        <div
          key={f.field}
          style={{
            display: 'grid',
            gridTemplateColumns: '120px 1fr 1fr',
            borderTop: '1px solid var(--licetum-border)',
          }}
        >
          <div className="mono" style={{ padding: '6px 10px', color: 'var(--licetum-muted)' }}>
            {f.field}
          </div>
          <div
            className="mono"
            style={{
              padding: '6px 10px',
              wordBreak: 'break-word',
              ...(f.before !== undefined && f.before !== null
                ? { background: 'var(--licetum-deny-bg)', color: 'var(--licetum-deny-fg)' }
                : {}),
            }}
          >
            {printValue(f.before)}
          </div>
          <div
            className="mono"
            style={{
              padding: '6px 10px',
              wordBreak: 'break-word',
              ...(f.after !== undefined && f.after !== null
                ? { background: 'var(--licetum-allow-bg)', color: 'var(--licetum-allow-fg)' }
                : {}),
            }}
          >
            {printValue(f.after)}
          </div>
        </div>
      ))}
    </div>
  );
}
