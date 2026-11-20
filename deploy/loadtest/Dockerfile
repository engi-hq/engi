# Build from this directory:  docker build -t engi-loadtest:dev deploy/loadtest
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/loadtest .

FROM scratch
COPY --from=build /out/loadtest /loadtest
USER 65532:65532
ENTRYPOINT ["/loadtest"]
