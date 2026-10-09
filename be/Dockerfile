# Étape 1 : Compilation du binaire Go avec CGO pour SQLite
FROM golang:1.22-alpine AS builder

# Installation des outils nécessaires pour compiler SQLite (mattn/go-sqlite3 utilise CGO)
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Copie et téléchargement des dépendances
COPY go.mod go.sum ./
RUN go mod download

# Copie du reste du code source du backend
COPY . .

# Compilation statique du serveur Go en activant CGO
RUN CGO_ENABLED=1 GOOS=linux go build -o cheapdish-backend .

# Étape 2 : Image d'exécution légère
FROM alpine:latest
RUN apk add --no-cache sqlite ca-certificates

WORKDIR /app

# Récupération du binaire et des fichiers requis depuis l'étape de build
COPY --from=builder /app/cheapdish-backend .
COPY --from=builder /app/schema.sql .
COPY --from=builder /app/admin.html .
COPY --from=builder /app/data ./data

# Exposition du port de votre API (8080)
EXPOSE 8080

# Démarrage du serveur backend
CMD ["./cheapdish-backend"]
