# syntax=docker/dockerfile:1
FROM golang:1.25.7-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY executor/k8s-openhands ./executor/k8s-openhands
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
    -o /out/executor_k8s_openhands \
    ./executor/k8s-openhands/cmd/executor_k8s_openhands

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/executor_k8s_openhands /executor_k8s_openhands
USER 65532:65532
ENTRYPOINT ["/executor_k8s_openhands"]
