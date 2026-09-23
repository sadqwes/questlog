# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/questlog ./cmd/questlog

# distroless: нет shell и пакетного менеджера — меньше поверхность атаки
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/questlog /questlog
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/questlog"]
