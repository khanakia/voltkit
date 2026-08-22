# The volt CI image (FORGE_GITLAB_PLAN FG-D3): container-native runners
# (GitLab, Bitbucket) run `volt ci` / `volt release --from-tag` inside this
# image, keeping every forge's CI file a thin shell — logic stays in the
# binary, exactly the volt-action property without porting volt-action.
#
# Build:  task volt:image        (tags ghcr.io/khanakia/volt:dev)
# Push:   manual for now — becomes a credential-gated release channel when
#         the GitLab live E2E lands (see docsi/FORGE_GITLAB_PLAN.md).

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
# The workspace build needs every module present; ldflags stamp the version
# the same way volt's own release does (dev builds carry "dev").
ARG VOLT_VERSION=dev
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -C apps/volt -ldflags "-s -w -X main.voltVersion=${VOLT_VERSION}" -o /out/volt .

FROM golang:1.24-alpine
# git: volt's version source and dirty-check. glab: the GitLab publish
# driver. curl/tar: install scripts and archive verification. golangci-lint:
# `volt ci`'s lint step (volt skips it loudly when absent — present here so
# CI never skips).
RUN apk add --no-cache git curl tar glab \
 && curl -fsSL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
    | sh -s -- -b /usr/local/bin
COPY --from=build /out/volt /usr/local/bin/volt
# Runners mount the checkout; volt detects everything from the directory.
WORKDIR /work
ENTRYPOINT []
CMD ["volt"]
