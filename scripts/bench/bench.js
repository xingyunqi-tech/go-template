// k6 scenario of scripts/bench/run.sh: ENDPOINT selects the request, VUS the concurrency.
import http from 'k6/http';
import { check } from 'k6';

const base = __ENV.BASE_URL || 'http://api:3000';
const endpoint = __ENV.ENDPOINT || 'me';
const json = { headers: { 'Content-Type': 'application/json' } };

export const options = {
  vus: Number(__ENV.VUS || 10),
  duration: __ENV.DURATION || '30s',
  summaryTrendStats: ['avg', 'p(50)', 'p(95)', 'p(99)', 'max'],
};

function login(email) {
  const r = http.post(`${base}/api/v1/auth/email/login`, JSON.stringify({ email, password: 'Passw0rd!' }), json);
  check(r, { 'login 200': (res) => res.status === 200 });

  return r.json('data');
}

export function setup() {
  const sessions = [];
  for (let i = 0; i < options.vus; i++) {
    sessions.push(login('user@example.com'));
  }

  return { sessions };
}

export default function (data) {
  const s = data.sessions[(__VU - 1) % data.sessions.length];
  const auth = { headers: { Authorization: `Bearer ${s.accessToken}`, 'Content-Type': 'application/json' } };
  let r;

  switch (endpoint) {
    case 'login':
      r = http.post(`${base}/api/v1/auth/email/login`, JSON.stringify({ email: 'user@example.com', password: 'Passw0rd!' }), json);
      break;
    case 'refresh':
      r = http.post(`${base}/api/v1/auth/refresh`, JSON.stringify({ refreshToken: s.refreshToken }), json);
      if (r.status === 200) {
        s.refreshToken = r.json('data.refreshToken');
      }
      break;
    case 'notes':
      r = http.get(`${base}/api/v1/notes?limit=20`, auth);
      break;
    case 'create-note':
      r = http.post(`${base}/api/v1/notes`, JSON.stringify({ title: 'bench', content: 'k6' }), auth);
      break;
    default:
      r = http.get(`${base}/api/v1/me`, auth);
  }

  check(r, { '2xx': (res) => res.status >= 200 && res.status < 300 });
}
