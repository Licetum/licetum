// Load test of the Licetum check API, run by admin-api/loadtest (see its README).
//
// The arrivals are an open model: k6 starts a request at each tick of the rate, whether or not the earlier answers have
// come back, so a server that falls behind shows as dropped iterations and late answers, not as a lower rate. The
// harness sets the settings through the environment, runs one step per rate, and reads the summary k6 writes.
import http from 'k6/http';
import { check } from 'k6';
import { SharedArray } from 'k6/data';
import { Counter, Trend } from 'k6/metrics';

const base = __ENV.BASE_URL;
const service = __ENV.SERVICE || 'loadtest';
const users = Number(__ENV.USERS);
const groups = Number(__ENV.GROUPS);
const documents = Number(__ENV.DOCUMENTS);
const rules = Number(__ENV.RULES);
const folders = Number(__ENV.FOLDERS);
const backend = __ENV.BACKEND_TOKEN;
const consistency = __ENV.CONSISTENCY || 'minimize_latency';

// The tokens are read once for all the virtual users: one token per subject, in the order of the subjects.
const tokens = new SharedArray('tokens', () => JSON.parse(open(__ENV.TOKENS)));

// The requests of the mix, in the order the harness reports them.
const names = ['permission', 'check', 'authzen', 'authzen_batch', 'on_behalf'];
const weights = parseMix(__ENV.MIX || 'permission=2,check=3,authzen=3,authzen_batch=1,on_behalf=1');
const latency = {};
for (const name of names) {
  latency[name] = new Trend(`latency_${name}`, true);
}
const decisions = new Counter('decisions');
const allowed = new Counter('allowed');

export const options = {
  scenarios: {
    traffic: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 10),
      timeUnit: '1s',
      duration: __ENV.DURATION || '15s',
      preAllocatedVUs: Number(__ENV.PRE_VUS || 50),
      maxVUs: Number(__ENV.MAX_VUS || 250),
    },
  },
  // The summary carries these for every trend: the harness reads p(99) and the median.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
  // The thresholds mark a step that misses its objectives when k6 runs it alone (exit code 99). The harness judges the
  // step from the summary itself, with the same objectives.
  thresholds: {
    http_req_failed: [`rate<${__ENV.MAX_ERROR_RATE || 0.01}`],
    http_req_duration: [`p(99)<${__ENV.SLO_P99_MS || 100}`],
    dropped_iterations: ['count==0'],
  },
};

export default function () {
  const i = Math.floor(Math.random() * users);
  const subject = `user-${i}`;
  const dept = `dept-${i % groups}`;
  const doc = accessibleDoc(i);
  const token = tokens[i];

  const kind = pick();
  const op = names[kind];
  const withToken = { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' };
  let res;
  switch (op) {
    case 'permission':
      res = http.get(`${base}/service/${service}/permission?type=document&limit=20&consistency=${consistency}`, {
        headers: { Authorization: `Bearer ${token}` },
        tags: { op },
      });
      break;
    case 'check':
      res = http.post(
        `${base}/service/${service}/check?consistency=${consistency}`,
        `{"checks":[{"type":"document","resource_id":"${doc}","action":"read"}]}`,
        { headers: withToken, tags: { op } },
      );
      break;
    case 'authzen':
      res = http.post(
        `${base}/service/${service}/access/v1/evaluation?consistency=${consistency}`,
        `{"subject":{"type":"user","id":"${subject}"},"resource":{"type":"document","id":"${doc}"},"action":{"name":"read"}}`,
        { headers: withToken, tags: { op } },
      );
      break;
    case 'authzen_batch':
      res = http.post(
        `${base}/service/${service}/access/v1/evaluations?consistency=${consistency}`,
        authzenBatch(subject, i),
        { headers: withToken, tags: { op } },
      );
      break;
    case 'on_behalf':
      res = http.post(
        `${base}/service/${service}/on_behalf/check?consistency=${consistency}`,
        `{"subject":"${subject}","claims":{"department":"${dept}"},"checks":[{"type":"document","resource_id":"${doc}","action":"read"}]}`,
        { headers: { Authorization: `Bearer ${backend}`, 'Content-Type': 'application/json' }, tags: { op } },
      );
      break;
  }

  latency[op].add(res.timings.duration);
  check(res, { 'status is 2xx': (r) => r.status >= 200 && r.status < 300 });
  if (res.status === 200) {
    const answers = countDecisions(res.body);
    decisions.add(answers.total);
    allowed.add(answers.allowed);
  }
}

// pick draws the kind of the next request by its weight.
function pick() {
  const total = weights.reduce((sum, w) => sum + w, 0);
  let n = Math.floor(Math.random() * total);
  for (let k = 0; k < weights.length; k++) {
    if (n < weights[k]) {
      return k;
    }
    n -= weights[k];
  }
  return 0;
}

// parseMix reads "name=weight,..." into the weights of the requests, in the order of names.
function parseMix(spec) {
  const out = names.map(() => 0);
  for (const part of spec.split(',')) {
    const [name, weight] = part.split('=');
    const k = names.indexOf(name.trim());
    if (k >= 0) {
      out[k] = Number(weight);
    }
  }
  return out;
}

// authzenBatch is a batch of ten evaluations of random documents for one subject.
function authzenBatch(subject, subjectIndex) {
  const evaluations = [];
  for (let k = 0; k < 10; k++) {
    evaluations.push(`{"resource":{"type":"document","id":"${accessibleDoc(subjectIndex)}"}}`);
  }
  return `{"subject":{"type":"user","id":"${subject}"},"action":{"name":"read"},"evaluations":[${evaluations.join(',')}]}`;
}

// accessibleDoc is the document a request asks about. Most of the traffic asks about a document the subject may read,
// as a client asks about what it shows: the owner pattern grants the documents of the subject (g = subject mod users),
// and a rule of the subject's department grants a folder (the department of rule r is (r div 10) mod groups, and its
// folder is r mod folders). The rest asks about any document, which is mostly denied. The rules are the ones the seeding writes
// (see ruleFor in seed.go): this function must follow them.
function accessibleDoc(subjectIndex) {
  const roll = Math.random();
  if (roll < 0.2) {
    return randomDoc();
  }
  if (roll < 0.4) {
    const owned = Math.floor(Math.random() * Math.ceil(documents / users));
    const g = subjectIndex + users * owned;
    return g < documents ? `d-${g}` : randomDoc();
  }
  const dept = subjectIndex % groups;
  for (let attempt = 0; attempt < 20; attempt++) {
    const kinds = [0, 1, 2, 3, 7];
    const kind = kinds[Math.floor(Math.random() * kinds.length)];
    const r = 10 * (dept + groups * Math.floor(Math.random() * Math.ceil(rules / (10 * groups)))) + kind;
    if (r >= rules || r === 0) {
      continue;
    }
    {
      const folder = r % folders;
      const g = folder + folders * Math.floor(Math.random() * Math.ceil(documents / folders));
      return g < documents ? `d-${g}` : randomDoc();
    }
  }
  return randomDoc();
}

function randomDoc() {
  return `d-${Math.floor(Math.random() * documents)}`;
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
