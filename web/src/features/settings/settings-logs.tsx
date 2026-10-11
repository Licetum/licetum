'use client';

import {
  Alert,
  Button,
  Flex,
  Radio,
  Segmented,
  Select,
  Slider,
  Switch,
  Typography,
  message,
} from 'antd';
import { Plus, X } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useGetLogSettings, usePutLogSettings } from '@/api/admin/log-settings/log-settings';
import type { LogSettings } from '@/api/admin/model';
import { errorDetail, ErrorBlock, LoadingBlock, PageHeader } from '@/components/kit';
import { useAccess } from '@/lib/access';

import { useRefreshAll } from '../data/queries';
import { SettingsTabs } from './settings-admins';

const FIELDS = [
  'request_id',
  'method',
  'path',
  'operation',
  'user',
  'status',
  'duration_ms',
  'service',
  'subject',
  'type',
  'resource_id',
  'action',
  'allowed',
  'consistency',
  'time',
  'level',
  'msg',
];

const DEFAULTS: LogSettings = {
  format: 'json',
  level: 'info',
  fields: [],
  events: { access: true, decision: true, sync: true },
  subject: 'hash',
  sample_allowed: 0.1,
};

const PRESETS: Record<string, LogSettings> = {
  dev: {
    format: 'text',
    level: 'debug',
    fields: [],
    events: { access: true, decision: true, sync: true },
    subject: 'plain',
    sample_allowed: 1,
  },
  prod: {
    format: 'json',
    level: 'info',
    fields: [],
    events: { access: true, decision: false, sync: true },
    subject: 'hash',
    sample_allowed: 0.1,
  },
  audit: {
    format: 'json',
    level: 'info',
    fields: [],
    events: { access: false, decision: true, sync: false },
    subject: 'hash',
    sample_allowed: 1,
  },
};

/** A sample line of the log with the chosen settings: what will actually be written. */
function sampleLine(s: LogSettings): string {
  const all: Record<string, string | number | boolean> = {
    time: '2026-10-10T14:12:00Z',
    level: 'INFO',
    msg: 'decision',
    request_id: '3f1c…',
    method: 'GET',
    path: '/service/docs/permission',
    operation: 'get_permission',
    user: s.subject === 'omit' ? '' : s.subject === 'hash' ? 'a91f3c0d…' : 'alice',
    status: 200,
    duration_ms: 4,
    service: 'docs',
    subject: s.subject === 'omit' ? '' : s.subject === 'hash' ? 'a91f3c0d…' : 'alice',
    type: 'Документ',
    resource_id: 'd42',
    action: 'read',
    allowed: true,
    consistency: 'minimize_latency',
  };
  const chosen = s.fields.length ? s.fields : Object.keys(all).map((name) => ({ name, as: '' }));
  const pairs = chosen
    .filter((f) => !(s.subject === 'omit' && ['user', 'subject'].includes(f.name)))
    .map((f) => [f.as || f.name, all[f.name] ?? ''] as const);
  if (s.format === 'json') return JSON.stringify(Object.fromEntries(pairs));
  if (s.format === 'logfmt')
    return pairs
      .map(([k, v]) => `${k}=${typeof v === 'string' && /\s/.test(v) ? JSON.stringify(v) : v}`)
      .join(' ');
  return pairs.map(([, v]) => v).join(' ');
}

export function SettingsLogs() {
  const { t } = useTranslation();
  const access = useAccess();
  const query = useGetLogSettings({
    query: { select: (r) => (r.status === 200 ? r.data : undefined) },
  });
  const put = usePutLogSettings();
  const refresh = useRefreshAll();
  const [api, holder] = message.useMessage();

  const [draft, setDraft] = useState<LogSettings | null>(null);
  const [error, setError] = useState<unknown>(null);
  const saved = query.data;
  useEffect(() => {
    if (saved && !draft) setDraft(saved);
  }, [saved, draft]);

  const dirty = useMemo(
    () => !!draft && !!saved && JSON.stringify(draft) !== JSON.stringify(saved),
    [draft, saved],
  );
  const canWrite = access.whole('service', 'write');

  if (query.isPending)
    return (
      <div className="page">
        <LoadingBlock rows={6} />
      </div>
    );
  if (query.isError || !draft)
    return (
      <div className="page">
        <ErrorBlock error={query.error} onRetry={() => void query.refetch()} />
      </div>
    );

  const patch = (next: Partial<LogSettings>) => setDraft({ ...draft, ...next });
  const custom = draft.fields.length > 0;

  const save = async () => {
    setError(null);
    try {
      const res = await put.mutateAsync({ data: draft });
      await refresh();
      setDraft(res.data as LogSettings);
      void api.success(t('logs.saved'), 5);
    } catch (e) {
      setError(e);
    }
  };

  const warnPlain = draft.subject === 'plain';

  return (
    <div className="page">
      {holder}
      <PageHeader title={t('settings.title')} />
      <SettingsTabs />
      <Typography.Paragraph type="secondary">{t('logs.scope')}</Typography.Paragraph>

      <Flex gap={16} wrap align="flex-start">
        <section
          style={{
            flex: '1 1 460px',
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: 16,
          }}
          aria-label={t('logs.title')}
        >
          <Flex vertical gap={16}>
            {error != null && (
              <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
            )}
            <Row label={t('logs.presets')}>
              <Flex gap={8} wrap>
                {Object.keys(PRESETS).map((p) => (
                  <Button
                    key={p}
                    size="small"
                    disabled={!canWrite}
                    onClick={() => setDraft(PRESETS[p]!)}
                  >
                    {t(`logs.preset.${p}`)}
                  </Button>
                ))}
              </Flex>
            </Row>
            <Row label={t('logs.formatLabel')}>
              <Segmented
                value={draft.format}
                disabled={!canWrite}
                onChange={(v) => patch({ format: v as LogSettings['format'] })}
                options={[
                  { label: 'JSON', value: 'json' },
                  { label: 'logfmt', value: 'logfmt' },
                  { label: t('logs.format.text'), value: 'text' },
                ]}
                aria-label={t('logs.formatLabel')}
              />
            </Row>
            <Row label={t('logs.level')}>
              <Select
                value={draft.level}
                disabled={!canWrite}
                onChange={(level) => patch({ level })}
                style={{ width: 160 }}
                options={['debug', 'info', 'warn', 'error'].map((l) => ({ value: l }))}
                aria-label={t('logs.level')}
              />
            </Row>
            <Row label={t('logs.events')}>
              <Flex vertical gap={8}>
                {(['access', 'decision', 'sync'] as const).map((e) => (
                  <Flex key={e} align="center" gap={8}>
                    <Switch
                      checked={draft.events[e]}
                      disabled={!canWrite}
                      onChange={(v) => patch({ events: { ...draft.events, [e]: v } })}
                      aria-label={t(`logs.event.${e}`)}
                    />
                    <span>{t(`logs.event.${e}`)}</span>
                  </Flex>
                ))}
              </Flex>
            </Row>
            <Row label={t('logs.sampleLabel')} help={t('logs.sample.help')}>
              <Slider
                min={0}
                max={100}
                step={5}
                disabled={!canWrite}
                value={Math.round(draft.sample_allowed * 100)}
                onChange={(v) => patch({ sample_allowed: v / 100 })}
                tooltip={{ formatter: (v) => `${v}%` }}
                style={{ maxWidth: 360 }}
                aria-label={t('logs.sampleLabel')}
              />
            </Row>
            <Row label={t('logs.subjectLabel')}>
              <Radio.Group
                value={draft.subject}
                disabled={!canWrite}
                onChange={(e) => patch({ subject: e.target.value })}
                aria-label={t('logs.subjectLabel')}
              >
                <Radio value="hash">{t('logs.subject.hash')}</Radio>
                <Radio value="plain">{t('logs.subject.plain')}</Radio>
                <Radio value="omit">{t('logs.subject.omit')}</Radio>
              </Radio.Group>
              {warnPlain && (
                <Alert
                  style={{ marginTop: 8 }}
                  type="warning"
                  showIcon
                  title={t('logs.subject.plainWarn')}
                />
              )}
            </Row>
            <Row label={t('logs.fieldsLabel')}>
              <Segmented
                value={custom ? 'custom' : 'all'}
                disabled={!canWrite}
                onChange={(v) =>
                  patch({
                    fields:
                      v === 'all' ? [] : [{ name: 'time' }, { name: 'level' }, { name: 'msg' }],
                  })
                }
                options={[
                  { label: t('logs.fields.all'), value: 'all' },
                  { label: t('logs.fields.choose'), value: 'custom' },
                ]}
                aria-label={t('logs.fieldsLabel')}
              />
              {custom && (
                <Flex vertical gap={8} style={{ marginTop: 8 }}>
                  {draft.fields.map((f, i) => (
                    <Flex key={i} gap={8} align="center">
                      <Select
                        showSearch
                        style={{ width: 200 }}
                        value={f.name}
                        aria-label={t('logs.field')}
                        onChange={(name) =>
                          patch({
                            fields: draft.fields.map((x, j) => (j === i ? { ...x, name } : x)),
                          })
                        }
                        options={FIELDS.map((n) => ({ value: n }))}
                      />
                      <span aria-hidden>→</span>
                      <Select
                        mode="tags"
                        maxCount={1}
                        style={{ width: 200 }}
                        value={f.as ? [f.as] : []}
                        placeholder={t('logs.fields.rename')}
                        aria-label={t('logs.fields.rename')}
                        onChange={(v) =>
                          patch({
                            fields: draft.fields.map((x, j) =>
                              j === i ? { ...x, as: v[0] ?? '' } : x,
                            ),
                          })
                        }
                      />
                      <Button
                        type="text"
                        aria-label={t('cond.remove')}
                        icon={<X size={14} aria-hidden />}
                        onClick={() => patch({ fields: draft.fields.filter((_, j) => j !== i) })}
                      />
                    </Flex>
                  ))}
                  <div>
                    <Button
                      size="small"
                      icon={<Plus size={14} aria-hidden />}
                      onClick={() => patch({ fields: [...draft.fields, { name: 'status' }] })}
                    >
                      {t('logs.fields.add')}
                    </Button>
                  </div>
                </Flex>
              )}
            </Row>
            <Flex gap={8} justify="flex-end">
              <Button disabled={!canWrite} onClick={() => setDraft(DEFAULTS)}>
                {t('logs.reset')}
              </Button>
              <Button
                type="primary"
                disabled={!canWrite || !dirty}
                loading={put.isPending}
                onClick={() => void save()}
              >
                {t('common.save')}
              </Button>
            </Flex>
          </Flex>
        </section>

        <section
          style={{
            flex: '1 1 380px',
            background: 'var(--licetum-surface)',
            border: '1px solid var(--licetum-border)',
            borderRadius: 8,
            padding: 16,
          }}
          aria-labelledby="preview"
        >
          <Typography.Title level={5} id="preview" style={{ marginTop: 0 }}>
            {t('logs.preview')}
          </Typography.Title>
          <pre
            className="mono"
            style={{
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              margin: 0,
              padding: 12,
              background: 'var(--licetum-inherit-bg)',
              borderRadius: 8,
            }}
            data-testid="log-preview"
          >
            {sampleLine(draft)}
          </pre>
          <Typography.Paragraph
            type="secondary"
            style={{ fontSize: 12, marginTop: 12, marginBottom: 0 }}
          >
            {t('logs.applied')}
          </Typography.Paragraph>
        </section>
      </Flex>
    </div>
  );
}

function Row({
  label,
  help,
  children,
}: {
  label: string;
  help?: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div style={{ fontWeight: 600, marginBottom: 6 }}>{label}</div>
      {children}
      {help && (
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {help}
        </Typography.Text>
      )}
    </div>
  );
}
