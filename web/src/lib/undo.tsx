'use client';

import { App } from 'antd';
import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';

/**
 * After a change the person gets a notice with "Undo" for ten seconds instead of a question before it (brief §4.6).
 * `undo` performs the inverse operation; its failure is reported, not hidden.
 */
export function useUndoNotice() {
  const { notification } = App.useApp();
  const { t } = useTranslation();

  return useCallback(
    (options: {
      message: string;
      description?: string;
      undo: () => Promise<unknown>;
      onDone?: () => void;
    }) => {
      const key = `undo-${Date.now()}`;
      notification.success({
        key,
        title: options.message,
        description: options.description,
        placement: 'bottomLeft',
        duration: 10,
        actions: (
          <button
            type="button"
            onClick={async () => {
              notification.destroy(key);
              try {
                await options.undo();
                notification.info({ title: t('undo.done'), placement: 'bottomLeft', duration: 3 });
              } catch {
                notification.error({ title: t('undo.failed'), placement: 'bottomLeft' });
              } finally {
                options.onDone?.();
              }
            }}
            style={{
              all: 'unset',
              cursor: 'pointer',
              color: 'var(--licetum-accent)',
              fontWeight: 600,
              padding: '4px 8px',
            }}
          >
            {t('undo.action')}
          </button>
        ),
      });
    },
    [notification, t],
  );
}
