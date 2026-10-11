'use client';

import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useMemo } from 'react';

import { useListAssignmentRules } from '@/api/admin/assignment-rule/assignment-rule';
import { listChanges } from '@/api/admin/change-log/change-log';
import { listGroupMembers, useListGroupMembers } from '@/api/admin/group-member/group-member';
import { useListGroups } from '@/api/admin/group/group';
import type {
  ChangeRecord,
  Group,
  GroupMember,
  Role,
  ServiceResponse,
  TypeResourceWithService,
} from '@/api/admin/model';
import { useListRoles } from '@/api/admin/role/role';
import { useListServices } from '@/api/admin/service/service';
import { useListAllTypeResources } from '@/api/admin/type-resource/type-resource';
import type { Rule } from '@/domain/rule';

/** The lists everything else is built from. Each one is a single request, kept fresh by TanStack Query. */
export function useServices(enabled = true) {
  return useListServices({
    query: {
      enabled,
      select: (r) => (r.status === 200 ? (r.data ?? []) : ([] as ServiceResponse[])),
    },
  });
}
export function useGroups(enabled = true) {
  return useListGroups({
    query: { enabled, select: (r) => (r.status === 200 ? (r.data ?? []) : ([] as Group[])) },
  });
}
export function useRoles(enabled = true) {
  return useListRoles({
    query: { enabled, select: (r) => (r.status === 200 ? (r.data ?? []) : ([] as Role[])) },
  });
}
export function useRules() {
  return useListAssignmentRules({
    query: { select: (r) => (r.status === 200 ? (r.data ?? []) : ([] as Rule[])) },
  });
}
export function useAllTypes() {
  return useListAllTypeResources({
    query: {
      select: (r) => (r.status === 200 ? (r.data ?? []) : ([] as TypeResourceWithService[])),
    },
  });
}

/** Group names by id: rules and memberships carry ids, people read names. */
export function useGroupNames() {
  const groups = useGroups();
  const names = useMemo(
    () => new Map((groups.data ?? []).map((g) => [g.id, g.name])),
    [groups.data],
  );
  return { ...groups, name: useCallback((id: string) => names.get(id), [names]) };
}

/**
 * Every record of the change log, oldest first. The API reads forward from a position (there is no "latest N" yet, E6),
 * so the log is read page by page; later reads continue from the last record already held.
 */
export function useChangeLog(options?: {
  serviceId?: string;
  entity?: string;
  enabled?: boolean;
  liveMs?: number;
}) {
  const client = useQueryClient();
  const key = ['/changes', 'all', options?.serviceId ?? '', options?.entity ?? ''] as const;
  return useQuery({
    queryKey: key,
    enabled: options?.enabled ?? true,
    refetchInterval: options?.liveMs,
    queryFn: async () => {
      const held = client.getQueryData<ChangeRecord[]>(key) ?? [];
      const out = [...held];
      let after = out.at(-1)?.id ?? 0;
      for (let page = 0; page < 40; page++) {
        const res = await listChanges({
          after,
          limit: 500,
          service_id: options?.serviceId,
          entity: options?.entity,
        });
        const records = res.status === 200 ? (res.data ?? []) : [];
        out.push(...records);
        if (records.length < 500) break;
        after = records.at(-1)?.id ?? after;
      }
      return out;
    },
  });
}

/** Refreshes everything after a write: another list may have changed as a side effect (a group, a rule, the history). */
export function useRefreshAll() {
  const client = useQueryClient();
  return useCallback(() => client.invalidateQueries(), [client]);
}

/** The memberships of every group, read one group at a time (there is no list of all memberships yet). */
export function useAllMembers(groupIds: string[]) {
  const results = useQueries({
    queries: groupIds.map((id) => ({
      queryKey: ['members-of', id],
      queryFn: async () => {
        const res = await listGroupMembers(id);
        return res.status === 200 ? (res.data ?? []) : ([] as GroupMember[]);
      },
    })),
  });
  const pending = results.some((r) => r.isPending);
  const byGroup = new Map<string, GroupMember[]>();
  groupIds.forEach((id, i) => byGroup.set(id, results[i]?.data ?? []));
  return { pending, byGroup, refetch: () => Promise.all(results.map((r) => r.refetch())) };
}

export function useGroupMembers(groupId: string) {
  return useListGroupMembers(groupId, {
    query: {
      enabled: groupId !== '',
      select: (r) => (r.status === 200 ? (r.data ?? []) : ([] as GroupMember[])),
    },
  });
}
