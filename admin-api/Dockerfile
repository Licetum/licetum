# Build from the repository root: docker build -f admin-api/Dockerfile --target <stage> .
# The admin API of Licetum (AGPL). It depends on the core module, which the build copies next to it (the replace of go.mod).

FROM golang:1.26-alpine AS base

WORKDIR /src

COPY core/go.mod core/go.sum core/
COPY admin-api/go.mod admin-api/go.sum admin-api/
RUN cd admin-api && GODEBUG=http2client=0 go mod download

# dev: the Go toolchain + air for hot reload; docker-compose.yml mounts ./admin-api and ./core over /src.
FROM base AS dev

RUN GODEBUG=http2client=0 go install github.com/air-verse/air@v1.67.4

COPY core/ core/
COPY admin-api/ admin-api/
WORKDIR /src/admin-api

FROM base AS build

COPY core/ core/
COPY admin-api/ admin-api/
RUN cd admin-api && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/licetum-admin ./cmd/licetum-admin

# runtime: a single static binary, no shell or package manager.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build /out/licetum-admin /usr/local/bin/licetum-admin

ENTRYPOINT ["/usr/local/bin/licetum-admin"]
