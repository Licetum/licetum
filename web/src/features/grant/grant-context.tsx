'use client';

import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';

/** What the panel "Give access" starts with, when it is opened from another screen. */
export type GrantPrefill = {
  /** A group id, "person:<username>" or "everyone". */
  who?: { kind: 'group'; id: string } | { kind: 'person'; username: string };
  serviceId?: string;
  typeId?: string;
  resourceId?: string;
  roleId?: string;
};

type GrantValue = {
  isOpen: boolean;
  prefill: GrantPrefill | null;
  open: (prefill?: GrantPrefill) => void;
  close: () => void;
};

const GrantContext = createContext<GrantValue | null>(null);

export function GrantProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<{ isOpen: boolean; prefill: GrantPrefill | null }>({
    isOpen: false,
    prefill: null,
  });
  const open = useCallback(
    (prefill?: GrantPrefill) => setState({ isOpen: true, prefill: prefill ?? null }),
    [],
  );
  const close = useCallback(() => setState((s) => ({ ...s, isOpen: false })), []);
  const value = useMemo(() => ({ ...state, open, close }), [state, open, close]);
  return <GrantContext.Provider value={value}>{children}</GrantContext.Provider>;
}

export const useGrant = (): GrantValue => {
  const value = useContext(GrantContext);
  if (!value) throw new Error('useGrant outside GrantProvider');
  return value;
};
