'use client';

import {
  Alert,
  Button,
  Descriptions,
  Flex,
  Input,
  Segmented,
  Select,
  Table,
  Typography,
  message,
} from 'antd';
import { Copy } from 'lucide-react';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  AUTHZEN_OPERATIONS,
  AUTHZEN_PATHS,
  PLACEHOLDERS,
  pdpUrl,
  requestOf,
  snippet,
  type AuthzenOperation,
  type Lang,
  type Sample,
} from '@/lib/authzen';

import { useAllTypes } from '../data/queries';
import { DelegationSection } from './delegation-section';
import { useService } from './service-frame';

/** How the code of a service asks Licetum: ready-made calls with the service's own slug and type names, and a way to try one. */
export function IntegrationTab({ checkUrl }: { checkUrl: string }) {
  const { t } = useTranslation();
  const { service } = useService();
  const types = useAllTypes();
  const [api, holder] = message.useMessage();
  const own = (types.data ?? []).filter((x) => x.service_id === service.id);

  const [operation, setOperation] = useState<AuthzenOperation>('evaluation');
  const [lang, setLang] = useState<Lang>('curl');
  const [person, setPerson] = useState('');
  const [typeName, setTypeName] = useState<string | undefined>(undefined);
  const [resource, setResource] = useState('');
  const [action, setAction] = useState('read');
  const [token, setToken] = useState('');
  const [answer, setAnswer] = useState<{
    status: number;
    revision: string | null;
    body: string;
  } | null>(null);
  const [busy, setBusy] = useState(false);

  const type = typeName ?? own[0]?.name ?? '';
  // What is tried is what is shown, with the placeholders of the example filled in by what was typed.
  const sample: Sample = { person, type, resource, action: action.trim() || 'read' };
  const shown: Sample = {
    ...sample,
    person: person.trim() || PLACEHOLDERS.person,
    type: type || PLACEHOLDERS.type,
    resource: resource.trim() || (operation === 'search_resource' ? '' : PLACEHOLDERS.resource),
  };

  const code = useMemo(
    () => snippet(lang, pdpUrl(checkUrl, service.slug, operation), requestOf(operation, shown)),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- shown is derived from the fields below
    [lang, checkUrl, service.slug, operation, person, type, resource, action],
  );

  const needsPerson = operation !== 'search_subject';
  const run = async () => {
    setBusy(true);
    setAnswer(null);
    try {
      const res = await fetch(
        `/api/check/service/${encodeURIComponent(service.slug)}/${AUTHZEN_PATHS[operation]}`,
        {
          method: 'POST',
          headers: {
            'x-test-token': token.trim(),
            'content-type': 'application/json',
            'accept-language': document.documentElement.lang,
          },
          body: JSON.stringify(requestOf(operation, { ...sample, person: person.trim() })),
        },
      );
      const text = await res.text();
      let pretty = text;
      try {
        pretty = JSON.stringify(JSON.parse(text), null, 2);
      } catch {
        /* not JSON */
      }
      setAnswer({
        status: res.status,
        revision: res.headers.get('x-authz-revision'),
        body: pretty,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Flex vertical gap={16}>
      {holder}
      <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
        {t('integration.hint')}
      </Typography.Paragraph>

      <Flex gap={12} wrap align="center">
        <Segmented
          aria-label={t('integration.modeLabel')}
          value={operation}
          onChange={(v) => setOperation(v as AuthzenOperation)}
          options={AUTHZEN_OPERATIONS.map((op) => ({
            label: t(`integration.op.${op}`),
            value: op,
          }))}
        />
        <Segmented
          aria-label={t('integration.lang')}
          value={lang}
          onChange={(v) => setLang(v as Lang)}
          options={[
            { label: 'curl', value: 'curl' },
            { label: 'Go', value: 'go' },
            { label: 'TypeScript', value: 'ts' },
            { label: 'Python', value: 'python' },
          ]}
        />
      </Flex>
      <Typography.Text type="secondary" style={{ fontSize: 13 }}>
        {t(`integration.about.${operation}`)}
      </Typography.Text>
      <Flex gap={12} wrap align="flex-end">
        {needsPerson && (
          <Field label={t('integration.sample.person')}>
            <Input
              aria-label={t('integration.sample.person')}
              placeholder={PLACEHOLDERS.person}
              className="mono"
              style={{ width: 180 }}
              value={person}
              onChange={(e) => setPerson(e.target.value)}
            />
          </Field>
        )}
        <Field label={t('integration.sample.type')}>
          <Select
            aria-label={t('integration.sample.type')}
            style={{ width: 200 }}
            value={type || undefined}
            onChange={setTypeName}
            options={own.map((x) => ({ value: x.name, label: x.name }))}
          />
        </Field>
        {operation !== 'search_resource' && (
          <Field label={t('integration.sample.resource')}>
            <Input
              aria-label={t('integration.sample.resource')}
              placeholder={PLACEHOLDERS.resource}
              className="mono"
              style={{ width: 220 }}
              value={resource}
              onChange={(e) => setResource(e.target.value)}
            />
          </Field>
        )}
        {operation !== 'search_action' && (
          <Field label={t('integration.sample.action')}>
            <Input
              aria-label={t('integration.sample.action')}
              className="mono"
              style={{ width: 120 }}
              value={action}
              onChange={(e) => setAction(e.target.value)}
            />
          </Field>
        )}
      </Flex>

      <div style={{ position: 'relative' }}>
        <pre
          className="mono"
          style={{
            margin: 0,
            padding: '14px 16px',
            background: 'var(--licetum-inherit-bg)',
            borderRadius: 8,
            overflowX: 'auto',
            whiteSpace: 'pre',
          }}
          data-testid="snippet"
        >
          {code}
        </pre>
        <Button
          size="small"
          icon={<Copy size={14} aria-hidden />}
          aria-label={t('common.copy')}
          style={{ position: 'absolute', top: 8, right: 8 }}
          onClick={async () => {
            await navigator.clipboard?.writeText(code);
            void api.success(t('common.copied'), 1.5);
          }}
        />
      </div>

      <div
        style={{
          background: 'var(--licetum-surface)',
          border: '1px solid var(--licetum-border)',
          borderRadius: 8,
          padding: 16,
        }}
      >
        <Typography.Title level={5} style={{ marginTop: 0 }}>
          {t('integration.try.title')}
        </Typography.Title>
        <Typography.Paragraph type="secondary" style={{ fontSize: 13 }}>
          {t('integration.try.hint')}
        </Typography.Paragraph>
        <Input.TextArea
          rows={3}
          aria-label={t('integration.try.token')}
          placeholder={t('integration.try.token')}
          value={token}
          onChange={(e) => setToken(e.target.value)}
          className="mono"
          autoComplete="off"
          spellCheck={false}
        />
        <Flex gap={8} style={{ marginTop: 8 }} align="center">
          <Button
            type="primary"
            disabled={!token.trim() || !type || (needsPerson && !person.trim())}
            loading={busy}
            onClick={() => void run()}
          >
            {t('integration.try.run')}
          </Button>
          {answer && (
            <Typography.Text
              style={{
                color: answer.status < 300 ? 'var(--licetum-allow-fg)' : 'var(--licetum-deny-fg)',
              }}
            >
              {answer.status}
              {answer.revision ? ` · X-Authz-Revision: ${answer.revision}` : ''}
            </Typography.Text>
          )}
        </Flex>
        {answer && (
          <>
            <pre
              className="mono"
              style={{
                margin: '12px 0 0',
                padding: 12,
                background: 'var(--licetum-inherit-bg)',
                borderRadius: 8,
                maxHeight: 360,
                overflow: 'auto',
              }}
            >
              {answer.body}
            </pre>
            {answer.status === 401 && (
              <Alert
                style={{ marginTop: 8 }}
                type="warning"
                showIcon
                title={t('integration.try.401')}
              />
            )}
            {answer.status === 403 && (
              <Alert
                style={{ marginTop: 8 }}
                type="warning"
                showIcon
                title={t('integration.try.403')}
              />
            )}
            {answer.status === 424 && (
              <Alert
                style={{ marginTop: 8 }}
                type="warning"
                showIcon
                title={t('integration.try.424')}
              />
            )}
          </>
        )}
      </div>

      <div>
        <Typography.Title level={5}>{t('integration.codes.title')}</Typography.Title>
        <Table
          size="small"
          pagination={false}
          rowKey="code"
          dataSource={['200', '400', '401', '403', '424', '429'].map((code) => ({ code }))}
          columns={[
            {
              title: t('integration.codes.status'),
              dataIndex: 'code',
              width: 90,
              render: (c: string) => <b className="mono">{c}</b>,
            },
            {
              title: t('integration.codes.meaning'),
              key: 'm',
              render: (_, r) => t(`integration.codes.${r.code}`),
            },
          ]}
        />
      </div>

      <DelegationSection checkUrl={checkUrl} />

      <Alert
        type="info"
        showIcon
        title={t('integration.fresh.title')}
        description={t('integration.fresh.body')}
      />
      <Descriptions
        size="small"
        column={1}
        styles={{ label: { width: 140, color: 'var(--licetum-muted)' } }}
      >
        <Descriptions.Item label={t('integration.api')}>
          <span className="mono">{checkUrl}</span>
        </Descriptions.Item>
        <Descriptions.Item label="OpenAPI">
          <a href={`${checkUrl}/openapi.json`} target="_blank" rel="noreferrer">
            {checkUrl}/openapi.json
          </a>
        </Descriptions.Item>
        <Descriptions.Item label="Swagger UI">
          <a href={`${checkUrl}/docs`} target="_blank" rel="noreferrer">
            {checkUrl}/docs
          </a>
        </Descriptions.Item>
      </Descriptions>
    </Flex>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div style={{ fontSize: 12, color: 'var(--licetum-muted)', marginBottom: 4 }}>{label}</div>
      {children}
    </div>
  );
}
