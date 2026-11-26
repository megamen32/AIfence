FROM python:3.12-slim
RUN apt-get update && apt-get install --no-install-recommends -y git && rm -rf /var/lib/apt/lists/* \
 && pip install --no-cache-dir "git+https://github.com/expremiental/yandex-metrika-mcp.git@4e708b38327ff4056e1e634cd3ea862b214edd1a"
RUN useradd --create-home --uid 10001 mcp
USER mcp
ENTRYPOINT ["yandex-metrika-mcp"]
