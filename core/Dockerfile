# Build from the repository root: docker build -f core/Dockerfile --target <stage> .
# The check runtime of Licetum (MIT): the binary licetum serves the permission-check API and runs the migrations.

FROM golang:1.26-alpine AS base

WORKDIR /src/core

COPY core/go.mod core/go.sum ./
# HTTP/2 downloads share one connection to the module proxy; HTTP/1.1 (GODEBUG=http2client=0) avoids a cut connection failing every module on it.
RUN GODEBUG=http2client=0 go mod download

# dev: the Go toolchain + air for hot reload; docker-compose.yml mounts ./core over /src/core.
FROM base AS dev

RUN GODEBUG=http2client=0 go install github.com/air-verse/air@v1.67.4

COPY core/ .

FROM base AS build

COPY core/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/licetum ./cmd/licetum

# runtime: a single static binary, no shell or package manager.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build /out/licetum /usr/local/bin/licetum

ENTRYPOINT ["/usr/local/bin/licetum"]
