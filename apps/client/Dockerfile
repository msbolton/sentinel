# The Angular bundle is architecture-independent, so the build always runs on the
# build host's own architecture and only the nginx stage is per-target.
FROM --platform=$BUILDPLATFORM node:22-alpine AS builder
WORKDIR /build
COPY package.json package-lock.json tsconfig.base.json nx.json ./
COPY libs/ ./libs/
COPY proto/ ./proto/
COPY apps/client/ ./apps/client/
# --legacy-peer-deps: jest 30 vs the Angular devkit optional peer (jest 29), same as local installs
RUN npm ci --ignore-scripts --legacy-peer-deps
RUN npx nx build client --configuration=production

# Unprivileged nginx runs as uid 101 and keeps its pid and temp files under /tmp,
# so it works with a read-only root filesystem and runAsNonRoot.
FROM nginxinc/nginx-unprivileged:1.27-alpine
# static.conf is the part every deployment shares; default.conf wraps it with the
# Compose proxy blocks and is what the Helm chart replaces.
COPY apps/client/nginx/static.conf /etc/nginx/sentinel/static.conf
COPY apps/client/nginx/default.conf /etc/nginx/conf.d/default.conf
# Writes /var/run/sentinel/config.json (map keys) from the container environment at start.
COPY --chmod=755 apps/client/docker-entrypoint.d/40-sentinel-config.sh /docker-entrypoint.d/40-sentinel-config.sh
USER root
RUN mkdir -p /var/run/sentinel && chown 101:101 /var/run/sentinel
USER 101
COPY --from=builder /build/dist/apps/client/browser /usr/share/nginx/html
EXPOSE 4200
CMD ["nginx", "-g", "daemon off;"]
