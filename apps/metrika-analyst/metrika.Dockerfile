FROM node:22-bookworm-slim
WORKDIR /opt/metrika
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --no-audit --no-fund && npm cache clean --force
ENV PATH="/opt/metrika/node_modules/.bin:$PATH"
USER node
ENV YANDEX_METRIKA_ENABLE_WRITE=false
ENTRYPOINT ["supergateway","--stdio","yandex-metrica-mcp","--outputTransport","streamableHttp","--stateful","--host","127.0.0.1","--port","8000","--tools","get_metadata","--tools","describe_counter","--tools","run_report","--tools","run_comparison","--tools","run_timeseries","--tools","run_drilldown"]
