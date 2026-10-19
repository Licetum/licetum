'use client';

import {
  Alert,
  AutoComplete,
  Button,
  Collapse,
  Flex,
  Input,
  Segmented,
  Select,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import { Plus, X } from 'lucide-react';
import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type {
  CheckResult,
  NotSyncTypePermission,
  RuleRef,
  SimulatePermission200,
  SyncTypePermission,
} from '@/api/admin/model';
import { useCreatePolicyScenario } from '@/api/admin/policy/policy';
import { useListResourcesOfType } from '@/api/admin/resource/resource';
import { useSimulateEvaluations, useSimulatePermission } from '@/api/admin/simulation/simulation';
import { DecisionTag, EmptyBlock, errorDetail, PageHeader } from '@/components/kit';
import type { Rule } from '@/domain/rule';
import {
  checkResultOf,
  evaluationError,
  evaluationsRequest,
  propertiesOf,
  type Properties,
} from '@/lib/authzen';

import { useAllTypes, useGroupNames, useRules, useServices } from '../data/queries';
import { levelsOf } from '../grant/grant-model';
import { WhoHasAccess } from '../services/who-has-access';

type Pair = { id: number; key: string; value: string };
type Question = {
  id: number;
  type?: string;
  resource: string;
  resourceName?: string;
  action: string;
};

const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/g;

let seq = 0;
const pair = (key = '', value = ''): Pair => ({ id: ++seq, key, value });
const question = (init: Partial<Question> = {}): Question => ({
  id: ++seq,
  resource: '',
  action: 'read',
  ...init,
});

export function CheckWorkspace() {
  const { t } = useTranslation();
  const params = useSearchParams();
  const [api, holder] = message.useMessage();

  const services = useServices();
  const types = useAllTypes();
  const rules = useRules();

  const [mode, setMode] = useState<'one' | 'list'>(params.get('mode') === 'list' ? 'list' : 'one');
  const [serviceSlug, setServiceSlug] = useState(params.get('service') ?? '');
  const [subject, setSubject] = useState(params.get('subject') ?? '');
  const [claims, setClaims] = useState<Pair[]>([]);
  const [context, setContext] = useState<Pair[]>([]);
  const [questions, setQuestions] = useState<Question[]>([
    question({
      type: params.get('type') ?? undefined,
      resource: params.get('resource') ?? '',
      action: params.get('action') ?? 'read',
    }),
  ]);
  const [listType, setListType] = useState<string | undefined>(params.get('type') ?? undefined);

  const check = useSimulateEvaluations();
  const createScenario = useCreatePolicyScenario();
  const permission = useSimulatePermission();
  const [lastRun, setLastRun] = useState<{ questions: Question[]; subject: string } | null>(null);

  const service = (services.data ?? []).find((s) => s.slug === serviceSlug || s.id === serviceSlug);
  useEffect(() => {
    if (!serviceSlug && services.data?.length) {
      const first = services.data.find((s) => s.slug !== 'licetum') ?? services.data[0];
      if (first) setServiceSlug(first.slug);
    }
  }, [services.data, serviceSlug]);

  const serviceTypes = useMemo(
    () => (types.data ?? []).filter((x) => x.service_id === service?.id),
    [types.data, service],
  );
  const rulesById = useMemo(() => new Map((rules.data ?? []).map((r) => [r.id, r])), [rules.data]);

  const ready = !!service && subject.trim() !== '';
  // The claims of the person and the attributes of the request, as the simulation of the PDP takes them: the person's
  // username is added by the PDP when the claims do not carry it.
  const properties: Properties = propertiesOf(claims);
  const attributes: Properties = propertiesOf(context);

  const run = async () => {
    if (!service) return;
    check.reset();
    permission.reset();
    if (mode === 'one') {
      const asked = questions.filter((q) => q.type);
      setLastRun({ questions: asked, subject: subject.trim() });
      await check.mutateAsync({
        serviceId: service.id,
        data: evaluationsRequest(
          { person: subject.trim(), properties, context: attributes },
          asked.map((q) => ({ type: q.type!, resource: q.resource, action: q.action })),
        ),
      });
    } else {
      await permission.mutateAsync({
        serviceId: service.id,
        data: {
          subject: subject.trim(),
          claims: { preferred_username: subject.trim(), ...properties },
          context: attributes,
          type: listType,
          limit: listType ? 200 : undefined,
        },
      });
    }
  };

  const shareLink = async () => {
    const url = new URL(window.location.href);
    url.search = '';
    url.searchParams.set('mode', mode);
    if (service) url.searchParams.set('service', service.slug);
    url.searchParams.set('subject', subject.trim());
    const first = questions[0];
    if (first?.type) url.searchParams.set('type', first.type);
    if (first?.resource) url.searchParams.set('resource', first.resource);
    if (first?.action) url.searchParams.set('action', first.action);
    await navigator.clipboard?.writeText(url.toString());
    void api.success(t('check.linkCopied'), 2);
  };

  const saveAsTest = async (q: Question, allowed: boolean) => {
    if (!service || !q.type) return;
    try {
      await createScenario.mutateAsync({
        serviceId: service.id,
        data: {
          name: `${subject.trim()} · ${q.type}${q.resource ? ` ${q.resource}` : ''} · ${q.action}`,
          subject: subject.trim(),
          claims: { preferred_username: subject.trim(), ...properties },
          context: attributes,
          checks: [
            {
              type: q.type,
              resource_id: q.resource.trim() || undefined,
              action: q.action,
              expect: allowed ? 'allow' : 'deny',
            },
          ],
        },
      });
      void api.success(t('check.savedTest'), 2);
    } catch (e) {
      void api.error(
        (e as { status?: number }).status === 409
          ? t('check.testExists')
          : (errorDetail(e) ?? t('check.saveTestFailed')),
      );
    }
  };

  const error = check.error ?? permission.error;

  // The first question that names a type: who has access to what it asks about.
  const whoQuestion = questions.find((q) => q.type);
  const whoType = whoQuestion ? serviceTypes.find((x) => x.name === whoQuestion.type) : undefined;

  return (
    <div className="page">
      {holder}
      <PageHeader
        title={t('check.title')}
        extra={
          <Segmented
            aria-label={t('check.modeLabel')}
            value={mode}
            onChange={(v) => setMode(v as 'one' | 'list')}
            options={[
              { label: t('check.mode.one'), value: 'one' },
              { label: t('check.mode.list'), value: 'list' },
            ]}
          />
        }
      />

      <div
        style={{
          background: 'var(--licetum-surface)',
          border: '1px solid var(--licetum-border)',
          borderRadius: 8,
          padding: 16,
          marginBottom: 16,
        }}
      >
        <Flex gap={12} wrap align="flex-end">
          <Field label={t('check.service')}>
            <Select
              aria-label={t('check.service')}
              style={{ width: 220 }}
              value={service?.slug}
              onChange={(v) => {
                setServiceSlug(v);
                setQuestions([question()]);
                setListType(undefined);
              }}
              loading={services.isPending}
              options={(services.data ?? []).map((s) => ({ value: s.slug, label: s.name }))}
            />
          </Field>
          <Field label={t('check.who')}>
            <Input
              aria-label={t('check.who')}
              placeholder="alice"
              style={{ width: 200 }}
              className="mono"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              onPressEnter={() => ready && void run()}
            />
          </Field>
        </Flex>

        <PairEditor
          label={t('check.attrs')}
          hint={t('check.attrsHint')}
          pairs={claims}
          onChange={setClaims}
          keyPlaceholder="department"
          valuePlaceholder="sales"
          addLabel={t('check.addAttr')}
        />

        {mode === 'one' ? (
          <Flex vertical gap={12} style={{ marginTop: 16 }}>
            {questions.map((q, index) => (
              <QuestionRow
                key={q.id}
                q={q}
                types={serviceTypes}
                canRemove={questions.length > 1}
                onChange={(next) =>
                  setQuestions((list) => list.map((x) => (x.id === q.id ? { ...x, ...next } : x)))
                }
                onRemove={() => setQuestions((list) => list.filter((x) => x.id !== q.id))}
                index={index}
              />
            ))}
            {service && whoType && (
              <Collapse
                ghost
                size="small"
                destroyOnHidden
                items={[
                  {
                    key: 'who',
                    label: t('check.whoSees'),
                    children: (
                      <WhoHasAccess
                        serviceId={service.id}
                        typeId={whoType.id}
                        resourceId={whoQuestion?.resource.trim() || undefined}
                      />
                    ),
                  },
                ]}
              />
            )}
            <Flex gap={8} wrap>
              <Button
                icon={<Plus size={14} aria-hidden />}
                disabled={questions.length >= 20}
                onClick={() => setQuestions((l) => [...l, question()])}
              >
                {t('check.addQuestion')}
              </Button>
              <span style={{ flex: 1 }} />
              <Button
                type="primary"
                disabled={!ready || !questions.some((q) => q.type)}
                loading={check.isPending}
                onClick={() => void run()}
              >
                {t('check.run')}
              </Button>
            </Flex>
          </Flex>
        ) : (
          <Flex gap={12} wrap align="flex-end" style={{ marginTop: 16 }}>
            <Field label={t('check.type')}>
              <Select
                allowClear
                aria-label={t('check.type')}
                style={{ width: 240 }}
                placeholder={t('check.allTypes')}
                value={listType}
                onChange={setListType}
                options={serviceTypes.map((x) => ({ value: x.name, label: x.name }))}
              />
            </Field>
            <span style={{ flex: 1 }} />
            <Button
              type="primary"
              disabled={!ready}
              loading={permission.isPending}
              onClick={() => void run()}
            >
              {t('check.show')}
            </Button>
          </Flex>
        )}

        <Collapse
          ghost
          size="small"
          style={{ marginTop: 8 }}
          items={[
            {
              key: 'context',
              label: t('check.context'),
              children: (
                <PairEditor
                  label={t('check.context')}
                  hint={t('check.contextHint')}
                  pairs={context}
                  onChange={setContext}
                  keyPlaceholder="ip"
                  valuePlaceholder="10.0.0.1"
                  addLabel={t('check.addContext')}
                />
              ),
            },
          ]}
        />
      </div>

      {error != null && (
        <Alert
          type="error"
          showIcon
          title={errorDetail(error) ?? t('check.failed')}
          style={{ marginBottom: 16 }}
        />
      )}

      {mode === 'one' && check.data && lastRun && (
        <Flex vertical gap={12}>
          {lastRun.questions.map((q, i) => {
            const answer = check.data.status === 200 ? check.data.data.evaluations?.[i] : undefined;
            if (!answer) return null;
            const problem = evaluationError(answer);
            return problem ? (
              <Alert key={q.id} type="error" showIcon title={problem} />
            ) : (
              <Verdict
                key={q.id}
                q={q}
                subject={lastRun.subject}
                serviceName={service?.name ?? ''}
                result={checkResultOf(answer)}
                resolve={(ref) => rulesById.get(ref.rule_id)}
                onSave={() => saveAsTest(q, answer.decision)}
                onShare={() => void shareLink()}
                resourceName={q.resourceName ?? q.resource}
              />
            );
          })}
        </Flex>
      )}

      {mode === 'list' && permission.data && (
        <PermissionList
          data={permission.data.data as SimulatePermission200}
          subject={subject.trim()}
          serviceSlug={service?.slug ?? ''}
        />
      )}

      {!check.data && !permission.data && !error && (
        <EmptyBlock title={t('check.start.title')} hint={t('check.start.hint')} />
      )}
    </div>
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

function PairEditor(props: {
  label: string;
  hint: string;
  pairs: Pair[];
  onChange: (p: Pair[]) => void;
  keyPlaceholder: string;
  valuePlaceholder: string;
  addLabel: string;
}) {
  const { t } = useTranslation();
  return (
    <Flex vertical gap={8} style={{ marginTop: 12 }}>
      {props.pairs.length > 0 && (
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {props.hint}
        </Typography.Text>
      )}
      {props.pairs.map((p) => (
        <Flex key={p.id} gap={8} align="center">
          <Input
            aria-label={`${props.label}: ${t('cond.attr')}`}
            value={p.key}
            placeholder={props.keyPlaceholder}
            className="mono"
            style={{ width: 180 }}
            onChange={(e) =>
              props.onChange(
                props.pairs.map((x) => (x.id === p.id ? { ...x, key: e.target.value } : x)),
              )
            }
          />
          <span aria-hidden>=</span>
          <Input
            aria-label={`${props.label}: ${t('cond.value')}`}
            value={p.value}
            placeholder={props.valuePlaceholder}
            style={{ width: 240 }}
            onChange={(e) =>
              props.onChange(
                props.pairs.map((x) => (x.id === p.id ? { ...x, value: e.target.value } : x)),
              )
            }
          />
          <Button
            type="text"
            aria-label={t('cond.remove')}
            icon={<X size={14} aria-hidden />}
            onClick={() => props.onChange(props.pairs.filter((x) => x.id !== p.id))}
          />
        </Flex>
      ))}
      <div>
        <Button
          size="small"
          icon={<Plus size={14} aria-hidden />}
          onClick={() => props.onChange([...props.pairs, pair()])}
        >
          {props.addLabel}
        </Button>
      </div>
    </Flex>
  );
}

function QuestionRow(props: {
  q: Question;
  index: number;
  types: ReturnType<typeof useAllTypes>['data'];
  canRemove: boolean;
  onChange: (next: Partial<Question>) => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  const type = (props.types ?? []).find((x) => x.name === props.q.type);
  const resources = useListResourcesOfType(type?.id ?? '', undefined, {
    query: { enabled: !!type, select: (r) => (r.status === 200 ? (r.data ?? []) : []) },
  });
  const levels = levelsOf(type);

  return (
    <Flex gap={12} wrap align="flex-end">
      <Field label={t('check.type')}>
        <Select
          aria-label={t('check.type')}
          style={{ width: 200 }}
          placeholder={t('check.typePlaceholder')}
          value={props.q.type}
          onChange={(v) => props.onChange({ type: v, resource: '' })}
          options={(props.types ?? []).map((x) => ({ value: x.name, label: x.name }))}
        />
      </Field>
      <Field label={t('check.resource')}>
        <AutoComplete
          aria-label={t('check.resource')}
          style={{ width: 320 }}
          placeholder={t('check.resourcePlaceholder')}
          value={props.q.resource}
          onChange={(v) => props.onChange({ resource: v, resourceName: undefined })}
          onSelect={(v: string) =>
            props.onChange({
              resource: v,
              resourceName: (resources.data ?? []).find((r) => r.id === v)?.name,
            })
          }
          options={(resources.data ?? [])
            .slice(0, 500)
            .map((r) => ({ value: r.id, label: `${r.name} (${r.id})` }))}
          filterOption={(input, option) =>
            String(option?.label ?? '')
              .toLowerCase()
              .includes(input.toLowerCase())
          }
          disabled={!type}
          allowClear
        />
      </Field>
      <Field label={t('check.level')}>
        <AutoComplete
          aria-label={t('check.level')}
          style={{ width: 130 }}
          value={props.q.action}
          onChange={(v) => props.onChange({ action: v })}
          options={levels.map((l) => ({ value: l }))}
        />
      </Field>
      {props.canRemove && (
        <Button
          type="text"
          aria-label={t('cond.remove')}
          icon={<X size={14} aria-hidden />}
          onClick={props.onRemove}
        />
      )}
    </Flex>
  );
}

function RuleLine({
  ref_,
  resolve,
}: {
  ref_: RuleRef;
  resolve: (ref: RuleRef) => Rule | undefined;
}) {
  const { t } = useTranslation();
  const groups = useGroupNames();
  const rule = resolve(ref_);
  const name = rule?.name ?? t('check.ruleFallback', { id: ref_.rule_id.slice(0, 8) });
  return (
    <Flex gap={8} wrap align="center">
      <Link href={`/access/${ref_.rule_id}`}>{name}</Link>
      <span className="mono" style={{ color: 'var(--licetum-muted)' }}>
        {ref_.access.join(', ')}
      </span>
      <span style={{ color: 'var(--licetum-muted)', fontSize: 13 }}>
        {ref_.anchor.resource_id
          ? t('check.on', { target: `${ref_.anchor.type} ${ref_.anchor.resource_id}` })
          : t('check.onType', { type: ref_.anchor.type })}{' '}
        · {ref_.applies_to.replace(UUID, (id) => `«${groups.name(id) ?? id.slice(0, 8)}»`)}
      </span>
    </Flex>
  );
}

function Verdict(props: {
  q: Question;
  subject: string;
  serviceName: string;
  resourceName: string;
  result: CheckResult;
  resolve: (ref: RuleRef) => Rule | undefined;
  onSave: () => void;
  onShare: () => void;
}) {
  const { t } = useTranslation();
  const ex = props.result.explanation;
  const decision = ex?.decision ?? (props.result.allowed ? 'allowed' : 'denied');
  const target = props.q.resource
    ? `«${props.resourceName}»`
    : t('check.wholeType', { type: props.q.type });
  const tone = decision === 'allowed' ? 'allow' : decision === 'denied' ? 'deny' : 'neutral';
  const bg =
    tone === 'allow'
      ? 'var(--licetum-allow-bg)'
      : tone === 'deny'
        ? 'var(--licetum-deny-bg)'
        : 'var(--licetum-inherit-bg)';
  const fg =
    tone === 'allow'
      ? 'var(--licetum-allow-fg)'
      : tone === 'deny'
        ? 'var(--licetum-deny-fg)'
        : 'var(--licetum-inherit-fg)';
  const unresolved = ex?.denials.some((d) => d.unresolved_parent);
  const missing = ex?.denials.filter((d) => d.missing_attribute) ?? [];

  return (
    <div role="status" style={{ background: bg, borderRadius: 8, padding: 16 }}>
      <Typography.Title level={4} style={{ margin: 0, color: fg }}>
        {t(`check.verdict.${decision}`)}
      </Typography.Title>
      <div style={{ fontSize: 16, margin: '6px 0 12px', color: 'var(--licetum-text)' }}>
        {t(props.result.allowed ? 'check.sentence.allowed' : 'check.sentence.denied', {
          subject: props.subject,
          action: props.q.action,
          target,
          service: props.serviceName,
        })}
      </div>
      {ex && (
        <dl
          style={{
            display: 'grid',
            gridTemplateColumns: '160px 1fr',
            gap: '8px 12px',
            margin: 0,
            color: 'var(--licetum-text)',
          }}
        >
          <dt style={{ color: 'var(--licetum-muted)' }}>{t('check.granted')}</dt>
          <dd style={{ margin: 0 }}>
            {ex.granted_by.length === 0 ? (
              <span style={{ color: 'var(--licetum-muted)' }}>{t('check.grantedNone')}</span>
            ) : (
              <Flex vertical gap={4}>
                {ex.granted_by.map((r) => (
                  <RuleLine key={r.rule_id} ref_={r} resolve={props.resolve} />
                ))}
              </Flex>
            )}
          </dd>
          <dt style={{ color: 'var(--licetum-muted)' }}>{t('check.denied')}</dt>
          <dd style={{ margin: 0 }}>
            {ex.denials.length === 0 ? (
              <span style={{ color: 'var(--licetum-muted)' }}>{t('check.deniedNone')}</span>
            ) : (
              <Flex vertical gap={6}>
                {ex.denials.map((d) => (
                  <div key={d.rule.rule_id}>
                    <RuleLine ref_={d.rule} resolve={props.resolve} />
                    {d.lifted_by.length > 0 ? (
                      <div style={{ color: 'var(--licetum-muted)', fontSize: 13 }}>
                        {t('check.lifted')}:{' '}
                        {d.lifted_by.map((l) => (
                          <Link
                            key={l.rule_id}
                            href={`/access/${l.rule_id}`}
                            style={{ marginRight: 8 }}
                          >
                            {props.resolve(l)?.name ?? l.rule_id.slice(0, 8)}
                          </Link>
                        ))}
                      </div>
                    ) : (
                      <DecisionTag decision="deny">{t('check.standing')}</DecisionTag>
                    )}
                  </div>
                ))}
              </Flex>
            )}
          </dd>
        </dl>
      )}
      {unresolved && (
        <Alert style={{ marginTop: 12 }} type="warning" showIcon title={t('check.unresolved')} />
      )}
      {missing.map((d) => (
        <Alert
          key={d.rule.rule_id}
          style={{ marginTop: 12 }}
          type="warning"
          showIcon
          title={t('check.missingAttr')}
        />
      ))}
      {(decision === 'no_access' || decision === 'denied') && (
        <Typography.Paragraph style={{ margin: '12px 0 0', color: 'var(--licetum-text)' }}>
          {decision === 'no_access' ? t('check.noAccessHint') : null}
        </Typography.Paragraph>
      )}
      <Flex gap={8} wrap style={{ marginTop: 12 }}>
        <Button size="small" onClick={props.onSave}>
          {t('check.saveTest')}
        </Button>
        <Button size="small" onClick={props.onShare}>
          {t('check.share')}
        </Button>
        {decision === 'no_access' && (
          <Button size="small" type="primary" href="/access?grant=1">
            {t('check.grantAccess')}
          </Button>
        )}
      </Flex>
    </div>
  );
}

type Entry = [string, SyncTypePermission | NotSyncTypePermission];

function PermissionList({
  data,
  subject,
  serviceSlug,
}: {
  data: SimulatePermission200;
  subject: string;
  serviceSlug: string;
}) {
  const { t } = useTranslation();
  const entries = Object.entries(data) as Entry[];
  if (entries.length === 0) {
    return <EmptyBlock title={t('check.list.none', { subject })} hint={t('check.list.noneHint')} />;
  }
  return (
    <Table<Entry>
      rowKey={([name]) => name}
      size="middle"
      pagination={false}
      dataSource={entries}
      expandable={{
        rowExpandable: ([, v]) => v.type === 'sync',
        expandedRowRender: ([name, v]) =>
          v.type === 'sync' ? (
            <Table
              size="small"
              rowKey="id"
              pagination={{ pageSize: 20, showSizeChanger: false }}
              dataSource={v.resources}
              columns={[
                {
                  title: t('check.resource'),
                  key: 'name',
                  render: (_, r) => (
                    <span>
                      {r.name}{' '}
                      <span className="mono" style={{ color: 'var(--licetum-muted)' }}>
                        {r.id}
                      </span>
                    </span>
                  ),
                },
                {
                  title: t('check.level'),
                  key: 'access',
                  width: 200,
                  render: (_, r) =>
                    (r.access ?? []).map((a) => (
                      <Tag key={a} className="mono">
                        {a}
                      </Tag>
                    )),
                },
                {
                  title: '',
                  key: 'why',
                  width: 110,
                  render: (_, r) => (
                    <Link
                      href={`/check?mode=one&service=${serviceSlug}&subject=${encodeURIComponent(subject)}&type=${encodeURIComponent(name)}&resource=${encodeURIComponent(r.id)}`}
                    >
                      {t('check.why')} ›
                    </Link>
                  ),
                },
              ]}
            />
          ) : null,
      }}
      columns={[
        { title: t('check.type'), key: 'type', render: (_, [name]) => name },
        {
          title: t('check.list.resources'),
          key: 'count',
          width: 220,
          render: (_, [, v]) =>
            v.type === 'sync'
              ? `${v.resources.length}${v.next_after ? '+' : ''}`
              : t('check.list.wholeType'),
        },
        {
          title: t('check.level'),
          key: 'levels',
          render: (_, [, v]) =>
            v.type === 'not_sync'
              ? v.access.map((a) => (
                  <Tag key={a} className="mono">
                    {a}
                  </Tag>
                ))
              : null,
        },
      ]}
    />
  );
}
