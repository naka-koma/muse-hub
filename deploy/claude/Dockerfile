FROM node:24-bookworm-slim@sha256:873f586f3794f52281bd039bf02c048a0be08fde619338f9941bad1eb6b34530 AS base

FROM base AS claude
RUN npm install --global @anthropic-ai/claude-code@2.1.251 && claude --version
COPY verify.mjs /opt/verify.mjs
USER node
ENTRYPOINT ["claude"]

FROM base AS proxy
COPY proxy.mjs /opt/proxy.mjs
USER node
ENTRYPOINT ["node", "/opt/proxy.mjs"]
