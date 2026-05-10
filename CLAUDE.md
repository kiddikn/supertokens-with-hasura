# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

- Build: `go build -v ./...`
- Vet: `go vet ./...`
- Test all: `go test -v ./...` (no tests currently in the repo; CI runs this on push/PR to `master`)
- Run a single test: `go test -v -run TestName ./domain/`
- Tidy modules: `go mod tidy`
- Run locally (after `direnv allow` to load `.envrc`): `go run .`

CI lives in `.github/workflows/go.yml` and only runs `go build` + `go test` on Go 1.22.

## Architecture

This service is a thin Go HTTP layer that sits between a frontend, **SuperTokens** (authentication), and **Hasura** (GraphQL data store). It has no database of its own — user records live in both SuperTokens and Hasura, and this service keeps them in sync.

### Two HTTP endpoints, everything else is SuperTokens middleware

`main.go` wires `supertokens.Middleware` around an inner mux that only handles two paths:

- `POST /verify` — called by Hasura's JWT/session webhook. Reads the SuperTokens session, looks up the user's role in Hasura, and returns Hasura session claims (`X-Hasura-User-Id`, `X-Hasura-Role`, `X-Hasura-Is-Owner`). This is how Hasura authorizes requests.
- `POST /create` — owner-gated invite flow. Verifies the caller has `owner`/`super` on the target group, signs the new user up in SuperTokens with `FAKE_PASSWORD`, mirrors the user into Hasura, then issues a SuperTokens password-reset link and emails it as the invite.

All other paths (login, reset-password, dashboard, etc.) are handled by SuperTokens' built-in middleware. Public sign-up is explicitly disabled by setting `SignUpPOST = nil` — the only way to create a user is the `/create` invite flow.

### The FAKE_PASSWORD invariant (critical)

`/create` signs the new user up with `cfg.FakePassword` as a placeholder, because SuperTokens requires a password at signup but the real password is set later by the user via the reset link. To prevent anyone from authenticating with that placeholder, three SuperTokens recipe functions are overridden in `main.go`:

- `SignIn` — returns `WrongCredentialsError` if the supplied password equals `FAKE_PASSWORD`.
- `ResetPasswordUsingToken` — returns `ResetPasswordInvalidTokenError` if the new password equals `FAKE_PASSWORD`.
- `UpdateEmailOrPassword` — returns an error if the new password equals `FAKE_PASSWORD`.

When changing auth behavior, preserve these guards or the placeholder password becomes a backdoor.

### Role model

Hasura stores a numeric role per user/user_group (`domain.User=1`, `Owner=2`, `Super=3`). `domain.GetHasuraRole` maps this to the strings Hasura expects (`user`/`owner`/`super`). The `/verify` claims and the `/create` authorization check both go through this mapping — keep the constants and the mapper in sync.

### Hasura access (`domain/domain.go`)

`domain.Hasura` is a `go-graphql-client` wrapper that injects `x-hasura-admin-secret` on every request. All queries/mutations are defined as Go structs with `graphql:"..."` tags; the original GraphQL is kept as a comment above each method for reference. There are four operations: `CreateUser`, `GetUser`, `GetUserByEmail`, `GetUserGroupRole`. `ErrNotFound` is returned by `GetUserByEmail` when the user is missing — `/create` distinguishes "exists in SuperTokens but not in Hasura" (recoverable, finishes the invite) from "exists in both" (409, tell the user to reset their own password).

### Cookies / CORS

Sessions use `SameSite=none`, `Secure=true`, with `CookieDomain` from env. `corsMiddleware` only echoes `Access-Control-Allow-Origin` for the configured `WEB_SITE_DOMAIN` or `HASURA_END_POINT_URL` — adding a new caller means extending that allowlist.

### Configuration

All config comes from environment variables parsed by `caarlos0/env/v11` into the `config` struct in `main.go`; every field except `PORT` is `required,notEmpty`. Local development uses `direnv` (`.envrc` is gitignored). The README lists the required vars.
