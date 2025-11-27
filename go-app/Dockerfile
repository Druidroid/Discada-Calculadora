FROM golang:1.22 AS build
WORKDIR /app

# Módulos
COPY go.mod go.sum ./
RUN go mod tidy

# Código + static
COPY . .

# Compila el binario (ruta absoluta /discada)
RUN CGO_ENABLED=0 GOOS=linux go build -o /discada

# ---- Imagen final ----
FROM debian:bookworm-slim
WORKDIR /app

# Necesario para confiar en certificados HTTPS (scraper en Render)
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Binario compilado
COPY --from=build /discada /discada

# Carpeta static (eldorado.png, etc.)
COPY --from=build /app/static ./static

EXPOSE 8080
ENTRYPOINT ["/discada"]


