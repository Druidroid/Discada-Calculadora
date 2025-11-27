# Etapa de compilación
FROM golang:1.22 AS build

WORKDIR /app

# Copiamos los archivos de Go
COPY . .

# Resolvemos dependencias
RUN go mod tidy

# Compilamos binario estático para Linux
RUN CGO_ENABLED=0 GOOS=linux go build -o /discada

# Etapa final: runtime mínimo pero con certificados TLS
FROM debian:bookworm-slim

# 🔑 INSTALAR CERTIFICADOS DE CA PARA HTTPS
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copiar binario y estáticos
COPY --from=build /discada /discada
COPY --from=build /static ./static

EXPOSE 8080

CMD ["/discada"]
