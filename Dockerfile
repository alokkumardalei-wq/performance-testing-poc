# Plugin backend image. The frontend bundle is built first and embedded into
# the Go binary via go:embed.
FROM node:22-alpine AS ui
WORKDIR /ui
COPY package.json package-lock.json ./
RUN npm ci
COPY tsconfig.json vite.config.ts index.html ./
COPY src ./src
RUN npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=ui /ui/dist/main.js ./dist/main.js
RUN CGO_ENABLED=0 go build -o /out/plugin-backend . && \
    CGO_ENABLED=0 go build -o /out/everest-perf ./cmd/everest-perf

FROM alpine:3.20
RUN adduser -D -u 10001 plugin
COPY --from=build /out/plugin-backend /usr/local/bin/plugin-backend
COPY --from=build /out/everest-perf /usr/local/bin/everest-perf
USER 10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/plugin-backend"]
