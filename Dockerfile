# --- build ---
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api

# --- run (imagen mínima, sin shell) ---
# Nota: corre como root para poder escribir el volumen de uploads (/data) en Fly.
# Se endurecerá a nonroot cuando las imágenes se muevan a object storage (R2).
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
