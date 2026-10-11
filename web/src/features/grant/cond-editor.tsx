'use client';

import { Button, Flex, Input, Select } from 'antd';
import { Plus, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { newCond, type Cond, type CondOp, type CondSource } from './grant-model';

const OPS: CondOp[] = ['eq', 'ne', 'in', 'not_in', 'regex', 'gt', 'ge', 'lt', 'le', 'in_cidr'];
const OP_LABEL: Record<CondOp, string> = {
  eq: '=',
  ne: '≠',
  in: '∈',
  not_in: '∉',
  regex: '~',
  gt: '>',
  ge: '≥',
  lt: '<',
  le: '≤',
  in_cidr: '⊂',
};

/** Rows "attribute — operator — value", joined by AND. `sources` limits where the attribute may come from. */
export function CondEditor(props: {
  value: Cond[];
  onChange: (next: Cond[]) => void;
  sources?: CondSource[];
  attrPlaceholder?: string;
  addLabel: string;
  label: string;
}) {
  const { t } = useTranslation();
  const sources = props.sources ?? ['claim'];
  const update = (id: string, patch: Partial<Cond>) =>
    props.onChange(props.value.map((c) => (c.id === id ? { ...c, ...patch } : c)));

  return (
    <Flex vertical gap={8}>
      {props.value.map((c, index) => (
        <Flex key={c.id} gap={8} align="center" wrap>
          {sources.length > 1 && (
            <Select
              aria-label={t('cond.source')}
              value={c.source}
              style={{ width: 130 }}
              onChange={(source) => update(c.id, { source })}
              options={sources.map((s) => ({ value: s, label: t(`cond.sources.${s}`) }))}
            />
          )}
          <Input
            aria-label={`${props.label} ${index + 1}: ${t('cond.attr')}`}
            value={c.attr}
            placeholder={props.attrPlaceholder ?? t('cond.attrPlaceholder')}
            onChange={(e) => update(c.id, { attr: e.target.value })}
            style={{ width: 160 }}
            className="mono"
          />
          <Select
            aria-label={t('cond.op')}
            value={c.op}
            style={{ width: 84 }}
            onChange={(op) => update(c.id, { op })}
            options={OPS.map((o) => ({ value: o, label: `${OP_LABEL[o]} ${t(`cond.ops.${o}`)}` }))}
            popupMatchSelectWidth={200}
            labelRender={({ value }) => OP_LABEL[value as CondOp]}
          />
          <Input
            aria-label={`${props.label} ${index + 1}: ${t('cond.value')}`}
            value={c.value}
            placeholder={
              ['in', 'not_in', 'in_cidr'].includes(c.op) ? t('cond.valueList') : t('cond.value')
            }
            onChange={(e) => update(c.id, { value: e.target.value })}
            style={{ flex: 1, minWidth: 140 }}
          />
          <Button
            type="text"
            aria-label={t('cond.remove')}
            icon={<X size={16} aria-hidden />}
            onClick={() => props.onChange(props.value.filter((x) => x.id !== c.id))}
          />
        </Flex>
      ))}
      <div>
        <Button
          size="small"
          icon={<Plus size={14} aria-hidden />}
          onClick={() => props.onChange([...props.value, newCond(sources[0])])}
        >
          {props.addLabel}
        </Button>
      </div>
    </Flex>
  );
}
