FROM node:22-alpine AS builder
WORKDIR /build
COPY package.json package-lock.json tsconfig.base.json ./
COPY libs/proto-gen/ ./libs/proto-gen/
COPY apps/api-gateway/ ./apps/api-gateway/
RUN npm ci --ignore-scripts
RUN npx nx build api-gateway

FROM node:22-alpine
WORKDIR /app
COPY --from=builder /build/package.json /build/package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts
COPY --from=builder /build/dist/apps/api-gateway ./dist/apps/api-gateway
EXPOSE 3000
CMD ["node", "dist/apps/api-gateway/main.js"]
