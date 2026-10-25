/*
 * `next/navigation` needs the App Router around it, which a test of one screen does not have. The jest configuration maps the
 * module here: the screens read the address and the query from `navigation`, and what they navigate to is recorded in
 * `navigation.moves`.
 */
export const navigation = {
  path: '/',
  params: new URLSearchParams(),
  moves: [] as string[],
};

export const useRouter = () => ({
  push: (href: string) => void navigation.moves.push(href),
  replace: (href: string) => void navigation.moves.push(href),
  refresh: () => {},
  back: () => {},
  prefetch: () => {},
});
export const usePathname = () => navigation.path;
export const useSearchParams = () => navigation.params;
export const useServerInsertedHTML = () => {};
