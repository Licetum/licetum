// Load test of the Licetum check API, run by admin-api/loadtest (see its README).
//
// Arrivals are an open model: k6 starts a request at each tick of the rate whether or not earlier answers have come
// back, so a server that falls behind shows as late answers and dropped iterations, not as a lower rate. Every request
// is recorded in the metrics of its phase ("warmup" or "measure") and of its operation, so each percentile is computed
// over all the requests of that operation in that phase, and the summary says how many there were.
//
// The choices of the script come from a generator seeded with SEED. The data is deterministic (the seeding); the choices
// are deterministic per virtual user; which iteration a virtual user runs is up to k6's scheduler.
import http from 'k6/http';
import exec from 'k6/execution';
import { SharedArray } from 'k6/data';
import { Counter, Rate, Trend } from 'k6/metrics';

const base = __ENV.BASE_URL;
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

// The generator the current draws come from. It is declared before the warm pool is drawn at start-up.
let rng = null;
const rate = Number(__ENV.RATE || 10);
const warmSeconds = Number(__ENV.WARM_SECONDS || 0);
const measureSeconds = Number(__ENV.MEASURE_SECONDS || 60);

// The tokens are read once for all the virtual users: one token per subject, in the order of the subjects.
const tokens = new SharedArray('tokens', () => JSON.parse(open(__ENV.TOKENS)));

// The operations, in the order the harness reports them.
const ops = ['permission', 'check1', 'check10', 'check100', 'authzen', 'authzen_batch', 'on_behalf'];
const weights = parseMix(__ENV.MIX || 'permission=2,check1=3,authzen=3,authzen_batch=1,on_behalf=1');

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
    subjectIndex = Math.floor(random() * users);
    doc = accessibleDoc(subjectIndex);
  } else {
    // A Zipf-like skew (exponent 1): the subjects near the start of the range are asked about far more often.
    subjectIndex = Math.min(users - 1, Math.floor(Math.pow(users, random())) - 1);
    doc = accessibleDoc(subjectIndex);
  }
  const subject = `user-${subjectIndex}`;
  const dept = `dept-${subjectIndex % groups}`;
  const token = tokens[subjectIndex];
  // The cold mode asks the sources again on every request (consistency=higher skips the caches).
  const consistency = mode === 'cold' ? 'higher' : consistencySetting;

  const op = ops[pick()];
  let res;
  switch (op) {
    case 'permission':
      res = http.get(`${base}/service/${service}/permission?type=document&limit=20&consistency=${consistency}`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      break;
    case 'check1':
      res = checkRequest(token, consistency, [doc]);
      break;
    case 'check10':
      res = checkRequest(token, consistency, questions(subjectIndex, 10));
      break;
    case 'check100':
      res = checkRequest(token, consistency, questions(subjectIndex, 100));
      break;
    case 'authzen':
      res = http.post(
        `${base}/service/${service}/access/v1/evaluation?consistency=${consistency}`,
        `{"subject":{"type":"user","id":"${subject}"},"resource":{"type":"document","id":"${doc}"},"action":{"name":"read"}}`,
        { headers: jsonHeaders(token) },
      );
      break;
    case 'authzen_batch':
      res = http.post(
        `${base}/service/${service}/access/v1/evaluations?consistency=${consistency}`,
        authzenBatch(subjectIndex),
        { headers: jsonHeaders(token) },
      );
      break;
    case 'on_behalf':
      res = http.post(
        `${base}/service/${service}/on_behalf/check?consistency=${consistency}`,
        `{"subject":"${subject}","claims":{"department":"${dept}"},"checks":[{"type":"document","resource_id":"${doc}","action":"read"}]}`,
        { headers: jsonHeaders(backend) },
      );
      break;
  }

  m.latency[op].add(res.timings.duration);
  m.requests[op].add(1);
  const ok = res.status >= 200 && res.status < 300;
  m.failed.add(!ok);
  if (ok) {
    m.answered2xx.add(1);
    const answers = countDecisions(res.body);
    m.decisions.add(answers.total);
    m.allowed.add(answers.allowed);
  } else {
    m.answeredOther.add(1);
  }
}

// checkRequest asks the check API the given questions, each (document, read).
function checkRequest(token, consistency, docs) {
  const checks = docs.map((d) => `{"type":"document","resource_id":"${d}","action":"read"}`).join(',');
  return http.post(`${base}/service/${service}/check?consistency=${consistency}`, `{"checks":[${checks}]}`, {
    headers: jsonHeaders(token),
  });
}

function jsonHeaders(token) {
  return { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' };
}

// questions are n documents the subject may read, for a check with n questions.
function questions(subjectIndex, n) {
  const out = [];
  for (let k = 0; k < n; k++) {
    out.push(accessibleDoc(subjectIndex));
  }
  return out;
}

// authzenBatch is a batch of ten evaluations of documents the subject may read.
function authzenBatch(subjectIndex) {
  const evaluations = [];
  for (let k = 0; k < 10; k++) {
    evaluations.push(`{"resource":{"type":"document","id":"${accessibleDoc(subjectIndex)}"}}`);
  }
  return `{"subject":{"type":"user","id":"user-${subjectIndex}"},"action":{"name":"read"},"evaluations":[${evaluations.join(',')}]}`;
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

// countDecisions counts the answers of a response: every "allowed" (check, on-behalf) and "decision" (AuthZEN) field,
// and the granted ones. The bodies are the ones the API writes, so the fields are matched as written.
function countDecisions(body) {
  const count = (s) => body.split(s).length - 1;
  return {
    total: count('"allowed":') + count('"decision":'),
    allowed: count('"allowed":true') + count('"decision":true'),
  };
}
