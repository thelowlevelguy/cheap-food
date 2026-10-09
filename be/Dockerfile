# Étape 1 : Compilation du backend Go avec SQLite
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Télécharger les dépendances
COPY go.mod go.sum ./
RUN go mod download

# Copier le code source
COPY . .

# Compiler le serveur Go avec le support SQLite
RUN CGO_ENABLED=1 GOOS=linux go build -o cheapdish-backend .

# Étape 2 : Image d'exécution
FROM alpine:3.23

RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates sqlite-libs

WORKDIR /app

# Copier le binaire et les fichiers nécessaires
COPY --from=builder /app/cheapdish-backend .
COPY --from=builder /app/schema.sql .
COPY --from=builder /app/admin.html .
COPY --from=builder /app/data ./data

# Port par défaut
EXPOSE 8080

CMD ["./cheapdish-backend"]