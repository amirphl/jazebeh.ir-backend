# HTTP Middleware

This package contains the Fiber v3 authentication, admin authorization, and Prometheus HTTP metrics middleware used by `app/router/routes.go`.

## Authentication Contexts

The backend has separate JWT validators and claims for each principal type:

| Principal | Middleware | Context ID | Claims type |
|---|---|---|---|
| Customer | `Authenticate()` | `customer_id` | `*services.TokenClaims` |
| Admin | `AdminAuthenticate()` | `admin_id` | `*services.AdminTokenClaims` |
| Bot | `BotAuthenticate()` | `bot_id` | `*services.BotTokenClaims` |

All three also set `token_id` and `token_claims`. If the request contains `X-Request-ID`, they copy it to `request_id`. The router's request-ID middleware normally creates that header before authentication runs.

Tokens must use this header format:

```http
Authorization: Bearer <access-token>
```

Customer, admin, and bot tokens are not interchangeable; each validator checks its own claim type.

## Construction

`AuthMiddleware` depends on the `services.TokenService` interface. The production application builds it from the JWT configuration in `main.go`.

```go
tokenService, err := services.NewTokenService(
    15*time.Minute,
    7*24*time.Hour,
    "yamata-no-orochi",
    "yamata-api",
    false,                 // use RSA keys
    "",                    // private key PEM
    "",                    // public key PEM
    "replace-with-secret", // HMAC secret
)
if err != nil {
    return err
}

auth := middleware.NewAuthMiddleware(tokenService)
```

For RSA signing, pass `true` and provide the private/public PEM values. Environment-backed construction uses the `JWT_*` settings documented in `env.template`.

## Protecting Routes

Apply the middleware to a group when every endpoint has the same principal type:

```go
customerRoutes := app.Group("/api/v1/campaigns")
customerRoutes.Use(auth.Authenticate())
customerRoutes.Get("/", listCampaigns)

botRoutes := app.Group("/api/v1/bot/campaigns")
botRoutes.Use(auth.BotAuthenticate())
botRoutes.Use(middleware.RequireBotAuth)
botRoutes.Get("/ready", listReadyCampaigns)
```

Admin routes additionally use the authorization middleware:

```go
authz := middleware.NewAuthorizationMiddleware(adminRepository)

adminRoutes := app.Group("/api/v1/admin/campaigns")
adminRoutes.Use(auth.AdminAuthenticate())
adminRoutes.Use(middleware.RequireAdminAuth)
adminRoutes.Use(authz.AdminAuthorize())
adminRoutes.Get("/", listCampaigns)
```

`AdminAuthorize()` resolves the method and route through `app/authorization/registry.go`, loads the admin, rejects inactive admins, and checks the required permission. An unmapped protected route fails closed with `PERMISSION_NOT_MAPPED`.

## Optional Customer Authentication

`OptionalAuth()` attempts customer-token validation only when a valid Bearer header is present. Missing, malformed, empty, expired, revoked, or invalid tokens are ignored and the request continues unauthenticated. Use it only when the handler is designed for both public and authenticated callers.

```go
app.Get("/public-or-personalized", auth.OptionalAuth(), handler)
```

Do not use `OptionalAuth()` as protection for a private route.

## Reading Context in Handlers

Use the helpers instead of reading `Locals` directly when only the principal ID is needed:

```go
func profile(c fiber.Ctx) error {
    customerID, ok := middleware.GetCustomerIDFromContext(c)
    if !ok {
        return c.SendStatus(fiber.StatusUnauthorized)
    }

    claims, _ := middleware.GetTokenClaimsFromContext(c)
    return c.JSON(fiber.Map{
        "customer_id": customerID,
        "token_id":    claims.TokenID,
    })
}
```

Available helpers:

- `GetCustomerIDFromContext(c) (uint, bool)`
- `GetAdminIDFromContext(c) (uint, bool)`
- `GetBotIDFromContext(c) (uint, bool)`
- `GetTokenClaimsFromContext(c) (*services.TokenClaims, bool)` for customer claims
- `RequireAuth(c) error`
- `RequireAdminAuth(c) error`
- `RequireBotAuth(c) error`

Admin and bot claim objects remain available under `c.Locals("token_claims")` with their respective concrete types.

## Authentication Errors

Authentication failures return HTTP `401` in the shared `dto.APIResponse` envelope. Current codes are:

| Code | Cause |
|---|---|
| `MISSING_AUTHORIZATION_HEADER` | No `Authorization` header |
| `INVALID_AUTHORIZATION_FORMAT` | Header does not start with `Bearer ` |
| `MISSING_ACCESS_TOKEN` | Bearer value is empty |
| `TOKEN_EXPIRED` | Access token has expired |
| `TOKEN_INVALID` | Signature, claims, or token type is invalid |
| `TOKEN_REVOKED` | Token ID is in the in-process revocation set |
| `TOKEN_VALIDATION_FAILED` | Other validation error |

The `Require*` helpers return principal-specific missing/invalid ID codes. Admin authorization returns `401`, `403`, or `500` depending on missing identity, permission/inactive state, or repository failure.

## Service-Account Permission Header

There is intentionally no header-based service-account authorization middleware. A caller-controlled permissions header is not an identity. Any future service-to-service route must first authenticate a service identity (for example, mTLS or a signed, audience-bound service token) and derive permissions from that identity.

## Prometheus Metrics

`Metrics()` records:

- `http_requests_total{method,route,status}`
- `http_request_duration_seconds{method,route,status}`
- `http_inflight_requests`

It prefers the matched Fiber route template for the `route` label to avoid high-cardinality path labels. Register it as application middleware:

```go
app.Use(middleware.Metrics())
```

The Prometheus endpoint itself is served by the separate metrics server started in `main.go`, not by this package.

## Tests and Security Notes

Run the package tests with:

```bash
go test ./app/middleware/...
```

The current token revocation store is process-local memory, so revocations are not shared across replicas and are lost on restart. Production traffic should use HTTPS, authentication routes should remain rate-limited, JWT keys/secrets must stay outside source control, and permission-protected admin routes must be registered in the central authorization registry.
