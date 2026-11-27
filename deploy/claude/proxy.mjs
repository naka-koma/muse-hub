import http from 'node:http';
import net from 'node:net';

const allowedHosts = new Set([
  'api.anthropic.com',
  'platform.claude.com',
  'claude.ai',
  'claude.com',
  'sso.claude.com',
]);

const server = http.createServer({ headersTimeout: 5_000, requestTimeout: 5_000 }, (_req, res) => {
  res.writeHead(403).end();
});

function reply(socket, status) {
  socket.end(`HTTP/1.1 ${status}\r\nConnection: close\r\n\r\n`);
}

server.on('connect', (req, socket, head) => {
  socket.on('error', () => socket.destroy());
  const match = /^([a-z0-9.-]+):443$/.exec(req.url.toLowerCase());
  if (!match || !allowedHosts.has(match[1])) {
    reply(socket, '403 Forbidden');
    return;
  }

  const upstream = net.connect({ host: match[1], port: 443, timeout: 8_000 });
  socket.on('close', () => upstream.destroy());
  upstream.on('timeout', () => upstream.destroy());
  upstream.on('error', () => socket.destroy());
  upstream.on('close', () => socket.destroy());
  upstream.once('connect', () => {
    upstream.setTimeout(60_000);
    socket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
    if (head.length) upstream.write(head);
    upstream.pipe(socket).pipe(upstream);
  });
});

server.listen(3128);
