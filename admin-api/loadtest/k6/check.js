// Load test of the AuthZEN API of Licetum, run by admin-api/loadtest (see its README).
//
// Arrivals are an open model: k6 starts a request at each tick of the rate whether or not earlier answers have come
// back, so a server that falls behind shows as late answers and dropped iterations, not as a lower rate. Every request
// is recorded in the metrics of its phase ("warmup" or "measure") and of its operation, so each percentile is computed
// over all the requests of that operation in that phase, and the summary says how many there were.
//
// The choices of the script come from a generator seeded with SEED. The data is deterministic (the seeding); the choices
// are deterministic per virtual user; which iteration a virtual user runs is up to k6's scheduler. The cold mode is the
// exception: its choices are a function of the number of the iteration, not of the virtual user.
import http from 'k6/http';
import exec from 'k6/execution';
import { SharedArray } from 'k6/data';
import { Counter, Rate, Trend } from 'k6/metrics';

// The check servers: one per replica. A request goes to one of them, drawn at random, so the replicas share the load.
const bases = (__ENV.BASES || __ENV.BASE_URL).split(',');
const service = __ENV.SERVICE || 'loadtest';
const users = Number(__ENV.USERS);
const groups = Number(__ENV.GROUPS);
const documents = Number(__ENV.DOCUMENTS);
const rules = Number(__ENV.RULES);
const folders = Number(__ENV.FOLDERS);
const backend = __ENV.BACKEND_TOKEN;
const mode = __ENV.MODE || 'mix';
const consistencySetting = __ENV.CONSISTENCY || 'minimize_latency';
const seed = Number(__ENV.SEED || 1);
// The number of the first iteration of this invocation among all the iterations of the run: the cold mode asks every
// question once, so a later window must go on where the earlier ones stopped.
const firstIteration = Number(__ENV.FIRST_ITERATION || 0);

// The generator the current draws come from. It is declared before the warm pool is drawn at start-up.
let rng = null;
const rate = Number(__ENV.RATE || 10);
const warmSeconds = Number(__ENV.WARM_SECONDS || 0);
const measureSeconds = Number(__ENV.MEASURE_SECONDS || 60);

// The tokens are read once for all the virtual users: one token per subject, in the order of the subjects.
const tokens = new SharedArray('tokens', () => JSON.parse(open(__ENV.TOKENS)));

// The operations, in the order the harness reports them (opNames in mix.go lists the same, and a test keeps them so).
const ops = ['search_resource', 'evaluation', 'evaluations10', 'evaluations100', 'evaluation_properties'];
const weights = parseMix(__ENV.MIX || 'search_resource=2,evaluation=5,evaluations10=2,evaluation_properties=1');

const phases = ['warmup', 'measure'];
const metrics = {};
for (const phase of phases) {
  const m = {
    latency: {},
    requests: {},
    failed: new Rate(`failed_${phase}`),
    decisions: new Counter(`decisions_${phase}`),
    allowed: new Counter(`allowed_${phase}`),
    answered2xx: new Counter(`answered_2xx_${phase}`),
    answeredOther: new Counter(`answered_other_${phase}`),
  };
  for (const op of ops) {
    m.latency[op] = new Trend(`latency_${op}_${phase}`, true);
    m.requests[op] = new Counter(`requests_${op}_${phase}`);
  }
  metrics[phase] = m;
}

function arrivals(start, duration) {
  return {
    executor: 'constant-arrival-rate',
    rate,
    timeUnit: '1s',
    duration: `${duration}s`,
    startTime: `${start}s`,
    preAllocatedVUs: Number(__ENV.PRE_VUS || 10),
    maxVUs: Number(__ENV.MAX_VUS || 250),
  };
}

const scenarios = {};
if (warmSeconds > 0) {
  scenarios.warmup = arrivals(0, warmSeconds);
}
scenarios.measure = arrivals(warmSeconds, measureSeconds);

export const options = {
  scenarios,
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)'],
};

// The pairs of the warm mode: a fixed set of (subject, document), asked again and again, so that the caches answer.
const warmPool = buildWarmPool();

export default function () {
  const base = bases.length === 1 ? bases[0] : bases[Math.floor(random() * bases.length)];
  const phase = exec.scenario.name === 'warmup' ? 'warmup' : 'measure';
  const m = metrics[phase];

  // The subject and the document of the request.
  let subjectIndex;
  let doc;
  if (mode === 'warm') {
    const pair = warmPool[Math.floor(random() * warmPool.length)];
    subjectIndex = pair.subject;
    doc = pair.doc;
  } else if (mode === 'cold') {
    // Every request asks what no earlier request of the run asked, so no answer a cache holds fits it.
    const n = firstIteration + exec.scenario.iterationInTest;
    subjectIndex = coldSubject(n);
    doc = coldDoc(subjectIndex, Math.floor(n / users));
  } else {
    // A Zipf-like skew (exponent 1): the subjects near the start of the range are asked about far more often.
    subjectIndex = Math.min(users - 1, Math.floor(Math.pow(users, random())) - 1);
    doc = accessibleDoc(subjectIndex);
  }
  const subject = `user-${subjectIndex}`;
  const dept = `dept-${subjectIndex % groups}`;
  const token = tokens[subjectIndex];
  // The cold mode sends no consistency query: its questions are new, so no cache holds an answer to them. The other modes
  // ask with the consistency of the run.
  const consistencyQuery = mode === 'cold' ? '' : `?consistency=${consistencySetting}`;

  const op = ops[pick()];
  let res;
  switch (op) {
    case 'search_resource':
      // One page of the documents the subject may read.
      res = http.post(
        `${base}/service/${service}/access/v1/search/resource${consistencyQuery}`,
        `{"subject":{"type":"user","id":"${subject}"},"resource":{"type":"document"},"action":{"name":"read"},"page":{"limit":20}}`,
        { headers: jsonHeaders(token) },
      );
      break;
    case 'evaluation':
      res = http.post(
        `${base}/service/${service}/access/v1/evaluation${consistencyQuery}`,
        `{"subject":{"type":"user","id":"${subject}"},"resource":{"type":"document","id":"${doc}"},"action":{"name":"read"}}`,
        { headers: jsonHeaders(token) },
      );
      break;
    case 'evaluations10':
      res = evaluationsRequest(base, token, consistencyQuery, subjectIndex, 10, doc);
      break;
    case 'evaluations100':
      res = evaluationsRequest(base, token, consistencyQuery, subjectIndex, 100, doc);
      break;
    case 'evaluation_properties':
      // The token is the one of the client application the service trusts; the subject is another person, whose claims
      // the client brings.
      res = http.post(
        `${base}/service/${service}/access/v1/evaluation${consistencyQuery}`,
        `{"subject":{"type":"user","id":"${subject}","properties":{"department":"${dept}"}},"resource":{"type":"document","id":"${doc}"},"action":{"name":"read"}}`,
        { headers: jsonHeaders(backend) },
      );
      break;
  }

  m.latency[op].add(res.timings.duration);
  m.requests[op].add(1);
  let ok = res.status >= 200 && res.status < 300;
  let answers = { total: 0, allowed: 0 };
  if (ok) {
    answers = countDecisions(res.body);
    // An evaluation the API could not decide comes back in a 200 answer as decision false with context.error. It is not
    // a denial: it counts as a failed request.
    ok = answers.errors === 0;
  }
  m.failed.add(!ok);
  if (ok) {
    m.answered2xx.add(1);
    m.decisions.add(answers.total);
    m.allowed.add(answers.allowed);
  } else {
    m.answeredOther.add(1);
  }
}

function jsonHeaders(token) {
  return { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' };
}

// questions are n documents the subject may read, for a batch of n evaluations.
function questions(subjectIndex, n) {
  const out = [];
  for (let k = 0; k < n; k++) {
    out.push(accessibleDoc(subjectIndex));
  }
  return out;
}

// evaluationsRequest is a batch of n evaluations of documents the subject may read, about the subject. In the cold mode
// the first one is the document no earlier request asked about this subject, so the batch is a question no earlier one was.
function evaluationsRequest(base, token, consistencyQuery, subjectIndex, n, doc) {
  const docs = questions(subjectIndex, n);
  if (mode === 'cold') {
    docs[0] = doc;
  }
  const evaluations = docs.map((d) => `{"resource":{"type":"document","id":"${d}"}}`);
  return http.post(
    `${base}/service/${service}/access/v1/evaluations${consistencyQuery}`,
    `{"subject":{"type":"user","id":"user-${subjectIndex}"},"action":{"name":"read"},"evaluations":[${evaluations.join(',')}]}`,
    { headers: jsonHeaders(token) },
  );
}

// accessibleDoc is a document the subject may read, most of the time. The owner pattern grants the documents of the
// subject (document g is owned by subject g mod users); a rule of the subject's department grants a folder: the
// department of rule r is (r div 10) mod groups, its folder is r mod folders, and the kinds 0-3 and 7 grant a folder.
// The rest asks about any document, which is mostly denied. These are the rules the seeding writes (ruleFor in seed.go):
// this function must follow them.
function accessibleDoc(subjectIndex) {
  const roll = random();
  if (roll < 0.2) {
    return randomDoc();
  }
  if (roll < 0.4) {
    const owned = Math.floor(random() * Math.ceil(documents / users));
    const g = subjectIndex + users * owned;
    return g < documents ? `d-${g}` : randomDoc();
  }
  const dept = subjectIndex % groups;
  const kinds = [0, 1, 2, 3, 7];
  for (let attempt = 0; attempt < 20; attempt++) {
    const kind = kinds[Math.floor(random() * kinds.length)];
    const r = 10 * (dept + groups * Math.floor(random() * Math.ceil(rules / (10 * groups)))) + kind;
    if (r >= rules || r === 0) {
      continue;
    }
    const folder = r % folders;
    const g = folder + folders * Math.floor(random() * Math.ceil(documents / folders));
    return g < documents ? `d-${g}` : randomDoc();
  }
  return randomDoc();
}

// The cold mode walks the subjects in rounds: each round visits every subject once, in an order that scrambles them, and
// the k-th visit of a subject asks it about the k-th document of a sequence of its own that repeats none. So no pair of
// (subject, document) is asked twice, and a question about a subject is never the one an earlier request asked. A search
// lists everything a subject may read, so it is new only the first time a subject comes up (the first round).
const subjectStride = coprimeStride(users);

function coldSubject(n) {
  return ((n % users) * subjectStride) % users;
}

function coprimeStride(n) {
  const gcd = (a, b) => (b === 0 ? a : gcd(b, a % b));
  let stride = Math.max(1, Math.round(n * 0.6180339887));
  while (gcd(stride, n) !== 1) {
    stride++;
  }
  return stride % n || 1;
}

// The documents already asked of each subject by this virtual user, in the order of the visits. The sequence of a subject
// is a function of the seed and the subject alone, so every virtual user builds the same one.
const coldSequences = new Map();

function coldDoc(subjectIndex, visit) {
  let sequence = coldSequences.get(subjectIndex);
  if (!sequence) {
    sequence = { draw: mulberry32(seed * 7919 + subjectIndex), seen: new Set(), docs: [] };
    coldSequences.set(subjectIndex, sequence);
  }
  while (sequence.docs.length <= visit) {
    const doc = unaskedDoc(subjectIndex, sequence);
    sequence.seen.add(doc);
    sequence.docs.push(doc);
  }
  return sequence.docs[visit];
}

// unaskedDoc draws the documents the subject may read, as the other modes do, until one that was not asked comes up; when
// the documents it is likely to be asked about are used up, it takes the next document of the source that was not asked.
function unaskedDoc(subjectIndex, sequence) {
  for (let attempt = 0; attempt < 20; attempt++) {
    const doc = accessibleDocWith(subjectIndex, sequence.draw);
    if (!sequence.seen.has(doc)) {
      return doc;
    }
  }
  let g = Math.floor(sequence.draw() * documents);
  while (sequence.seen.has(`d-${g}`) && sequence.seen.size < documents) {
    g = (g + 1) % documents;
  }
  return `d-${g}`;
}

function randomDoc() {
  return `d-${Math.floor(random() * documents)}`;
}

// buildWarmPool draws the pairs of the warm mode from the seed: the same pairs in every run.
function buildWarmPool() {
  const pool = [];
  const draw = mulberry32(seed);
  for (let k = 0; k < 100; k++) {
    const subject = Math.floor(draw() * users);
    pool.push({ subject, doc: accessibleDocWith(subject, draw) });
  }
  return pool;
}

// accessibleDocWith is accessibleDoc with the draws of the given generator (the warm pool is drawn with its own one).
function accessibleDocWith(subjectIndex, draw) {
  const saved = rng;
  rng = draw;
  const doc = accessibleDoc(subjectIndex);
  rng = saved;
  return doc;
}

// The generator of the choices, seeded per virtual user from SEED (set on the first draw of each virtual user).
function random() {
  if (rng === null) {
    rng = mulberry32(seed * 1000003 + exec.vu.idInTest);
  }
  return rng();
}

function mulberry32(a) {
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// pick draws the kind of the next request by its weight.
function pick() {
  const total = weights.reduce((sum, w) => sum + w, 0);
  let n = Math.floor(random() * total);
  for (let k = 0; k < weights.length; k++) {
    if (n < weights[k]) {
      return k;
    }
    n -= weights[k];
  }
  return 0;
}

// parseMix reads "name=weight,..." into the weights of the operations, in the order of ops.
function parseMix(spec) {
  const out = ops.map(() => 0);
  for (const part of spec.split(',')) {
    const [name, weight] = part.split('=');
    const k = ops.indexOf(name.trim());
    if (k >= 0) {
      out[k] = Number(weight);
    }
  }
  return out;
}

// countDecisions counts the answers of a response: every "decision" field of the AuthZEN API, the granted ones, and the
// evaluations that carry context.error. The bodies are the ones the API writes, so the fields are matched as written.
// A search has no decisions.
function countDecisions(body) {
  const count = (s) => body.split(s).length - 1;
  return {
    total: count('"decision":'),
    allowed: count('"decision":true'),
    errors: count('"error":'),
  };
}
