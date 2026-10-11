'use client';

import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { useCallback } from 'react';

// Changes made in the same tick (a "reset filters" button clears five parameters) are collected and written to the URL
// once: separate navigations would each start from the old address and the last one would win.
let pending: URLSearchParams | null = null;

function update(
  router: ReturnType<typeof useRouter>,
  pathname: string,
  change: (query: URLSearchParams) => void,
) {
  if (!pending) {
    pending = new URLSearchParams(window.location.search);
    queueMicrotask(() => {
      const query = pending?.toString() ?? '';
      pending = null;
      router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false });
    });
  }
  change(pending);
}

/** A string parameter of the URL as state: filters and tabs survive a reload and can be shared as a link. */
export function useQueryState(
  name: string,
  fallback = '',
): [string, (value: string | null) => void] {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const value = params.get(name) ?? fallback;

  const set = useCallback(
    (next: string | null) =>
      update(router, pathname, (query) => {
        if (next === null || next === '' || next === fallback) query.delete(name);
        else query.set(name, next);
      }),
    [router, pathname, name, fallback],
  );
  return [value, set];
}

/** A multi-value parameter as `a,b,c`. */
export function useQueryList(name: string): [string[], (values: string[]) => void] {
  const [raw, setRaw] = useQueryState(name);
  const values = raw ? raw.split(',').filter(Boolean) : [];
  return [values, (next) => setRaw(next.length ? next.join(',') : null)];
}
