import { policyTestCaseSchema, type PolicyTestCaseValue } from './schemas';

/*
 * Before the server kept them, the scenarios of the policy tests lived in the browser. What a browser still holds is
 * offered for moving to the server once, and then forgotten.
 */

const storageKey = (serviceId: string) => `licetum_policy_tests_${serviceId}`;

/** The scenarios this browser holds for a service; entries that no longer fit the format are dropped. */
export function loadLocalPolicyTests(serviceId: string): PolicyTestCaseValue[] {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(storageKey(serviceId)) ?? '[]');
    if (!Array.isArray(raw)) return [];
    return raw.flatMap((item) => {
      const parsed = policyTestCaseSchema.safeParse(item);
      return parsed.success ? [parsed.data] : [];
    });
  } catch {
    return [];
  }
}

export function clearLocalPolicyTests(serviceId: string): void {
  try {
    localStorage.removeItem(storageKey(serviceId));
  } catch {
    /* storage is blocked: nothing to forget */
  }
}
