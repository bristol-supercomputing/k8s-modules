FROM docker.io/library/golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY internal/ internal/
COPY cmd/ cmd/
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/module-controller ./cmd/module-controller

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/module-controller /module-controller
USER 65532:65532
ENTRYPOINT ["/module-controller"]
