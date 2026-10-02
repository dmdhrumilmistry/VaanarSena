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
test/dev/start.sh          # dev server on https://localhost:18443 with PostgreSQL in Docker
bash test/dev/seed.sh      # sample devices
```

The console is plain JavaScript in `internal/web/static`, with no build step;
the dev server serves it from disk. See [docs/testing.md](docs/testing.md) for
every test layer and [CLAUDE.md](CLAUDE.md) for project conventions and the
pre-commit checklist.

## Commit style

Imperative subject line, under 72 characters, with a body explaining why.
