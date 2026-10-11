'use client';

import { App } from 'antd';
import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';

import { useRunBatch } from '@/api/admin/batch/batch';
import { errorDetail } from '@/components/kit';
import { ruleToInput, type Rule } from '@/domain/rule';
import { useUndoNotice } from '@/lib/undo';

import { useRefreshAll } from '../data/queries';

/** Changes to rules that are done in one transaction and can be undone for ten seconds. */
export function useRuleActions() {
  const { t } = useTranslation();
  const { notification } = App.useApp();
  const batch = useRunBatch();
  const refresh = useRefreshAll();
  const undoNotice = useUndoNotice();

  const run = useCallback(
    async (operations: Parameters<typeof batch.mutateAsync>[0]['data']['operations']) => {
      await batch.mutateAsync({ data: { operations } });
      await refresh();
    },
    [batch, refresh],
  );

  const fail = useCallback(
    (error: unknown) =>
      notification.error({
        title: t('common.saveFailed'),
        description: errorDetail(error),
        placement: 'bottomLeft',
      }),
    [notification, t],
  );

  const setEnabled = useCallback(
    async (rules: Rule[], enabled: boolean) => {
      const ops = (list: Rule[], value: (r: Rule) => boolean) =>
        list.map((r) => ({
          op: 'update' as const,
          entity: 'assignment_rule' as const,
          id: r.id,
          body: { ...ruleToInput(r), enabled: value(r) },
        }));
      try {
        await run(ops(rules, () => enabled));
        undoNotice({
          message: t(enabled ? 'rule.enabledDone' : 'rule.disabledDone', { count: rules.length }),
          undo: () => run(ops(rules, (r) => r.enabled)),
        });
      } catch (e) {
        fail(e);
      }
    },
    [run, undoNotice, t, fail],
  );

  const remove = useCallback(
    async (rules: Rule[]) => {
      try {
        await run(
          rules.map((r) => ({
            op: 'delete' as const,
            entity: 'assignment_rule' as const,
            id: r.id,
          })),
        );
        undoNotice({
          message: t('rule.deletedDone', { count: rules.length }),
          undo: () =>
            run(
              rules.map((r) => ({
                op: 'create' as const,
                entity: 'assignment_rule' as const,
                id: r.id,
                body: ruleToInput(r),
              })),
            ),
        });
        return true;
      } catch (e) {
        fail(e);
        return false;
      }
    },
    [run, undoNotice, t, fail],
  );

  /** Writes the rule back with changes (a new validity, a name...); undone by writing the previous body. */
  const update = useCallback(
    async (rule: Rule, changes: Partial<ReturnType<typeof ruleToInput>>, message: string) => {
      try {
        await run([
          {
            op: 'update',
            entity: 'assignment_rule',
            id: rule.id,
            body: { ...ruleToInput(rule), ...changes },
          },
        ]);
        undoNotice({
          message,
          undo: () =>
            run([
              { op: 'update', entity: 'assignment_rule', id: rule.id, body: ruleToInput(rule) },
            ]),
        });
        return true;
      } catch (e) {
        fail(e);
        return false;
      }
    },
    [run, undoNotice, fail],
  );

  return { setEnabled, remove, update, busy: batch.isPending };
}
