FROM node:22-alpine AS builder
WORKDIR /build
COPY package.json package-lock.json tsconfig.base.json nx.json ./
COPY libs/ ./libs/
COPY proto/ ./proto/
COPY apps/client/ ./apps/client/
# --legacy-peer-deps: jest 30 vs the Angular devkit optional peer (jest 29), same as local installs
RUN npm ci --ignore-scripts --legacy-peer-deps
RUN npx nx build client --configuration=production

FROM nginx:1.27-alpine
COPY apps/client/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /build/dist/apps/client/browser /usr/share/nginx/html
EXPOSE 4200
CMD ["nginx", "-g", "daemon off;"]
