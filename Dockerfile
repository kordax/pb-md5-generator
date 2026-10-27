# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26.10-bookworm@sha256:632dff7351799a5a9954a48dc06670ade72ee44e8c351cdc0e5a57af2fea21af AS source
WORKDIR /src
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

FROM source AS compiler
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/pb-md5-generator ./cmd/pb-md5-generator

FROM scratch AS cli
COPY --from=compiler /out/pb-md5-generator /pb-md5-generator
WORKDIR /work
USER 65532:65532
ENTRYPOINT ["/pb-md5-generator"]
CMD ["-help"]

FROM source AS site-build
ARG DOCS_CONFIG=docs.json
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pb-md5-generator ./cmd/pb-md5-generator
RUN /out/pb-md5-generator build -config "$DOCS_CONFIG" -o /out/site \
    && chmod -R a+rX /out/site

FROM nginx/nginx-unprivileged:stable-alpine@sha256:2daef144b574804198caccc4041819fa1801ecd5df2252a39646bfa2b1ff3bea AS site
COPY deploy/nginx.conf /etc/nginx/nginx.conf
COPY --from=site-build /out/site/ /usr/share/nginx/html/
USER 101:101
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=3s --retries=3 \
    CMD ["wget", "-q", "-T", "2", "-O", "/dev/null", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["nginx"]
CMD ["-g", "daemon off;"]
