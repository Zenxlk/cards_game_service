# syntax=docker/dockerfile:1
FROM golang:1.24 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

# distroless:nonroot corre como UID 65532 e incluye certificados TLS/tzdata
# (hacen falta para verificar el propio TLS del proxy de Cloud Run y para
# timestamps de eventos en zonas horarias no-UTC).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
