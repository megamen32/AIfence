FROM node:24-bookworm-slim@sha256:873f586f3794f52281bd039bf02c048a0be08fde619338f9941bad1eb6b34530
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts --no-audit --no-fund && npm cache clean --force
ENV NODE_ENV=production ASKADS_TELEMETRY=0
LABEL org.opencontainers.image.title="AIfence Wordstat MCP" \
      io.airlock.wordstat.version="2.5.0" \
      io.airlock.wordstat.revision="5d7395ed352679ad639e872fd7fe232e2c1c61c9" \
      io.airlock.supergateway.version="4.3.0" \
      io.airlock.supergateway.revision="0afe993496636bb30dc1eb6810dc4d18f0f66f42"
USER node
EXPOSE 3000
ENTRYPOINT ["/app/node_modules/.bin/supergateway"]
CMD ["--stdio", "/app/node_modules/.bin/mcp-yandex-wordstat", "--outputTransport", "streamableHttp", "--host", "0.0.0.0", "--port", "3000", "--healthEndpoint", "/healthz", "--logLevel", "none", "--apiKeyFile", "/run/secrets/wordstat-service-token"]
