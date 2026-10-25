'use client';

import { Alert, Button, Checkbox, Drawer, Flex, Input, Segmented, Select, Typography } from 'antd';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { PEPClient } from '@/api/admin/model';
import { useCreatePepClient, useListPepIssuers, useUpdatePepClient } from '@/api/admin/pep/pep';
import { errorDetail } from '@/components/kit';

import { useRefreshAll } from '../data/queries';
import {
  clientProblem,
  createClientBody,
  emptyClientForm,
  formOfClient,
  patchClientBody,
  PEP_OPERATIONS,
  type ClientForm,
  type PepKind,
} from './pep-model';

/** Registering a PEP client of a service, or changing one (its kind stays). `state` is the client being changed, `null` for a new one. */
export function PepClientDrawer(props: {
  open: boolean;
  serviceId: string;
  state: PEPClient | null;
  onClose: () => void;
  /** Called with the client that was created, so that the keys of an api_key client can be issued next. */
  onCreated?: (client: PEPClient) => void;
}) {
  const { t } = useTranslation();
  const create = useCreatePepClient();
  const update = useUpdatePepClient();
  const refresh = useRefreshAll();
  const issuers = useListPepIssuers({
    query: { enabled: props.open, select: (r) => (r.status === 200 ? (r.data ?? []) : []) },
  });

  const [form, setForm] = useState<ClientForm>(emptyClientForm);
  const [error, setError] = useState<unknown>(null);
  const [loaded, setLoaded] = useState<string | null>(null);

  // The form starts over each time the drawer opens, with the client being changed or empty.
  const key = props.open ? (props.state?.id ?? 'new') : null;
  if (key !== loaded) {
    setLoaded(key);
    if (props.open) {
      setForm(props.state ? formOfClient(props.state) : emptyClientForm());
      setError(null);
    }
  }

  const set = (next: Partial<ClientForm>) => setForm((f) => ({ ...f, ...next }));
  const problem = clientProblem(form);
  const editing = props.state !== null;
  const registered = issuers.data ?? [];

  const submit = async () => {
    setError(null);
    try {
      if (props.state) {
        await update.mutateAsync({
          serviceId: props.serviceId,
          clientId: props.state.id,
          data: patchClientBody(form),
        });
      } else {
        const res = await create.mutateAsync({
          serviceId: props.serviceId,
          data: createClientBody(form),
        });
        if (res.status === 200) props.onCreated?.(res.data);
      }
      await refresh();
      props.onClose();
    } catch (e) {
      setError(e);
    }
  };

  return (
    <Drawer
      open={props.open}
      onClose={props.onClose}
      size={520}
      destroyOnHidden
      title={editing ? t('pep.drawer.edit', { name: props.state?.name }) : t('pep.drawer.create')}
      footer={
        <Flex justify="flex-end" gap={8}>
          <Button onClick={props.onClose}>{t('common.cancel')}</Button>
          <Button
            type="primary"
            disabled={problem !== null}
            loading={create.isPending || update.isPending}
            onClick={() => void submit()}
          >
            {editing ? t('common.save') : t('common.create')}
          </Button>
        </Flex>
      }
    >
      <Flex vertical gap={16}>
        {error != null && (
          <Alert type="error" showIcon title={errorDetail(error) ?? t('common.saveFailed')} />
        )}

        <div>
          <label htmlFor="pep-name" style={{ fontWeight: 600, display: 'block', marginBottom: 4 }}>
            {t('pep.field.name')}
          </label>
          <Input
            id="pep-name"
            value={form.name}
            maxLength={100}
            placeholder="checkout"
            onChange={(e) => set({ name: e.target.value })}
          />
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t('pep.field.nameHelp')}
          </Typography.Text>
        </div>

        <div>
          <div style={{ fontWeight: 600, marginBottom: 4 }}>{t('pep.field.kind')}</div>
          <Segmented
            aria-label={t('pep.field.kind')}
            value={form.kind}
            disabled={editing}
            onChange={(kind) => set({ kind: kind as PepKind })}
            options={[
              { label: t('pep.kind.api_key'), value: 'api_key' },
              { label: t('pep.kind.jwt'), value: 'jwt' },
            ]}
          />
          <div>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {editing ? t('pep.field.kindFixed') : t(`pep.kindHelp.${form.kind}`)}
            </Typography.Text>
          </div>
        </div>

        {form.kind === 'jwt' && (
          <>
            <div>
              <div style={{ fontWeight: 600, marginBottom: 4 }}>{t('pep.field.issuer')}</div>
              {issuers.isError ? (
                // The list of issuers is for those who may read all services; a client can still name one.
                <Input
                  aria-label={t('pep.field.issuer')}
                  className="mono"
                  value={form.issuer}
                  onChange={(e) => set({ issuer: e.target.value })}
                />
              ) : (
                <Select
                  showSearch
                  aria-label={t('pep.field.issuer')}
                  style={{ width: '100%' }}
                  loading={issuers.isPending}
                  value={form.issuer || undefined}
                  placeholder={t('pep.field.issuerPlaceholder')}
                  onChange={(issuer) => set({ issuer })}
                  options={registered.map((i) => ({ value: i.issuer, label: i.issuer }))}
                  notFoundContent={t('pep.field.noIssuers')}
                />
              )}
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {t('pep.field.issuerHelp')}
              </Typography.Text>
            </div>
            <div>
              <label
                htmlFor="pep-client-id"
                style={{ fontWeight: 600, display: 'block', marginBottom: 4 }}
              >
                {t('pep.field.clientId')}
              </label>
              <Input
                id="pep-client-id"
                className="mono"
                value={form.clientId}
                placeholder="checkout-backend"
                onChange={(e) => set({ clientId: e.target.value })}
              />
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {t('pep.field.clientIdHelp')}
              </Typography.Text>
            </div>
          </>
        )}

        <div>
          <div style={{ fontWeight: 600, marginBottom: 4 }}>{t('pep.field.operations')}</div>
          <Flex vertical gap={6} role="group" aria-label={t('pep.field.operations')}>
            {PEP_OPERATIONS.map((op) => (
              <Checkbox
                key={op}
                checked={form.operations.includes(op)}
                onChange={(e) =>
                  set({
                    operations: PEP_OPERATIONS.filter((x) =>
                      x === op ? e.target.checked : form.operations.includes(x),
                    ),
                  })
                }
              >
                <span className="mono">{op}</span>
                <span style={{ color: 'var(--licetum-muted)', marginLeft: 8 }}>
                  {t(`pep.op.${op}`)}
                </span>
              </Checkbox>
            ))}
          </Flex>
        </div>

        <Checkbox checked={form.enabled} onChange={(e) => set({ enabled: e.target.checked })}>
          {t('pep.field.enabled')}
        </Checkbox>
        {problem && (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t(problem)}
          </Typography.Text>
        )}
      </Flex>
    </Drawer>
  );
}
