'use client';

import { createContext, useContext, useMemo, type ReactNode } from 'react';

import type { TypeAccess } from '@/api/admin/model';
import { useGetPlatformMe } from '@/api/admin/platform/platform';

/** The platform policies (resource types of the self-service); the names are fixed by the backend. */
export type Policy =
  | 'service'
  | 'type_resource'
  | 'group'
  | 'group_member'
  | 'role'
  | 'assignment_rule';
export type Level = 'read' | 'write';

type AccessValue = {
  loading: boolean;
  failed: boolean;
  /** The caller has no access to any policy. */
  empty: boolean;
  /** Levels on the policy as a whole (so also creating entities). */
  whole: (policy: Policy, level: Level) => boolean;
  /** Levels on the policy's entities taken together: whole-type access or at least one entity. */
  any: (policy: Policy, level: Level) => boolean;
  subject: string;
};

const AccessContext = createContext<AccessValue | null>(null);

const has = (levels: string[] | null | undefined, level: Level) => !!levels?.includes(level);

export function AccessProvider({ children }: { children: ReactNode }) {
  const query = useGetPlatformMe({
    query: { select: (r) => (r.status === 200 ? r.data : undefined), staleTime: 30_000 },
  });
  const me = query.data;

  const value = useMemo<AccessValue>(() => {
    const types = (me?.types ?? {}) as Record<string, TypeAccess>;
    return {
      loading: query.isPending,
      failed: query.isError,
      empty: !!me && Object.keys(types).length === 0,
      subject: me?.subject ?? '',
      whole: (policy, level) => has(types[policy]?.whole_type, level),
      any: (policy, level) => {
        const access = types[policy];
        return (
          has(access?.whole_type, level) || !!access?.entries?.some((e) => has(e.access, level))
        );
      },
    };
  }, [me, query.isPending, query.isError]);

  return <AccessContext.Provider value={value}>{children}</AccessContext.Provider>;
}

export const useAccess = (): AccessValue => {
  const value = useContext(AccessContext);
  if (!value) throw new Error('useAccess outside AccessProvider');
  return value;
};
