'use client';

import { useTranslation } from 'react-i18next';

import type { ChangeRecord } from '@/api/admin/model';
import { entityName } from '@/domain/changes';

/** "Anna deleted the access «Budget denial»": the one-line form of a record, for lists and the home page. */
export function useChangeSentence() {
  const { t } = useTranslation();
  return (record: ChangeRecord): string =>
    t('history.sentence', {
      actor: record.actor || t('history.system'),
      verb: t(`history.verb.${record.action}`),
      entity: t(`history.entity.${record.entity}`),
      name: entityName(record, t),
    });
}
