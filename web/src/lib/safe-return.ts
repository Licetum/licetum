/** A path inside this application that is safe to return to after signing in; anything else becomes "/". */
export function safeReturnTo(value: string | null | undefined): string {
  if (!value || !value.startsWith('/') || value.startsWith('//') || value.startsWith('/\\'))
    return '/';
  if (value.startsWith('/auth/') || value.startsWith('/login') || value.startsWith('/api/'))
    return '/';
  return value;
}
