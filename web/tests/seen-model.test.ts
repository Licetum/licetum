import { describe, expect, it } from '@jest/globals';

import type { AuthzenResource, TypeResourceWithService } from '@/api/admin/model';
import type { Rule } from '@/domain/rule';
import {
  accumulate,
  compareIds,
  hitOf,
  levelsToAsk,
  mergeFeeds,
  type LevelFeed,
} from '@/features/check/seen-model';

/*
 * The oracle here is the definition of a row: the levels a person has on a resource are exactly the levels whose search
 * lists it. `truth` is the full answer of each level, and a feed is a prefix of it, as the pages of the PDP's resource search
 * are (sorted by id, a next token while there is more). Whatever is shown from a prefix has to agree with the full answer.
 */

const hit = (id: string) => ({ id, name: `name of ${id}` });

/** The first `n` resources of the level's full answer, as the pages read so far; `next` is set while there are more. */
function prefix(level: string, truth: string[], n: number): LevelFeed {
  return { level, hits: truth.slice(0, n).map(hit), next: n < truth.length ? 'more' : '' };
}

/** The levels each id has in the full answers. */
function levelsOfId(truths: Record<string, string[]>, id: string) {
  return Object.entries(truths)
    .filter(([, ids]) => ids.includes(id))
    .map(([level]) => level);
}

describe('mergeFeeds', () => {
  it('shows every resource with all of its levels when the levels are exhausted', () => {
    const { rows, more } = mergeFeeds([
      prefix('read', ['B1', 'B2', 'B3'], 3),
      prefix('write', ['B2'], 1),
    ]);
    expect(more).toBe(false);
    expect(rows.map((r) => [r.id, r.levels])).toEqual([
      ['B1', ['read']],
      ['B2', ['read', 'write']],
      ['B3', ['read']],
    ]);
  });

  it('holds back the resources an unfinished level has not reached, so a row never lacks a level the person has', () => {
    // write is exhausted; read has been read up to B3 only and may still list B4 ... and write has B5, which read may list too.
    const truths = { read: ['B1', 'B3', 'B5', 'B7'], write: ['B1', 'B5'] };
    const { rows, more } = mergeFeeds([
      prefix('read', truths.read, 2),
      prefix('write', truths.write, 2),
    ]);
    expect(more).toBe(true);
    // B5 is known to write, but read has only been read up to B3: B5 is not shown until read has passed it.
    expect(rows.map((r) => r.id)).toEqual(['B1', 'B3']);
    for (const row of rows) expect(row.levels).toEqual(levelsOfId(truths, row.id));
  });

  it('agrees with the full answers at every stage of reading, for any pages', () => {
    const truths = {
      read: ['a', 'b', 'c', 'd', 'e', 'f', 'g'],
      write: ['b', 'd', 'f', 'g'],
      approve: ['c', 'g'],
    };
    const levels = Object.keys(truths);
    // Every combination of how far each level has been read.
    for (let r = 0; r <= 7; r++) {
      for (let w = 0; w <= 4; w++) {
        for (let a = 0; a <= 2; a++) {
          const { rows, more } = mergeFeeds([
            prefix('read', truths.read, r),
            prefix('write', truths.write, w),
            prefix('approve', truths.approve, a),
          ]);
          for (const row of rows) {
            expect(row.levels).toEqual(
              levels.filter((l) => truths[l as keyof typeof truths].includes(row.id)),
            );
          }
          // Everything the three levels list is eventually shown: with all levels exhausted nothing is held back.
          if (!more) {
            expect(rows.map((x) => x.id)).toEqual(['a', 'b', 'c', 'd', 'e', 'f', 'g']);
          }
          expect(more).toBe(r < 7 || w < 4 || a < 2);
        }
      }
    }
  });

  it('shows nothing from a level whose page was empty but has a next one (the scan stopped at its budget)', () => {
    const { rows, more } = mergeFeeds([
      { level: 'read', hits: [], next: 'budget' },
      prefix('write', ['B1'], 1),
    ]);
    expect(rows).toEqual([]);
    expect(more).toBe(true);
  });

  it('lists the levels in the order they were asked, and the rows in the order of the ids', () => {
    const { rows } = mergeFeeds([prefix('write', ['b', 'a'], 2), prefix('read', ['a', 'b'], 2)]);
    expect(rows.map((r) => r.id)).toEqual(['a', 'b']);
    expect(rows[0]?.levels).toEqual(['write', 'read']);
  });

  it('has no rows and nothing more for a person who has no level', () => {
    expect(mergeFeeds([prefix('read', [], 0), prefix('write', [], 0)])).toEqual({
      rows: [],
      more: false,
    });
  });
});

describe('compareIds', () => {
  it('orders ids by their UTF-8 bytes, as the PDP sorts them', () => {
    expect(['b', 'B', 'a', '10', '9'].sort(compareIds)).toEqual(['10', '9', 'B', 'a', 'b']);
    expect(compareIds('a', 'ab')).toBeLessThan(0);
    expect(compareIds('ab', 'ab')).toBe(0);
    // U+FF5E is one UTF-16 unit above a surrogate pair's first unit, but its code point is below U+1F600.
    expect(compareIds('～', '\u{1f600}')).toBeLessThan(0);
    expect('～' > '\u{1f600}').toBe(true); // what comparing the strings would have said
  });
});

describe('accumulate', () => {
  it('appends the pages of a level, keeps the last token and does not repeat a resource', () => {
    const feeds = accumulate([
      [
        { level: 'read', hits: [hit('a'), hit('b')], next: 't1' },
        { level: 'write', hits: [hit('a')], next: '' },
      ],
      [{ level: 'read', hits: [hit('b'), hit('c')], next: '' }],
    ]);
    expect(feeds).toEqual([
      { level: 'read', hits: [hit('a'), hit('b'), hit('c')], next: '' },
      { level: 'write', hits: [hit('a')], next: '' },
    ]);
  });
});

describe('hitOf', () => {
  it('takes the name from the properties and falls back to the id', () => {
    const named: AuthzenResource = { type: 't', id: 'B1', properties: { name: 'Branch 1' } };
    expect(hitOf(named)).toEqual({ id: 'B1', name: 'Branch 1' });
    expect(hitOf({ type: 't', id: 'B2' })).toEqual({ id: 'B2', name: 'B2' });
    expect(hitOf({ type: 't', id: 'B3', properties: { name: 7 } })).toEqual({
      id: 'B3',
      name: 'B3',
    });
  });
});

describe('levelsToAsk', () => {
  const type = {
    id: 'type-1',
    name: 'branch',
    access_implies: { approve: ['write'], write: ['read'] },
  } as unknown as TypeResourceWithService;
  const rule = (over: Partial<Rule>) =>
    ({ type_resource_id: 'type-1', access: [], ...over }) as Rule;

  it('asks the levels of the hierarchy and the ones the inline rules on the type grant', () => {
    expect(levelsToAsk(type, [])).toEqual(['read', 'write', 'approve']);
    expect(levelsToAsk(type, [rule({ access: ['export', 'read'] })])).toEqual([
      'read',
      'write',
      'approve',
      'export',
    ]);
  });

  it('leaves out access groups, the denial level and the rules of other types', () => {
    const levels = levelsToAsk(type, [
      rule({ access: ['@editors', '*'] }),
      rule({ type_resource_id: 'other', access: ['audit'] }),
      rule({ access: null }),
    ]);
    expect(levels).toEqual(['read', 'write', 'approve']);
  });
});
