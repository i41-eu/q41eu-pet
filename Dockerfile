# syntax=docker/dockerfile:1

FROM docker.io/library/golang:1.26.8 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/q41eu-pet ./cmd/q41eu-pet

# Not distroless: the pet's voice needs ffmpeg (the Edge voice) and espeak-ng (the fallback).
FROM docker.io/library/alpine:3.22
RUN apk add --no-cache ffmpeg espeak-ng ca-certificates tzdata
COPY --from=build /out/q41eu-pet /q41eu-pet
EXPOSE 8770
USER 65532:65532
# Mount the robot token at /secrets/robot-token and pass -public-url https://<host>. The root
# file system can be read-only: mount a volume for -state-file (pets, pairings, the leaderboard
# photos next to it) and set XDG_CACHE_HOME to a writable place (the spoken lines, snapshots).
ENTRYPOINT ["/q41eu-pet", "-listen", ":8770", "-token-file", "/secrets/robot-token", "-state-file", ""]
