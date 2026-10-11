'use client';

import {
  Alert,
  Button,
  Collapse,
  Flex,
  Input,
  Modal,
  Select,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
  Upload,
  message,
} from 'antd';
import { Plus, X } from 'lucide-react';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type {
  PolicyDocument,
  PolicyImportPlan,
  PolicyTestCase,
  PolicyTestReport,
} from '@/api/admin/model';
import {
  exportPolicy,
  useCreatePolicyScenario,
  useDeletePolicyScenario,
  useImportPolicy,
  useLintPolicy,
  useListPolicyScenarios,
  useRunPolicyTests,
} from '@/api/admin/policy/policy';
import { DecisionTag, EmptyBlock, errorDetail, ErrorBlock, LoadingBlock } from '@/components/kit';
import { useAccess } from '@/lib/access';
import { clearLocalPolicyTests, loadLocalPolicyTests } from '@/lib/policy-tests-store';
import { policyDocumentShapeSchema, policyTestFileSchema } from '@/lib/schemas';
import { useQueryState } from '@/lib/use-query-state';

import { useAllTypes, useRefreshAll, useRules } from '../data/queries';
import { useService } from './service-frame';

export function PolicyTab() {
  const { t } = useTranslation();
  const lint = useService();
  const { service } = lint;
  const [tab, setTab] = useQueryState('tab', 'lint');
  const findings = useLintPolicy(service.id, {
    query: { select: (r) => (r.status === 200 ? (r.data?.findings ?? []) : []) },
  });
  const bad = (findings.data ?? []).filter((f) => f.severity !== 'info').length;

  return (
    <Tabs
      activeKey={tab}
      onChange={setTab}
      items={[
        {
          key: 'lint',
          label: t('policy.tab.lint', { count: findings.data?.length ?? 0 }),
          children: <LintPanel />,
        },
        { key: 'tests', label: t('policy.tab.tests'), children: <TestsPanel /> },
        { key: 'document', label: t('policy.tab.document'), children: <DocumentPanel /> },
      ]}
      tabBarExtraContent={
        bad > 0 ? <Tag color="warning">{t('policy.attention', { count: bad })}</Tag> : null
      }
    />
  );
}

function LintPanel() {
  const { t } = useTranslation();
  const { service } = useService();
  const rules = useRules();
  const lint = useLintPolicy(service.id, {
    query: { select: (r) => (r.status === 200 ? (r.data?.findings ?? []) : []) },
  });
  const items = lint.data ?? [];
  const ruleName = (id?: string) => rules.data?.find((r) => r.id === id)?.name;

  if (lint.isPending) return <LoadingBlock rows={3} />;
  if (lint.isError) return <ErrorBlock error={lint.error} onRetry={() => void lint.refetch()} />;
  return (
    <Flex vertical gap={12}>
      <Flex justify="space-between" align="center" wrap>
        <Typography.Text type="secondary">{t('policy.lint.hint')}</Typography.Text>
        <Button onClick={() => void lint.refetch()}>{t('policy.lint.recheck')}</Button>
      </Flex>
      {items.length === 0 ? (
        <Alert type="success" showIcon title={t('policy.lint.none')} />
      ) : (
        <Table
          rowKey={(f) => `${f.code}-${f.rule_id ?? f.role_id ?? f.group_id ?? f.message}`}
          size="middle"
          pagination={false}
          dataSource={items}
          columns={[
            {
              title: t('policy.lint.severity'),
              key: 'sev',
              width: 140,
              render: (_, f) => (
                <DecisionTag
                  decision={
                    f.severity === 'error'
                      ? 'deny'
                      : f.severity === 'warning'
                        ? 'except'
                        : 'inherit'
                  }
                >
                  {t(`policy.severity.${f.severity}`)}
                </DecisionTag>
              ),
            },
            { title: t('policy.lint.finding'), key: 'msg', render: (_, f) => f.message },
            {
              title: '',
              key: 'open',
              width: 200,
              render: (_, f) =>
                f.rule_id ? (
                  <Link href={`/access/${f.rule_id}`}>
                    {ruleName(f.rule_id) ?? t('policy.lint.openRule')}
                  </Link>
                ) : f.role_id ? (
                  <Link href={`/roles/${f.role_id}`}>{t('policy.lint.openRole')}</Link>
                ) : f.group_id ? (
                  <Link href={`/people/groups/${f.group_id}`}>{t('policy.lint.openGroup')}</Link>
                ) : null,
            },
          ]}
        />
      )}
    </Flex>
  );
}

function TestsPanel() {
  const { t } = useTranslation();
  const { service } = useService();
  const types = useAllTypes();
  const run = useRunPolicyTests();
  const scenarios = useListPolicyScenarios(service.id, {
    query: { select: (r) => (r.status === 200 ? (r.data ?? []) : []) },
  });
  const createScenario = useCreatePolicyScenario();
  const deleteScenario = useDeletePolicyScenario();
  const canWrite = useAccess().any('service', 'write');
  const stored = scenarios.data ?? [];
  const cases: PolicyTestCase[] = stored.map((s) => s.case);
  // What this browser still holds from before the server kept the scenarios.
  const [local, setLocal] = useState<PolicyTestCase[]>([]);
  useEffect(() => setLocal(loadLocalPolicyTests(service.id)), [service.id]);
  const [report, setReport] = useState<PolicyTestReport | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [editing, setEditing] = useState<boolean>(false);
  const [api, holder] = message.useMessage();

  /** Stores scenarios on the server; the ones whose name the service already has are skipped. Returns how many were added. */
  const store = async (list: PolicyTestCase[]): Promise<number> => {
    let added = 0;
    for (const data of list) {
      try {
        await createScenario.mutateAsync({ serviceId: service.id, data });
        added++;
      } catch (e) {
        if ((e as { status?: number }).status !== 409) throw e;
      }
    }
    await scenarios.refetch();
    return added;
  };

  const go = async () => {
    setError(null);
    try {
      const res = await run.mutateAsync({ serviceId: service.id, data: { cases } });
      setReport(res.data as PolicyTestReport);
    } catch (e) {
      setError(e);
    }
  };

  const ownTypes = (types.data ?? []).filter((x) => x.service_id === service.id);

  return (
    <Flex vertical gap={12}>
      {holder}
      <Typography.Text type="secondary">{t('policy.tests.storage')}</Typography.Text>
      {local.length > 0 && canWrite && (
        <Alert
          type="info"
          showIcon
          title={t('policy.tests.local', { count: local.length })}
          action={
            <Button
              size="small"
              onClick={() =>
                void store(local).then(() => {
                  clearLocalPolicyTests(service.id);
                  setLocal([]);
                })
              }
            >
              {t('policy.tests.moveLocal')}
            </Button>
          }
        />
      )}
      <Flex gap={8} wrap>
        <Button icon={<Plus size={14} aria-hidden />} onClick={() => setEditing(true)}>
          {t('policy.tests.add')}
        </Button>
        <Button
          type="primary"
          disabled={cases.length === 0}
          loading={run.isPending}
          onClick={() => void go()}
        >
          {t('policy.tests.run')}
        </Button>
        <Button
          disabled={cases.length === 0}
          onClick={() => {
            const blob = new Blob([JSON.stringify({ cases }, null, 2)], {
              type: 'application/json',
            });
            const a = document.createElement('a');
            a.href = URL.createObjectURL(blob);
            a.download = `policy-tests.${service.slug}.json`;
            a.click();
            URL.revokeObjectURL(a.href);
          }}
        >
          {t('policy.tests.save')}
        </Button>
        <Upload
          accept="application/json"
          showUploadList={false}
          beforeUpload={async (file) => {
            try {
              const parsed = policyTestFileSchema.parse(JSON.parse(await file.text()));
              void api.success(t('policy.tests.added', { count: await store(parsed.cases) }));
            } catch {
              void api.error(t('policy.tests.badFile'));
            }
            return false;
          }}
        >
          <Button>{t('policy.tests.load')}</Button>
        </Upload>
      </Flex>

      {error != null && (
        <Alert type="error" showIcon title={errorDetail(error) ?? t('check.failed')} />
      )}

      {cases.length === 0 ? (
        <EmptyBlock title={t('policy.tests.empty.title')} hint={t('policy.tests.empty.hint')} />
      ) : (
        <Table<PolicyTestCase>
          rowKey={(c) => c.name}
          size="middle"
          pagination={false}
          dataSource={cases}
          columns={[
            {
              title: t('policy.tests.col.name'),
              key: 'name',
              render: (_, c) => {
                const result = report?.cases.find((r) => r.name === c.name);
                return (
                  <span>
                    {result && (
                      <DecisionTag decision={result.passed ? 'allow' : 'deny'}>
                        {result.passed ? t('policy.tests.passed') : t('policy.tests.failed')}
                      </DecisionTag>
                    )}{' '}
                    {c.name}
                  </span>
                );
              },
            },
            {
              title: t('check.who'),
              key: 'subject',
              width: 140,
              render: (_, c) => <span className="mono">{c.subject}</span>,
            },
            {
              title: t('policy.tests.col.checks'),
              key: 'checks',
              render: (_, c) => {
                const result = report?.cases.find((r) => r.name === c.name);
                return c.checks.map((k, i) => {
                  const r = result?.checks[i];
                  return (
                    <div key={i} style={{ fontSize: 13 }}>
                      <span className="mono">
                        {k.type}
                        {k.resource_id ? ` ${k.resource_id}` : ''} · {k.action}
                      </span>{' '}
                      → {t(`policy.expect.${k.expect}`)}
                      {r && !r.passed && (
                        <span style={{ color: 'var(--licetum-deny-fg)' }}>
                          {' '}
                          ({t('policy.tests.got', { decision: t(`check.verdict.${r.decision}`) })})
                        </span>
                      )}
                    </div>
                  );
                });
              },
            },
            {
              title: '',
              key: 'x',
              width: 60,
              render: (_, c) => (
                <Button
                  type="text"
                  aria-label={t('common.delete')}
                  icon={<X size={14} aria-hidden />}
                  disabled={!canWrite}
                  onClick={() => {
                    const id = stored.find((s) => s.case.name === c.name)?.id;
                    if (!id) return;
                    void deleteScenario
                      .mutateAsync({ serviceId: service.id, scenarioId: id })
                      .then(() => scenarios.refetch());
                  }}
                />
              ),
            },
          ]}
        />
      )}

      {report && (
        <Alert
          type={report.failed === 0 ? 'success' : 'error'}
          showIcon
          title={t('policy.tests.summary', {
            passed: report.passed,
            total: report.passed + report.failed,
            covered: report.coverage.covered,
            rules: report.coverage.rules,
          })}
        />
      )}

      <TestEditor
        open={editing}
        types={ownTypes.map((x) => x.name)}
        existing={cases.map((c) => c.name)}
        onClose={() => setEditing(false)}
        onSave={(c) => {
          void store([c]).then(() => setEditing(false));
        }}
      />
    </Flex>
  );
}

function TestEditor(props: {
  open: boolean;
  types: string[];
  existing: string[];
  onClose: () => void;
  onSave: (c: PolicyTestCase) => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState('');
  const [subject, setSubject] = useState('');
  const [claims, setClaims] = useState<{ k: string; v: string }[]>([]);
  const [checks, setChecks] = useState<
    { type?: string; resource: string; action: string; expect: 'allow' | 'deny' }[]
  >([{ resource: '', action: 'read', expect: 'allow' }]);

  const ready =
    name.trim() &&
    !props.existing.includes(name.trim()) &&
    subject.trim() &&
    checks.every((c) => c.type && c.action.trim());
  return (
    <Modal
      open={props.open}
      title={t('policy.tests.add')}
      onCancel={props.onClose}
      okText={t('common.save')}
      cancelText={t('common.cancel')}
      okButtonProps={{ disabled: !ready }}
      width={720}
      destroyOnHidden
      afterOpenChange={(open) => {
        if (open) {
          setName('');
          setSubject('');
          setClaims([]);
          setChecks([{ resource: '', action: 'read', expect: 'allow' }]);
        }
      }}
      onOk={() =>
        props.onSave({
          name: name.trim(),
          subject: subject.trim(),
          claims: {
            preferred_username: subject.trim(),
            ...Object.fromEntries(claims.filter((c) => c.k.trim()).map((c) => [c.k.trim(), c.v])),
          },
          checks: checks.map((c) => ({
            type: c.type!,
            resource_id: c.resource.trim() || undefined,
            action: c.action.trim(),
            expect: c.expect,
          })),
        })
      }
    >
      <Flex vertical gap={12}>
        <Input
          aria-label={t('policy.tests.col.name')}
          placeholder={t('policy.tests.namePlaceholder')}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <Input
          aria-label={t('check.who')}
          placeholder="alice"
          value={subject}
          onChange={(e) => setSubject(e.target.value)}
          className="mono"
          style={{ maxWidth: 240 }}
        />
        <Collapse
          ghost
          size="small"
          items={[
            {
              key: 'c',
              label: t('check.attrs'),
              children: (
                <Flex vertical gap={8}>
                  {claims.map((c, i) => (
                    <Flex key={i} gap={8}>
                      <Input
                        aria-label={t('cond.attr')}
                        className="mono"
                        style={{ width: 180 }}
                        value={c.k}
                        onChange={(e) =>
                          setClaims(
                            claims.map((x, j) => (j === i ? { ...x, k: e.target.value } : x)),
                          )
                        }
                      />
                      <Input
                        aria-label={t('cond.value')}
                        style={{ width: 240 }}
                        value={c.v}
                        onChange={(e) =>
                          setClaims(
                            claims.map((x, j) => (j === i ? { ...x, v: e.target.value } : x)),
                          )
                        }
                      />
                      <Button
                        type="text"
                        aria-label={t('cond.remove')}
                        icon={<X size={14} aria-hidden />}
                        onClick={() => setClaims(claims.filter((_, j) => j !== i))}
                      />
                    </Flex>
                  ))}
                  <div>
                    <Button size="small" onClick={() => setClaims([...claims, { k: '', v: '' }])}>
                      {t('check.addAttr')}
                    </Button>
                  </div>
                </Flex>
              ),
            },
          ]}
        />
        {checks.map((c, i) => (
          <Flex key={i} gap={8} wrap align="center">
            <Select
              aria-label={t('check.type')}
              style={{ width: 180 }}
              placeholder={t('check.typePlaceholder')}
              value={c.type}
              onChange={(type) => setChecks(checks.map((x, j) => (j === i ? { ...x, type } : x)))}
              options={props.types.map((x) => ({ value: x }))}
            />
            <Input
              aria-label={t('check.resource')}
              style={{ width: 180 }}
              placeholder={t('policy.tests.resourceEmpty')}
              value={c.resource}
              onChange={(e) =>
                setChecks(checks.map((x, j) => (j === i ? { ...x, resource: e.target.value } : x)))
              }
              className="mono"
            />
            <Input
              aria-label={t('check.level')}
              style={{ width: 110 }}
              value={c.action}
              onChange={(e) =>
                setChecks(checks.map((x, j) => (j === i ? { ...x, action: e.target.value } : x)))
              }
              className="mono"
            />
            <Select
              aria-label={t('policy.tests.expect')}
              style={{ width: 140 }}
              value={c.expect}
              onChange={(expect) =>
                setChecks(checks.map((x, j) => (j === i ? { ...x, expect } : x)))
              }
              options={[
                { value: 'allow', label: t('policy.expect.allow') },
                { value: 'deny', label: t('policy.expect.deny') },
              ]}
            />
            {checks.length > 1 && (
              <Button
                type="text"
                aria-label={t('cond.remove')}
                icon={<X size={14} aria-hidden />}
                onClick={() => setChecks(checks.filter((_, j) => j !== i))}
              />
            )}
          </Flex>
        ))}
        <div>
          <Button
            size="small"
            icon={<Plus size={14} aria-hidden />}
            onClick={() =>
              setChecks([...checks, { resource: '', action: 'read', expect: 'allow' }])
            }
          >
            {t('check.addQuestion')}
          </Button>
        </div>
      </Flex>
    </Modal>
  );
}

function DocumentPanel() {
  const { t } = useTranslation();
  const access = useAccess();
  const { service } = useService();
  const refresh = useRefreshAll();
  const importer = useImportPolicy();

  const [text, setText] = useState('');
  const [prune, setPrune] = useState(false);
  const [plan, setPlan] = useState<PolicyImportPlan | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [applied, setApplied] = useState(false);

  const download = async () => {
    const res = await exportPolicy(service.id);
    if (res.status !== 200) return;
    const blob = new Blob([JSON.stringify(res.data, null, 2)], { type: 'application/json' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `policy.${service.slug}.json`;
    a.click();
    URL.revokeObjectURL(a.href);
  };

  const parse = (): PolicyDocument | null => {
    let value: unknown;
    try {
      value = JSON.parse(text);
    } catch {
      setError({ data: { detail: t('policy.doc.badJson') } });
      return null;
    }
    // Only the envelope is checked here; the server validates everything else and reports what is wrong.
    if (!policyDocumentShapeSchema.safeParse(value).success) {
      setError({ data: { detail: t('policy.doc.badShape') } });
      return null;
    }
    return value as PolicyDocument;
  };

  const preview = async () => {
    setError(null);
    setApplied(false);
    const doc = parse();
    if (!doc) return;
    try {
      const res = await importer.mutateAsync({
        serviceId: service.id,
        data: doc,
        params: { dry_run: true, prune },
      });
      setPlan(res.data as PolicyImportPlan);
    } catch (e) {
      setPlan(null);
      setError(e);
    }
  };

  const apply = async () => {
    const doc = parse();
    if (!doc) return;
    Modal.confirm({
      title: t('policy.doc.applyTitle'),
      content: prune
        ? t('policy.doc.pruneWarn', { count: plan?.counts.deleted ?? 0 })
        : t('policy.doc.applyBody'),
      okText: t('policy.doc.apply'),
      okButtonProps: { danger: prune },
      cancelText: t('common.cancel'),
      onOk: async () => {
        try {
          await importer.mutateAsync({
            serviceId: service.id,
            data: doc,
            params: { dry_run: false, prune },
          });
          await refresh();
          setApplied(true);
          setPlan(null);
        } catch (e) {
          setError(e);
          throw e;
        }
      },
    });
  };

  const canWrite = access.whole('assignment_rule', 'write');
  return (
    <Flex vertical gap={12}>
      <Typography.Text type="secondary">{t('policy.doc.hint')}</Typography.Text>
      <Flex gap={8}>
        <Button onClick={() => void download()}>{t('policy.doc.export')}</Button>
      </Flex>
      <Typography.Title level={5} style={{ margin: '8px 0 0' }}>
        {t('policy.doc.import')}
      </Typography.Title>
      <Upload
        accept="application/json"
        showUploadList={false}
        beforeUpload={async (file) => {
          setText(await file.text());
          setPlan(null);
          setApplied(false);
          return false;
        }}
      >
        <Button>{t('policy.doc.choose')}</Button>
      </Upload>
      <Input.TextArea
        rows={8}
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          setPlan(null);
        }}
        placeholder={t('policy.doc.paste')}
        className="mono"
        aria-label={t('policy.doc.paste')}
      />
      <Flex gap={12} align="center" wrap>
        <Switch
          checked={prune}
          onChange={(v) => {
            setPrune(v);
            setPlan(null);
          }}
          aria-label={t('policy.doc.prune')}
        />
        <span>{t('policy.doc.prune')}</span>
        <span style={{ flex: 1 }} />
        <Button disabled={!text.trim()} loading={importer.isPending} onClick={() => void preview()}>
          {t('policy.doc.dry')}
        </Button>
      </Flex>
      {error != null && (
        <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
      )}
      {applied && <Alert type="success" showIcon title={t('policy.doc.applied')} />}
      {plan && (
        <div style={{ border: '1px solid var(--licetum-accent)', borderRadius: 8, padding: 16 }}>
          <Typography.Text strong>
            {t('policy.doc.plan', {
              created: plan.counts.created,
              updated: plan.counts.updated,
              deleted: plan.counts.deleted,
              unchanged: plan.counts.unchanged,
            })}
          </Typography.Text>
          <ul style={{ margin: '8px 0', paddingLeft: 20 }}>
            {plan.operations.slice(0, 50).map((op, i) => (
              <li key={i}>
                {t(`history.op.${op.op}`)} {t(`history.entity.${op.entity}`)}
              </li>
            ))}
          </ul>
          {plan.operations.length === 0 && (
            <Typography.Text type="secondary">{t('policy.doc.noChanges')}</Typography.Text>
          )}
          {canWrite && plan.operations.length > 0 && (
            <Button type="primary" danger={prune} onClick={() => void apply()}>
              {t('policy.doc.apply')}
            </Button>
          )}
        </div>
      )}
    </Flex>
  );
}
