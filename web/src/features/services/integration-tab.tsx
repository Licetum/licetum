'use client';

import {
  Alert,
  Button,
  Descriptions,
  Flex,
  Input,
  Segmented,
  Table,
  Typography,
  message,
} from 'antd';
import { Copy } from 'lucide-react';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useAllTypes } from '../data/queries';
import { DelegationSection } from './delegation-section';
import { useService } from './service-frame';

type Mode = 'permission' | 'check' | 'authzen';
type Lang = 'curl' | 'go' | 'ts' | 'python';

function snippet(mode: Mode, lang: Lang, base: string, slug: string, type: string): string {
  const url =
    mode === 'permission'
      ? `${base}/service/${slug}/permission`
      : mode === 'check'
        ? `${base}/service/${slug}/check`
        : `${base}/service/${slug}/access/v1/evaluation`;
  const body =
    mode === 'check'
      ? { checks: [{ type, resource_id: 'RESOURCE_ID', action: 'read' }], explain: true }
      : mode === 'authzen'
        ? {
            subject: { type: 'user', id: 'USERNAME' },
            resource: { type, id: 'RESOURCE_ID' },
            action: { name: 'read' },
          }
        : null;
  const json = body ? JSON.stringify(body, null, 2) : '';
  const post = mode !== 'permission';

  if (lang === 'curl') {
    return post
      ? `curl -X POST '${url}' \\\n  -H "Authorization: Bearer $TOKEN" \\\n  -H 'Content-Type: application/json' \\\n  -d '${JSON.stringify(body)}'`
      : `curl '${url}' \\\n  -H "Authorization: Bearer $TOKEN"`;
  }
  if (lang === 'go') {
    return post
      ? `body := strings.NewReader(\`${JSON.stringify(body)}\`)\nreq, _ := http.NewRequest(http.MethodPost, "${url}", body)\nreq.Header.Set("Authorization", "Bearer "+token)\nreq.Header.Set("Content-Type", "application/json")\nres, err := http.DefaultClient.Do(req)`
      : `req, _ := http.NewRequest(http.MethodGet, "${url}", nil)\nreq.Header.Set("Authorization", "Bearer "+token)\nres, err := http.DefaultClient.Do(req)`;
  }
  if (lang === 'ts') {
    return post
      ? `const res = await fetch('${url}', {\n  method: 'POST',\n  headers: { Authorization: \`Bearer \${token}\`, 'Content-Type': 'application/json' },\n  body: JSON.stringify(${json.replace(/\n/g, '\n  ')}),\n});\nconst answer = await res.json();`
      : `const res = await fetch('${url}', { headers: { Authorization: \`Bearer \${token}\` } });\nconst permissions = await res.json();`;
  }
  return post
    ? `import requests\n\nres = requests.post(\n    "${url}",\n    headers={"Authorization": f"Bearer {token}"},\n    json=${json.replace(/\n/g, '\n    ')},\n)\nanswer = res.json()`
    : `import requests\n\nres = requests.get("${url}", headers={"Authorization": f"Bearer {token}"})\npermissions = res.json()`;
}

/** How the code of a service asks Licetum: ready-made calls with the service's own slug and type names, and a way to try one. */
export function IntegrationTab({ checkUrl }: { checkUrl: string }) {
  const { t } = useTranslation();
  const { service } = useService();
  const types = useAllTypes();
  const [api, holder] = message.useMessage();
  const own = (types.data ?? []).filter((x) => x.service_id === service.id);

  const [mode, setMode] = useState<Mode>('permission');
  const [lang, setLang] = useState<Lang>('curl');
  const [token, setToken] = useState('');
  const [answer, setAnswer] = useState<{
    status: number;
    revision: string | null;
    body: string;
  } | null>(null);
  const [busy, setBusy] = useState(false);

  const code = useMemo(
    () => snippet(mode, lang, checkUrl, service.slug, own[0]?.name ?? 'TYPE'),
    [mode, lang, checkUrl, service.slug, own],
  );

  const run = async () => {
    setBusy(true);
    setAnswer(null);
    try {
      const path =
        mode === 'permission' ? 'permission' : mode === 'check' ? 'check' : 'access/v1/evaluation';
      const body =
        mode === 'permission'
          ? undefined
          : mode === 'check'
            ? JSON.stringify({
                checks: [{ type: own[0]?.name ?? 'TYPE', action: 'read' }],
                explain: true,
              })
            : JSON.stringify({
                subject: { type: 'user', id: 'me' },
                resource: { type: own[0]?.name ?? 'TYPE' },
                action: { name: 'read' },
              });
      const res = await fetch(`/api/check/service/${encodeURIComponent(service.slug)}/${path}`, {
        method: mode === 'permission' ? 'GET' : 'POST',
        headers: {
          'x-test-token': token.trim(),
          ...(body ? { 'content-type': 'application/json' } : {}),
          'accept-language': document.documentElement.lang,
        },
        body,
      });
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
          value={mode}
          onChange={(v) => setMode(v as Mode)}
          options={[
            { label: t('integration.mode.permission'), value: 'permission' },
            { label: t('integration.mode.check'), value: 'check' },
            { label: 'AuthZEN', value: 'authzen' },
          ]}
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
        {t(`integration.about.${mode}`)}
      </Typography.Text>

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
          <Button type="primary" disabled={!token.trim()} loading={busy} onClick={() => void run()}>
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
          dataSource={['200', '401', '403', '404', '424'].map((code) => ({ code }))}
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
