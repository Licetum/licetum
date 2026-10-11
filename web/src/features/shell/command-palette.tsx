'use client';

import { Input, Modal, Typography, type InputRef } from 'antd';
import { useRouter } from 'next/navigation';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useAccess } from '@/lib/access';

import { useGroups, useRoles, useServices } from '../data/queries';

type Entry = { key: string; group: string; label: string; hint?: string; run: () => void };

/** ⌘K: actions and a jump to a page, a service, a group or a role by name. */
export function CommandPalette(props: { open: boolean; onClose: () => void; onGrant: () => void }) {
  const { t } = useTranslation();
  const router = useRouter();
  const access = useAccess();
  const [query, setQuery] = useState('');
  const [cursor, setCursor] = useState(0);
  const input = useRef<InputRef>(null);

  const enabled = props.open;
  const services = useServices(enabled);
  const groups = useGroups(enabled);
  const roles = useRoles(enabled);

  useEffect(() => {
    if (props.open) {
      setQuery('');
      setCursor(0);
      setTimeout(() => input.current?.focus(), 50);
    }
  }, [props.open]);

  const go = (href: string) => () => {
    props.onClose();
    router.push(href);
  };

  const entries = useMemo<Entry[]>(() => {
    const a = t('palette.actions');
    const g = t('palette.goto');
    const out: Entry[] = [];
    if (access.whole('assignment_rule', 'write')) {
      out.push({
        key: 'a-grant',
        group: a,
        label: t('palette.grant'),
        run: () => {
          props.onClose();
          props.onGrant();
        },
      });
    }
    out.push({ key: 'a-check', group: a, label: t('palette.check'), run: go('/check') });
    if (access.whole('service', 'write'))
      out.push({
        key: 'a-connect',
        group: a,
        label: t('palette.connect'),
        run: go('/services/new'),
      });
    out.push(
      { key: 'g-home', group: g, label: t('nav.home'), run: go('/') },
      { key: 'g-access', group: g, label: t('nav.access'), run: go('/access') },
      { key: 'g-people', group: g, label: t('nav.people'), run: go('/people') },
      { key: 'g-roles', group: g, label: t('nav.roles'), run: go('/roles') },
      { key: 'g-services', group: g, label: t('nav.services'), run: go('/services') },
      { key: 'g-check', group: g, label: t('nav.check'), run: go('/check') },
      { key: 'g-history', group: g, label: t('nav.history'), run: go('/history') },
    );
    for (const s of services.data ?? [])
      out.push({
        key: `s-${s.id}`,
        group: t('palette.services'),
        label: s.name,
        hint: s.slug,
        run: go(`/services/${s.slug}`),
      });
    for (const x of groups.data ?? [])
      out.push({
        key: `grp-${x.id}`,
        group: t('palette.groups'),
        label: x.name,
        run: go(`/people/groups/${x.id}`),
      });
    for (const r of roles.data ?? [])
      out.push({
        key: `r-${r.id}`,
        group: t('palette.roles'),
        label: r.name,
        run: go(`/roles/${r.id}`),
      });
    return out;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t, access, services.data, groups.data, roles.data]);

  const q = query.trim().toLowerCase();
  const shown = (
    q
      ? entries.filter((e) => `${e.label} ${e.hint ?? ''}`.toLowerCase().includes(q))
      : entries.filter((e) => e.key.startsWith('a-') || e.key.startsWith('g-'))
  ).slice(0, 12);

  return (
    <Modal
      open={props.open}
      onCancel={props.onClose}
      footer={null}
      closable={false}
      width={560}
      destroyOnHidden
      styles={{ body: { padding: 0 } }}
      style={{ top: 96 }}
    >
      <Input
        ref={input}
        size="large"
        variant="borderless"
        value={query}
        placeholder={t('palette.placeholder')}
        aria-label={t('palette.placeholder')}
        onChange={(e) => {
          setQuery(e.target.value);
          setCursor(0);
        }}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') {
            e.preventDefault();
            setCursor((c) => Math.min(c + 1, shown.length - 1));
          }
          if (e.key === 'ArrowUp') {
            e.preventDefault();
            setCursor((c) => Math.max(c - 1, 0));
          }
          if (e.key === 'Enter') shown[cursor]?.run();
        }}
        style={{
          padding: '14px 20px',
          borderBottom: '1px solid var(--licetum-border)',
          borderRadius: 0,
        }}
      />
      <div
        role="listbox"
        aria-label={t('palette.placeholder')}
        style={{ maxHeight: 360, overflowY: 'auto' }}
      >
        {shown.length === 0 && (
          <div style={{ padding: '16px 20px', color: 'var(--licetum-muted)' }}>
            {t('palette.empty')}
          </div>
        )}
        {shown.map((entry, index) => (
          <div
            key={entry.key}
            role="option"
            aria-selected={index === cursor}
            onClick={entry.run}
            onMouseEnter={() => setCursor(index)}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 12,
              cursor: 'pointer',
              padding: '8px 20px',
              background: index === cursor ? 'var(--licetum-inherit-bg)' : undefined,
            }}
          >
            <Typography.Text type="secondary" style={{ width: 96, fontSize: 12 }}>
              {entry.group}
            </Typography.Text>
            <span style={{ flex: 1 }}>{entry.label}</span>
            {entry.hint && (
              <span className="mono" style={{ color: 'var(--licetum-muted)' }}>
                {entry.hint}
              </span>
            )}
          </div>
        ))}
      </div>
    </Modal>
  );
}
