FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY executor/k8s-openhands/test/mock_openhands ./executor/k8s-openhands/test/mock_openhands
RUN go build -o /mock-openhands ./executor/k8s-openhands/test/mock_openhands

FROM alpine:3.22
COPY --from=build /mock-openhands /usr/local/bin/mock-openhands
USER 65532:65532
EXPOSE 8000
ENTRYPOINT ["/usr/local/bin/mock-openhands"]
