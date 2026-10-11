'use client';

import { App } from 'antd';
import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';

import { useRevertChange, useRollbackChanges } from '@/api/admin/batch/batch';
import { errorDetail } from '@/components/kit';

import { useRefreshAll } from '../data/queries';

/** Putting a change of the log back as it was: a create is deleted, a delete is created again, an update is written back. */
export function useRevert() {
  const { t } = useTranslation();
  const { notification } = App.useApp();
  const revert = useRevertChange();
  const rollback = useRollbackChanges();
  const refresh = useRefreshAll();

  const report = useCallback(
    (error: unknown) =>
      notification.error({
        title: t('history.revertFailed'),
        description: errorDetail(error),
        placement: 'bottomLeft',
      }),
    [notification, t],
  );

  /** The plan of the revert, without applying it. */
  const planOne = useCallback(
    async (changeId: number) =>
      (await revert.mutateAsync({ changeId, data: { dry_run: true } })).data,
    [revert],
  );
  const planTo = useCallback(
    async (to: number) => (await rollback.mutateAsync({ data: { to, dry_run: true } })).data,
    [rollback],
  );

  const applyOne = useCallback(
    async (changeId: number) => {
      try {
        await revert.mutateAsync({ changeId, data: { dry_run: false } });
        await refresh();
        notification.success({
          title: t('history.reverted'),
          placement: 'bottomLeft',
          duration: 4,
        });
        return true;
      } catch (e) {
        report(e);
        return false;
      }
    },
    [revert, refresh, notification, t, report],
  );

  const applyTo = useCallback(
    async (to: number) => {
      try {
        await rollback.mutateAsync({ data: { to, dry_run: false } });
        await refresh();
        notification.success({
          title: t('history.rolledBack'),
          placement: 'bottomLeft',
          duration: 4,
        });
        return true;
      } catch (e) {
        report(e);
        return false;
      }
    },
    [rollback, refresh, notification, t, report],
  );

  return { planOne, planTo, applyOne, applyTo, busy: revert.isPending || rollback.isPending };
}
