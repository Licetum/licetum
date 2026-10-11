import type { ServiceResponse, TypeResourceWithService } from '@/api/admin/model';

type ServiceStatus = 'ready' | 'incomplete';

export type ServiceFacts = {
  service: ServiceResponse;
  types: TypeResourceWithService[];
  rules: number;
  system: boolean;
  status: ServiceStatus;
};

/** What the lists of the API tell about a service without asking more: whether it has types and how many rules point at it. */
export function factsOf(
  service: ServiceResponse,
  types: TypeResourceWithService[],
  rules: { service_id: string | null }[],
  platformId: string | undefined,
): ServiceFacts {
  const own = types.filter((t) => t.service_id === service.id);
  return {
    service,
    types: own,
    rules: rules.filter((r) => r.service_id === service.id).length,
    system: service.id === platformId,
    status: own.length > 0 ? 'ready' : 'incomplete',
  };
}
