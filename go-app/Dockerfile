
FROM golang:1.22 AS build
WORKDIR /app

# Copiamos los archivos de Go
COPY go.mod go.sum ./
RUN go mod tidy

COPY . .

# Compilamos el binario
RUN CGO_ENABLED=0 GOOS=linux go build -o /discada

# Imagen final mínima
FROM debian:bookworm-slim
WORKDIR /app

# Copiamos el binario
COPY --from=build /discada /discada

# Copiamos la carpeta static (imágenes, etc.)
COPY --from=build /app/static ./static

EXPOSE 8080
ENTRYPOINT ["/discada"]

