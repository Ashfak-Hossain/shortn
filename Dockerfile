ARG SERVICE=api

FROM golang:1.26-alpine AS builder
WORKDIR /app

# Download deps in their own layer so it stays cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# ARGs before the first FROM aren't visible inside a stage unless re-declared.
ARG SERVICE
# CGO_ENABLED=0 produces a fully static binary (no libc), so it runs on a base
# image that has no libc.
RUN CGO_ENABLED=0 GOOS=linux go build -o /server ./cmd/${SERVICE}

# Final image: distroless static — CA certs and tzdata, but no shell or package
# manager. :nonroot runs as unprivileged UID 65532.
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /server /server
EXPOSE 8080
ENTRYPOINT ["/server"]