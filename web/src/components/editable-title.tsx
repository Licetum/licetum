'use client';

import { Button, Input } from 'antd';
import { Pencil } from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

/** A page title that can be renamed in place: a pencil turns it into a field; Enter or leaving it saves, Escape cancels. */
export function EditableTitle(props: {
  value: string;
  display?: ReactNode;
  label: string;
  onSave: (next: string) => void;
}) {
  const { t } = useTranslation();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(props.value);
  const input = useRef<import('antd').InputRef>(null);

  useEffect(() => {
    if (editing) {
      setDraft(props.value);
      input.current?.focus();
      input.current?.select();
    }
  }, [editing, props.value]);

  const finish = (save: boolean) => {
    setEditing(false);
    if (save && draft.trim() !== props.value) props.onSave(draft);
  };

  if (editing) {
    return (
      <Input
        ref={input}
        aria-label={props.label}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={() => finish(true)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') finish(true);
          if (e.key === 'Escape') finish(false);
        }}
        style={{ fontSize: 20, fontWeight: 600, maxWidth: 560 }}
      />
    );
  }
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
      {props.display ?? props.value}
      <Button
        type="text"
        size="small"
        aria-label={`${props.label}: ${t('common.edit')}`}
        icon={<Pencil size={16} aria-hidden />}
        onClick={() => setEditing(true)}
      />
    </span>
  );
}
