# --- Build stage ---------------------------------------------------------
FROM golang:1.26 AS build
WORKDIR /src

# Cache deps first.
COPY go.mod go.sum ./
RUN go mod download

# Build a static binary so it runs on distroless.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# --- Runtime stage -------------------------------------------------------
# Distroless: no shell, no package manager — minimal attack surface. The
# nonroot variant runs as an unprivileged user.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

# The server reads agent-card.json from the working directory at startup.
COPY --from=build /out/server /app/server
COPY agent-card.json /app/agent-card.json

# Cloud Run sets $PORT; main.go defaults to 8081 locally.
ENV PORT=8081
EXPOSE 8081
USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
