FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /athvan ./cmd/athvan

FROM gcr.io/distroless/static-debian12
COPY --from=build /athvan /athvan
ENV PORT=8080
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/athvan"]
