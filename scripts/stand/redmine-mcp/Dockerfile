# Open-source Redmine MCP server (MIT, https://pypi.org/project/redmine-mcp-server/).
# legacy-per-user mode: every MCP request carries the caller's
# X-Redmine-API-Key header; Airlock injects it from its encrypted MCP credential.
FROM python:3.12-slim
RUN pip install --no-cache-dir redmine-mcp-server==2.23.0
ENV REDMINE_AUTH_MODE=legacy-per-user SERVER_HOST=0.0.0.0 SERVER_PORT=8000 AUTO_CLEANUP_ENABLED=false
RUN useradd -r -u 10001 mcp && mkdir -p /app/attachments && chown mcp /app/attachments
WORKDIR /app
USER mcp
EXPOSE 8000
CMD ["redmine-mcp-server"]
