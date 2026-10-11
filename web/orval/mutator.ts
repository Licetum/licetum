/** fetch for the generated client: every call goes to the application's own proxy (src/app/api/admin), which attaches the token. */
export class ApiError extends Error {
  readonly status: number;
  readonly data: unknown;
  constructor(status: number, data: unknown) {
    super(`HTTP ${status}`);
    this.status = status;
    this.data = data;
  }
}

export const customFetch = async <T>(url: string, options: RequestInit): Promise<T> => {
  // The generated path starts with "/": a relative path guarantees the host is this application.
  if (!url.startsWith('/')) throw new Error(`customFetch: the path must start with "/": ${url}`);

  const headers = new Headers(options.headers);
  headers.set('accept-language', document.documentElement.lang || 'en');

  const response = await fetch(`/api/admin${url}`, {
    ...options,
    headers,
    credentials: 'same-origin',
  });
  const text = await response.text();
  let data: unknown;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = text;
  }

  if (response.status === 401 && typeof window !== 'undefined') {
    // The session is gone or the identity provider rejected the token: sign in again and come back here.
    const here = window.location.pathname + window.location.search;
    window.location.assign(`/auth/login?returnTo=${encodeURIComponent(here)}`);
  }
  if (!response.ok) throw new ApiError(response.status, data);
  return { data, status: response.status, headers: response.headers } as T;
};
