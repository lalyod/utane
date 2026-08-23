FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/utane .

FROM alpine:3.22

WORKDIR /app

RUN apk add --no-cache ca-certificates

COPY --from=build /out/utane ./utane
COPY web ./web
COPY migrations ./migrations

EXPOSE 8080
CMD ["./utane", "serve"]
