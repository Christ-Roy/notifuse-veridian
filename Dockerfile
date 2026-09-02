# Stage 1: Build the React frontend
FROM node:22-alpine AS console-frontend-builder

# Set working directory for the frontend
WORKDIR /build/console

# Copy frontend package files
COPY console/package*.json ./

# Install dependencies with retry settings for network resilience
RUN npm config set fetch-retries 5 && \
    npm config set fetch-retry-mintimeout 20000 && \
    npm config set fetch-retry-maxtimeout 120000 && \
    npm ci

# Copy frontend source code
COPY console/ ./

# Build frontend in production mode
RUN npm run build

# Stage 2: Build the notification center frontend
FROM node:22-alpine AS notification-center-builder

# Set working directory for the notification center
WORKDIR /build/notification_center

# Copy notification center package files
COPY notification_center/package*.json ./

# Install dependencies with retry settings for network resilience
RUN npm config set fetch-retries 5 && \
    npm config set fetch-retry-mintimeout 20000 && \
    npm config set fetch-retry-maxtimeout 120000 && \
    npm ci

# Copy notification center source code
COPY notification_center/ ./

# Build notification center in production mode
RUN npm run build

# Stage 3: Build the Go binary (pure Go, no CGO needed)
FROM golang:1.26.6-alpine AS backend-builder

# Set working directory
WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY cmd/ cmd/
COPY config/ config/
COPY internal/ internal/
COPY pkg/ pkg/

# Build the application with CGO disabled (pure Go)
ENV CGO_ENABLED=0
ENV GOOS=linux

# === Veridian patch === Build-time metadata injectee dans internal/buildinfo
# pour /api/version. Permet a la CI de verifier qu'un redeploy a effectivement
# remplace le container (defense anti-faux-positif documente 2026-05-18).
# Defaults "dev" quand le build se fait sans ARG (run local docker build sans
# --build-arg). Le job build du workflow GH Actions passe les 3 ARGs.
ARG BUILD_TAG=dev
ARG BUILD_SHA=dev
ARG BUILD_DATE=dev

RUN go build \
  -ldflags="-s -w \
    -X github.com/Notifuse/notifuse/internal/buildinfo.Tag=${BUILD_TAG} \
    -X github.com/Notifuse/notifuse/internal/buildinfo.GitSHA=${BUILD_SHA} \
    -X github.com/Notifuse/notifuse/internal/buildinfo.BuildDate=${BUILD_DATE}" \
  -o /tmp/server ./cmd/api

# Stage 4: Create the runtime container (Alpine for smaller image)
# === Veridian patch === bump 3.19 → 3.21 : alpine 3.19 EOL depuis 2025-11-01,
# Trivy bloque via Constitution CI §13 (exit-on-eol). Alpine 3.21 supporté
# jusqu'au 2026-11-01. Pas de pkg apk version-specific dans cette image.
FROM alpine:3.21 AS runtime

# Add necessary runtime packages
# === Veridian patch === apk upgrade en amont pour récupérer les CVE patches
# OS (ex: libpq 17.9→17.10 CVE-2026-6638 SQL injection). Sans upgrade, l'index
# Alpine cache une version antérieure même si le repo a déjà le fix.
#
# === Veridian patch 2026-09-02 === le stage est NOMMÉ `runtime` pour que la CI
# puisse l'exclure du cache de couches (`no-cache-filters: runtime`). `--no-cache`
# est une option d'apk : elle ne désactive PAS le cache buildkit. Mesuré sur le
# run 33254429451 : `#24 [stage-3 2/7] RUN apk upgrade … CACHED`, donc la couche
# n'était pas exécutée et l'image rejouait un jeu de paquets figé (libpq 17.10-r0)
# alors que le miroir servait le correctif (17.11-r0). Le gate Trivy refusait
# alors de publier l'image qui aurait corrigé les CVE qu'il signalait.
# Ne PAS retirer le nom du stage sans retirer aussi `no-cache-filters` du workflow.
RUN apk upgrade --no-cache && \
    apk add --no-cache \
    ca-certificates \
    tzdata \
    postgresql-client

# Create application directory structure
WORKDIR /app
RUN mkdir -p /app/console/dist /app/notification_center/dist /app/data

# Copy the binary from the builder stage
COPY --from=backend-builder /tmp/server /app/server

# Copy the built console files
COPY --from=console-frontend-builder /build/console/dist/ /app/console/dist/

# Copy the built notification center files
COPY --from=notification-center-builder /build/notification_center/dist/ /app/notification_center/dist/

# Expose the application ports
EXPOSE 8080
EXPOSE 587

# Run the application
CMD ["/app/server"] 
