# Utane

Private music library and player written in Go.

## Run

```sh
docker compose up --build
```

The app listens on `http://localhost:8080`.

## Test

```sh
go test ./...
node --test web/static/*.test.mjs
```
