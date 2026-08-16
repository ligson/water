# syntax=docker/dockerfile:1.7

ARG NODE_BASE=node:22-alpine
ARG NGINX_BASE=nginx:1.27-alpine

FROM --platform=$BUILDPLATFORM ${NODE_BASE} AS build

WORKDIR /src

COPY water-fe/package.json water-fe/package-lock.json ./
RUN npm ci

COPY water-fe/ ./
RUN npm run build

FROM ${NGINX_BASE}

COPY water-fe/docker/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html

EXPOSE 80
