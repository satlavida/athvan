FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /athvan ./cmd/athvan \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /athvan /athvan
# API keys live here; mount a volume so they survive container restarts.
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV PORT=8080 ATHVAN_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/athvan"]
