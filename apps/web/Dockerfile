FROM node:22-alpine AS builder
WORKDIR /build
COPY package.json package-lock.json tsconfig.base.json ./
COPY libs/ ./libs/
COPY apps/web/ ./apps/web/
RUN npm ci --ignore-scripts
RUN npx nx build web --configuration=production

FROM nginx:1.27-alpine
COPY apps/web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /build/dist/apps/web/browser /usr/share/nginx/html
EXPOSE 4200
CMD ["nginx", "-g", "daemon off;"]
