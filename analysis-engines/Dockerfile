FROM docker.1ms.run/library/node@sha256:ba849c60be29959425b8734d57b8b4b7d56f98edd9504c9af091d5281095a71e AS dependencies
WORKDIR /engine
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts --registry=https://registry.npmjs.org && npm cache clean --force
FROM docker.1ms.run/library/node@sha256:50c3b2f6988dfc307b86e5301d69611af31f4789bdf232863b07d3b02fe55ae0
WORKDIR /engine
COPY --from=dependencies /engine/node_modules ./node_modules
COPY package.json package-lock.json ./
COPY *.mjs ./
COPY rules ./rules
COPY notices ./notices
RUN apt-get update && apt-get install -y --no-install-recommends libssl3t64 ca-certificates && rm -rf /var/lib/apt/lists/*
COPY bin/heimdall /usr/local/bin/heimdall
COPY bin/anvil /usr/local/bin/anvil
ENV HOME=/tmp HEIMDALL_BINARY=/usr/local/bin/heimdall ANVIL_BINARY=/usr/local/bin/anvil
USER 65532:65532
ENTRYPOINT ["node", "/engine/cli.mjs"]
