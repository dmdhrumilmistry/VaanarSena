# Contributing

Thanks for helping. A few ground rules keep the project healthy:

- **Original code only.** Implement from the public specifications (Apple
  Device Management, MS-MDE2, MS-MDM, Android Management API, Admin SDK). Do
  not copy code from other MDM projects, even permissively licensed ones,
  without discussing it in an issue first.
- **Security first.** Anything touching authentication, device identity or
  the BYOD guard needs tests. Never weaken a personal-device restriction
  without an issue explaining why.
- **Small pull requests** with a clear description and `make test` passing.

## Development

```bash
docker run -d --name vs-pg -e POSTGRES_USER=vaanarsena -e POSTGRES_PASSWORD=vaanarsena \
  -e POSTGRES_DB=vaanarsena -p 5432:5432 postgres:17-alpine
export VS_SECRET_KEY=$(openssl rand -hex 32)
export VS_BOOTSTRAP_ADMIN_EMAIL=admin@example.com VS_BOOTSTRAP_ADMIN_PASSWORD=change-me-please
go run ./cmd/vaanarsena serve
```

The console is plain JavaScript in `internal/web/static`, with no build step.

## Commit style

Imperative subject line, under 72 characters, with a body explaining why.
