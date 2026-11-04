import type { AuthzenResource, TypeResourceWithService } from '@/api/admin/model';
import type { Rule } from '@/domain/rule';

import { levelsOf } from '../grant/grant-model';

/*
 * What a person sees of a type with resources is read the way the PDP's resource search gives it: one search per access
 * level, each paged and sorted by the id of the resource. This file puts the pages of the levels together into rows (a
 * resource and the levels the person has on it) without asking more than the searches did.
 */

/** A resource a search found: the id the source gives it and its name. */
export interface Hit {
  id: string;
  name: string;
}

/** What the search of one level has given so far; `next` is the token of its next page, empty when the level is exhausted. */
export interface LevelFeed {
  level: string;
  hits: Hit[];
  next: string;
}

export interface SeenRow extends Hit {
  levels: string[];
}

/** A result of the resource search: its id, and the name the PDP puts in `properties.name` (the id when it has none). */
export function hitOf(resource: AuthzenResource): Hit {
  const id = resource.id ?? '';
  const name = resource.properties?.name;
  return { id, name: typeof name === 'string' && name !== '' ? name : id };
}

/** The pages read so far, round by round (a round asks every level that has a next page), as one feed per level. */
export function accumulate(rounds: LevelFeed[][]): LevelFeed[] {
  const byLevel = new Map<string, LevelFeed>();
  for (const round of rounds) {
    for (const feed of round) {
      const held = byLevel.get(feed.level)?.hits ?? [];
      const known = new Set(held.map((h) => h.id));
      byLevel.set(feed.level, {
        level: feed.level,
        hits: [...held, ...feed.hits.filter((h) => !known.has(h.id))],
        next: feed.next,
      });
    }
  }
  return [...byLevel.values()];
}

/**
 * The order of the ids in a page of the search: by their UTF-8 bytes (the PDP sorts with COLLATE "C"), which is the order of
 * their code points. Comparing JavaScript strings (UTF-16 code units) would put a character outside the Basic Multilingual
 * Plane before one in the range U+E000 to U+FFFF.
 */
export function compareIds(a: string, b: string): number {
  const x = a[Symbol.iterator]();
  const y = b[Symbol.iterator]();
  for (;;) {
    const p = x.next();
    const q = y.next();
    if (p.done || q.done) return Number(!p.done) - Number(!q.done);
    const d = (p.value.codePointAt(0) ?? 0) - (q.value.codePointAt(0) ?? 0);
    if (d !== 0) return d;
  }
}

/**
 * The rows of the levels read so far. A level that has more pages has only been read up to the last resource it gave, so a
 * resource beyond that point could still turn up in it: the row would show fewer levels than the person has. Only the
 * resources up to the least advanced unfinished level are therefore listed, and each row's levels are exact; the rest
 * appear as the next pages are read. `more` says that another page of some level can be read. A page may be empty and still
 * have a next one (the search stopped at its scan budget), in which case nothing is listed until it has been read on.
 */
export function mergeFeeds(feeds: LevelFeed[]): { rows: SeenRow[]; more: boolean } {
  const unfinished = feeds.filter((f) => f.next !== '');
  let frontier: string | undefined;
  let blind = false;
  for (const feed of unfinished) {
    const last = feed.hits.at(-1);
    if (!last) blind = true;
    else if (frontier === undefined || compareIds(last.id, frontier) < 0) frontier = last.id;
  }

  const byId = new Map<string, SeenRow>();
  if (!blind) {
    for (const feed of feeds) {
      for (const hit of feed.hits) {
        if (frontier !== undefined && compareIds(hit.id, frontier) > 0) continue;
        const row = byId.get(hit.id) ?? { ...hit, levels: [] };
        if (!row.levels.includes(feed.level)) row.levels.push(feed.level);
        byId.set(hit.id, row);
      }
    }
  }
  const order = new Map(feeds.map((f, i) => [f.level, i]));
  const rows = [...byId.values()].sort((a, b) => compareIds(a.id, b.id));
  for (const row of rows) row.levels.sort((a, b) => (order.get(a) ?? 0) - (order.get(b) ?? 0));
  return { rows, more: unfinished.length > 0 };
}

/**
 * The levels to ask the resource search about for a type: those its hierarchy names (read and write always), and the ones the
 * inline rules on the type grant — a level that no hierarchy mentions is still a level. Access groups (`@name`) and the
 * denial level `*` are not levels of their own. The levels a role grants are not found out here.
 */
export function levelsToAsk(type: TypeResourceWithService, rules: Rule[]): string[] {
  const levels = new Set(levelsOf(type));
  for (const rule of rules) {
    if (rule.type_resource_id !== type.id) continue;
    for (const level of rule.access ?? []) {
      if (level && level !== '*' && !level.startsWith('@')) levels.add(level);
    }
  }
  return [...levels];
}
