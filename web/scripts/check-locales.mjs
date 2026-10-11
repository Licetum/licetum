// Fails the build when the dictionaries drift apart or the code uses a key that is not defined.
// Plural forms (key_one, key_few, ...) are compared by their base key; {{placeholders}} must match per base key.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const LOCALES = ['en', 'ru', 'zh'];
const PLURAL = /_(zero|one|two|few|many|other)$/;

const flatten = (object, prefix = '', out = {}) => {
  for (const [key, value] of Object.entries(object)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value && typeof value === 'object') flatten(value, path, out);
    else out[path] = String(value);
  }
  return out;
};

const dictionaries = Object.fromEntries(
  LOCALES.map((l) => [l, flatten(JSON.parse(readFileSync(`src/i18n/locales/${l}.json`, 'utf8')))]),
);

const base = (key) => key.replace(PLURAL, '');
const placeholders = (text) => [...text.matchAll(/\{\{\s*(\w+)\s*\}\}/g)].map((m) => m[1]);
const problems = [];

const bases = Object.fromEntries(LOCALES.map((l) => [l, new Map()]));
for (const l of LOCALES) {
  for (const [key, text] of Object.entries(dictionaries[l])) {
    const b = base(key);
    const entry = bases[l].get(b) ?? new Set();
    // count is implied by a plural form, so it is not a placeholder that has to appear in every language.
    for (const p of placeholders(text)) if (p !== 'count') entry.add(p);
    bases[l].set(b, entry);
  }
}

const [first, ...rest] = LOCALES;
for (const l of rest) {
  for (const b of bases[first].keys())
    if (!bases[l].has(b)) problems.push(`${l}: missing key ${b}`);
  for (const b of bases[l].keys())
    if (!bases[first].has(b)) problems.push(`${l}: extra key ${b} (not in ${first})`);
  for (const [b, set] of bases[first]) {
    const other = bases[l].get(b);
    if (other && [...set].sort().join() !== [...other].sort().join()) {
      problems.push(
        `${l}: placeholders of ${b} differ: {${[...set].sort()}} vs {${[...other].sort()}}`,
      );
    }
  }
}

// Every key the code asks for literally must exist; every dynamic prefix must have at least one key.
const files = [];
(function walk(dir) {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) {
      if (path === 'src/api') continue;
      walk(path);
    } else if (/\.(ts|tsx)$/.test(name)) files.push(path);
  }
})('src');

const known = bases[first];
for (const file of files) {
  const text = readFileSync(file, 'utf8');
  for (const m of text.matchAll(/\bt\(\s*'([^']+)'/g)) {
    if (!known.has(m[1])) problems.push(`${file}: unknown key ${m[1]}`);
  }
  for (const m of text.matchAll(/\bt\(\s*`([^`$]*)\$\{/g)) {
    const prefix = m[1].replace(/\.$/, '');
    if (![...known.keys()].some((k) => k.startsWith(`${prefix}.`)))
      problems.push(`${file}: no keys under ${prefix}`);
  }
}

if (problems.length) {
  console.error(problems.join('\n'));
  process.exit(1);
}
console.log(`locales OK: ${known.size} keys in ${LOCALES.join(', ')}`);
