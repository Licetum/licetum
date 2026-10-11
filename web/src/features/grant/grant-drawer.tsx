'use client';

import {
  Alert,
  Button,
  Checkbox,
  Collapse,
  DatePicker,
  Drawer,
  Flex,
  Input,
  Segmented,
  Select,
  Switch,
  Typography,
} from 'antd';
import dayjs from 'dayjs';
import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useCreateAssignmentRule } from '@/api/admin/assignment-rule/assignment-rule';
import { useRunBatch } from '@/api/admin/batch/batch';
import { errorDetail } from '@/components/kit';
import { describeConditions } from '@/domain/rule';
import { useAccess } from '@/lib/access';
import { useUndoNotice } from '@/lib/undo';

import { useGroups, useRefreshAll, useRoles } from '../data/queries';
import { CondEditor } from './cond-editor';
import { useGrant } from './grant-context';
import {
  buildInputs,
  emptyForm,
  problemOf,
  sentence,
  toPredicate,
  type GrantForm,
  type WhoMode,
} from './grant-model';
import { GrantPreviewPanel } from './grant-preview';
import { LevelsFields, WhereFields, useTargetData } from './target-fields';

const section = { borderTop: '1px solid var(--licetum-border)', paddingTop: 16 } as const;

function Label({ children }: { children: string }) {
  return (
    <Typography.Text
      type="secondary"
      style={{
        fontSize: 12,
        fontWeight: 600,
        letterSpacing: '.04em',
        textTransform: 'uppercase',
        display: 'block',
        marginBottom: 8,
      }}
    >
      {children}
    </Typography.Text>
  );
}

/**
 * "Give access": one panel that reads like a sentence — who, what, where, with what conditions. It opens over any screen
 * (the shell owns it) and creates one rule, or one rule per chosen resource.
 */
export function GrantDrawer() {
  const { t } = useTranslation();
  const grant = useGrant();
  const access = useAccess();
  const undoNotice = useUndoNotice();
  const refresh = useRefreshAll();

  const [form, setForm] = useState<GrantForm>(emptyForm);
  const [error, setError] = useState<unknown>(null);
  const [added, setAdded] = useState(0);
  const patch = (next: Partial<GrantForm>) => setForm((f) => ({ ...f, ...next }));

  // Every opening starts clean, with what the opener gave (a group, a person, a type, a resource).
  useEffect(() => {
    if (!grant.isOpen) return;
    const prefill = grant.prefill;
    const base = emptyForm();
    if (prefill?.who?.kind === 'group')
      Object.assign(base, { who: 'group', groupId: prefill.who.id });
    if (prefill?.who?.kind === 'person')
      Object.assign(base, { who: 'person', person: prefill.who.username });
    if (prefill?.roleId) Object.assign(base, { what: 'role', roleId: prefill.roleId });
    if (prefill?.serviceId) base.serviceId = prefill.serviceId;
    if (prefill?.typeId) base.typeId = prefill.typeId;
    if (prefill?.resourceId)
      Object.assign(base, { scope: 'resources', resourceIds: [prefill.resourceId] });
    setForm(base);
    setError(null);
    setAdded(0);
  }, [grant.isOpen, grant.prefill]);

  const enabled = grant.isOpen;
  const groups = useGroups(enabled);
  const roles = useRoles(enabled);
  const patchStable = useCallback(
    (next: Partial<GrantForm>) => setForm((f) => ({ ...f, ...next })),
    [],
  );
  const data = useTargetData(form, patchStable, enabled);
  const { services, type, hasSource } = data;

  const problem = problemOf(form, { hasSource });

  const create = useCreateAssignmentRule();
  const batch = useRunBatch();
  const busy = create.isPending || batch.isPending;

  const groupName = (id?: string) => groups.data?.find((g) => g.id === id)?.name;
  const roleName = roles.data?.find((r) => r.id === form.roleId)?.name;
  const resourceName = (id: string) => data.resources.data?.find((r) => r.id === id)?.name ?? id;

  const text = sentence(
    form,
    {
      groupName: groupName(form.groupId),
      roleName,
      typeName: type?.name,
      serviceName: services.find((s) => s.value === form.serviceId)?.label,
      resourceNames: form.resourceIds.map(resourceName),
      whoConds: describeConditions(form.whoConds.filter((c) => c.attr && c.value).map(toPredicate)),
    },
    t,
  );

  // The rules the form would create: the preview asks what they would do, and the count of the footer comes from them.
  const previewInputs = problem ? [] : buildInputs(form, form.typeId);

  const submit = async (more: boolean) => {
    setError(null);
    const inputs = buildInputs(form, form.typeId);
    try {
      let ids: string[];
      if (inputs.length === 1 && inputs[0]) {
        const res = await create.mutateAsync({ data: inputs[0] });
        ids = [(res.data as { id: string }).id];
      } else {
        const operations = inputs.map((body) => ({
          op: 'create' as const,
          entity: 'assignment_rule' as const,
          id: crypto.randomUUID(),
          body,
        }));
        await batch.mutateAsync({ data: { operations } });
        ids = operations.map((o) => o.id);
      }
      await refresh();
      undoNotice({
        message: t('grant.created', { count: ids.length }),
        description: text,
        undo: async () => {
          await batch.mutateAsync({
            data: {
              operations: ids.map((id) => ({
                op: 'delete' as const,
                entity: 'assignment_rule' as const,
                id,
              })),
            },
          });
          await refresh();
        },
      });
      if (more) {
        setAdded((n) => n + ids.length);
        setForm((f) => ({
          ...emptyForm(),
          what: f.what,
          serviceId: f.serviceId,
          typeId: f.typeId,
          levels: f.levels,
          accessGroups: f.accessGroups,
          scope: 'all' as const,
          roleId: f.roleId,
        }));
      } else {
        grant.close();
      }
    } catch (e) {
      setError(e);
    }
  };

  const whoOptions: { label: string; value: WhoMode }[] = [
    { label: t('grant.who.group'), value: 'group' },
    { label: t('grant.who.person'), value: 'person' },
    { label: t('grant.who.condition'), value: 'condition' },
    { label: t('grant.who.everyone'), value: 'everyone' },
  ];

  const canWrite = access.whole('assignment_rule', 'write');

  return (
    <Drawer
      open={grant.isOpen}
      onClose={grant.close}
      size={640}
      title={t('grant.title')}
      destroyOnHidden
      footer={
        <Flex justify="space-between" align="center" gap={8} wrap>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            {added > 0
              ? t('grant.addedMore', { count: added })
              : problem
                ? t(problem.key)
                : t('grant.impact')}
          </Typography.Text>
          <Flex gap={8}>
            <Button onClick={grant.close}>{t('common.cancel')}</Button>
            <Button disabled={!!problem || busy || !canWrite} onClick={() => void submit(true)}>
              {t('grant.createAndMore')}
            </Button>
            <Button
              type="primary"
              disabled={!!problem || busy || !canWrite}
              loading={busy}
              onClick={() => void submit(false)}
            >
              {t('grant.create')}
            </Button>
          </Flex>
        </Flex>
      }
    >
      <Flex vertical gap={16}>
        <div
          aria-live="polite"
          style={{
            background: 'var(--licetum-inherit-bg)',
            borderRadius: 8,
            padding: '12px 14px',
            fontSize: 15,
          }}
          data-testid="grant-sentence"
        >
          {text}
        </div>

        <GrantPreviewPanel inputs={previewInputs} ready={!problem && canWrite} />

        {!canWrite && <Alert type="warning" showIcon title={t('grant.noRight')} />}
        {error != null && (
          <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
        )}

        {/* WHO */}
        <div style={section}>
          <Label>{t('grant.who.label')}</Label>
          <Flex vertical gap={8}>
            <Segmented
              aria-label={t('grant.who.label')}
              value={form.who}
              onChange={(who) => patch({ who: who as WhoMode, everyoneConfirmed: false })}
              options={whoOptions}
            />
            {form.who === 'group' && (
              <Select
                showSearch
                aria-label={t('grant.who.group')}
                placeholder={t('grant.who.groupPlaceholder')}
                value={form.groupId}
                onChange={(groupId) => patch({ groupId })}
                optionFilterProp="label"
                loading={groups.isPending}
                options={(groups.data ?? []).map((g) => ({ value: g.id, label: g.name }))}
                notFoundContent={t('grant.who.noGroups')}
              />
            )}
            {form.who === 'person' && (
              <>
                <Input
                  aria-label={t('grant.who.person')}
                  placeholder={t('grant.who.personPlaceholder')}
                  value={form.person}
                  onChange={(e) => patch({ person: e.target.value })}
                  className="mono"
                />
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {t('grant.who.personHint')}
                </Typography.Text>
              </>
            )}
            {form.who === 'condition' && (
              <>
                <CondEditor
                  value={form.whoConds}
                  onChange={(whoConds) => patch({ whoConds })}
                  addLabel={t('cond.add')}
                  label={t('grant.who.condition')}
                />
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {t('grant.who.conditionHint')}
                </Typography.Text>
              </>
            )}
            {form.who === 'everyone' && (
              <>
                <Alert type="warning" showIcon title={t('grant.who.everyoneWarn')} />
                <Checkbox
                  checked={form.everyoneConfirmed}
                  onChange={(e) => patch({ everyoneConfirmed: e.target.checked })}
                >
                  {t('grant.who.everyoneConfirm')}
                </Checkbox>
              </>
            )}
          </Flex>
        </div>

        {/* WHAT */}
        <div style={section}>
          <Label>{t('grant.what.label')}</Label>
          <Flex vertical gap={8}>
            <Segmented
              aria-label={t('grant.what.label')}
              value={form.what}
              onChange={(what) => patch({ what: what as 'levels' | 'role' })}
              options={[
                { label: t('grant.what.levels'), value: 'levels' },
                { label: t('grant.what.role'), value: 'role' },
              ]}
            />
            {form.what === 'role' ? (
              <Select
                showSearch
                aria-label={t('grant.what.role')}
                placeholder={t('grant.what.rolePlaceholder')}
                value={form.roleId}
                onChange={(roleId) => patch({ roleId })}
                optionFilterProp="label"
                loading={roles.isPending}
                options={(roles.data ?? []).map((r) => ({ value: r.id, label: r.name }))}
                notFoundContent={t('grant.what.noRoles')}
              />
            ) : (
              <LevelsFields form={form} patch={patch} data={data} />
            )}
          </Flex>
        </div>

        {/* WHERE */}
        {form.what === 'levels' && (
          <div style={section}>
            <Label>{t('grant.where.label')}</Label>
            <WhereFields form={form} patch={patch} data={data} />
          </div>
        )}

        {/* CONDITIONS */}
        <div style={section}>
          <Label>{t('grant.when.label')}</Label>
          <Flex vertical gap={8}>
            <Flex gap={8} wrap align="center">
              <DatePicker
                aria-label={t('grant.when.from')}
                placeholder={t('grant.when.from')}
                value={form.validFrom ? dayjs(form.validFrom) : null}
                onChange={(d) => patch({ validFrom: d ? d.format('YYYY-MM-DD') : undefined })}
              />
              <DatePicker
                aria-label={t('grant.when.until')}
                placeholder={t('grant.when.until')}
                value={form.expiresAt ? dayjs(form.expiresAt) : null}
                onChange={(d) => patch({ expiresAt: d ? d.format('YYYY-MM-DD') : undefined })}
              />
            </Flex>
            <Collapse
              ghost
              size="small"
              items={[
                {
                  key: 'guard',
                  label: t('grant.when.guard'),
                  children: (
                    <Flex vertical gap={8}>
                      <CondEditor
                        value={form.requires}
                        onChange={(requires) => patch({ requires })}
                        sources={['claim', 'context', 'now']}
                        addLabel={t('cond.add')}
                        label={t('grant.when.guard')}
                      />
                      <Select
                        allowClear
                        showSearch
                        aria-label={t('grant.when.requiresGroup')}
                        placeholder={t('grant.when.requiresGroup')}
                        value={form.requiresGroupId}
                        onChange={(requiresGroupId) => patch({ requiresGroupId })}
                        optionFilterProp="label"
                        options={(groups.data ?? []).map((g) => ({ value: g.id, label: g.name }))}
                      />
                    </Flex>
                  ),
                },
              ]}
            />
          </Flex>
        </div>

        {/* ADVANCED */}
        <Collapse
          size="small"
          items={[
            {
              key: 'advanced',
              label: t('grant.advanced.label'),
              children: (
                <Flex vertical gap={12}>
                  <Input
                    aria-label={t('grant.advanced.name')}
                    placeholder={t('grant.advanced.name')}
                    value={form.name}
                    maxLength={200}
                    onChange={(e) => patch({ name: e.target.value })}
                  />
                  <Input.TextArea
                    aria-label={t('grant.advanced.description')}
                    placeholder={t('grant.advanced.description')}
                    value={form.description}
                    maxLength={2000}
                    rows={2}
                    onChange={(e) => patch({ description: e.target.value })}
                  />
                  <Checkbox
                    checked={form.deny}
                    onChange={(e) =>
                      patch({
                        deny: e.target.checked,
                        exception: false,
                        levels: e.target.checked
                          ? form.levels
                          : form.levels.filter((l) => l !== '*'),
                      })
                    }
                  >
                    {t('grant.advanced.deny')}
                  </Checkbox>
                  {form.deny && (
                    <Select
                      aria-label={t('grant.advanced.onMissing')}
                      value={form.onMissing}
                      onChange={(onMissing) => patch({ onMissing })}
                      options={[
                        { value: 'apply', label: t('grant.advanced.onMissingApply') },
                        { value: 'skip', label: t('grant.advanced.onMissingSkip') },
                      ]}
                    />
                  )}
                  <Checkbox
                    checked={form.exception}
                    disabled={form.deny}
                    onChange={(e) => patch({ exception: e.target.checked })}
                  >
                    {t('grant.advanced.exception')}
                  </Checkbox>
                  <Flex align="center" gap={8}>
                    <Switch
                      checked={form.enabled}
                      onChange={(enabledValue) => patch({ enabled: enabledValue })}
                      aria-label={t('grant.advanced.enabled')}
                    />
                    <span>{t('grant.advanced.enabled')}</span>
                  </Flex>
                </Flex>
              ),
            },
          ]}
        />
      </Flex>
    </Drawer>
  );
}
