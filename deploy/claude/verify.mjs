import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import tls from 'node:tls';

const proxy = new URL(process.env.HTTPS_PROXY);
assert.equal(process.getuid(), 1000);
assert.ok(!fs.existsSync('/var/run/docker.sock'));
assert.ok(!fs.existsSync('/workspace'));
assert.ok(!fs.existsSync('/mnt/c/Users'));

const mounts = fs.readFileSync('/proc/self/mountinfo', 'utf8').split('\n');
const root = mounts.map(line => line.split(' ')).find(fields => fields[4] === '/');
assert.ok(root?.[5].split(',').includes('ro'), 'root filesystem is not read-only');
fs.accessSync('/home/node', fs.constants.W_OK);
fs.accessSync('/tmp', fs.constants.W_OK);

const routes = fs.readFileSync('/proc/net/route', 'utf8').trim().split('\n').slice(1);
assert.ok(!routes.some(line => line.trim().split(/\s+/)[1] === '00000000'), 'unexpected default route');
for (const name of ['ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'CLAUDE_CODE_OAUTH_TOKEN', 'MUSE_HUB_TOKEN']) {
  assert.equal(process.env[name], undefined, `unexpected environment variable: ${name}`);
}

function tunnel(host, port) {
  return new Promise((resolve, reject) => {
    const req = http.request({ hostname: proxy.hostname, port: proxy.port, method: 'CONNECT', path: `${host}:${port}` });
    req.on('connect', (res, socket) => resolve({ status: res.statusCode, socket }));
    req.on('error', reject);
    req.setTimeout(10_000, () => req.destroy(new Error('CONNECT timed out')));
    req.end();
  });
}

for (const [host, port] of [['example.com', 443], ['api.anthropic.com', 80]]) {
  const { status, socket } = await tunnel(host, port);
  socket.destroy();
  assert.equal(status, 403);
}

for (const host of ['api.anthropic.com', 'platform.claude.com']) {
  const { status, socket } = await tunnel(host, 443);
  assert.equal(status, 200);
  await new Promise((resolve, reject) => {
    const secure = tls.connect({ socket, servername: host }, () => {
      secure.destroy();
      resolve();
    });
    secure.on('error', reject);
    secure.setTimeout(10_000, () => secure.destroy(new Error('TLS handshake timed out')));
  });
}

console.log('PASS: filesystem, credentials, routing, CONNECT allowlist and TLS');
