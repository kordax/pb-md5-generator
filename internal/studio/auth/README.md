# Studio authentication

`Open(Config)` owns one private state directory until `Close`. The parent studio
server mounts `Handler()` below `/_docs/api/v1` and uses `Resolve`, `Require` and
`Can` for its own routes. The handler expects `/session`, `/users` and
`/users/{id}` after that prefix has been stripped.

Create the first administrator with `Bootstrap(ctx, username, displayName,
passwordBytes)` while the studio server is stopped. The CLI reads the password
from stdin. There is no default account, default password or unauthenticated
bootstrap HTTP endpoint. A second bootstrap attempt fails once any user exists.

## Sessions and requests

Production uses an HTTPS public URL. Cookies are always Secure, HttpOnly,
SameSite Strict and host-only, with `Path=/`. HTTPS uses the
`__Host-docs_session` name. Local HTTP development accepts only the literal
`localhost` hostname and uses `docs_session`; it relies on the documented
localhost Secure-cookie exception. Use HTTPS for loopback IP addresses and all
other hosts. The real localhost flow is tested with libcurl, and normal cookie
jar behavior is tested over a verified local TLS connection. Browser
compatibility still requires browser verification.

`GET /session` returns either `{ "user": null }` or a user and `csrfToken`.
An empty user store also returns `setupRequired: true`. Login accepts JSON
`username` and `password`; logout deletes the current server session. The raw
session cookie never appears in response JSON, request URLs or logs.

Authenticated mutations require the configured public origin, a matching request
host and `X-CSRF-Token`. The token is derived from the session cookie and a stored
server key, so separate tabs and process restarts keep the same token. JSON is
bounded and rejects malformed UTF-8, unknown fields, duplicate or differently
cased aliases, invalid Unicode escapes and trailing values. No CORS allowance is
installed. Reverse proxies must preserve the public Host header; forwarding
headers are not trusted for origin checks or login rate limits.

## Roles and document scopes

Administrators manage users. Editors can read and write content; viewers can
read. `Can` applies optional `Grants.Read` and `Grants.Write` project-relative path
prefixes independently, with segment boundaries. A nil grants object means role
defaults. An explicit object with empty lists denies every content path, including
for administrators. `.` grants the project root and its descendants. Grants never
add a capability that the role lacks.

`Require` handles authentication, minimum role, origin and CSRF checks. The parent
must also call `Can` for the actual content path. This package does not protect
published HTML, search indexes, navigation or exported models automatically. The
parent reader and publication design must enforce those boundaries consistently.

Password, role, disabled-state and grants changes revoke the user's sessions.
The last enabled administrator cannot be disabled, demoted or deleted.

## Persistence and resource bounds

The state directory is created with mode 0700 and must already be private when it
exists on Unix, with ownership matching the process's effective user. State
snapshots and lock files are created with mode 0600; their ownership is checked. Windows
uses an exclusive file handle; operators must give its state directory a private
NTFS ACL because Unix mode bits do not enforce Windows ACLs. Linux and Windows
lock ownership is released by the OS when the process exits. A second process
cannot open the same state directory concurrently.

A new snapshot is synced and atomically renamed before in-memory authority
changes. Invalid or corrupt state fails startup without resetting credentials.
Back up the private state directory as sensitive data. This is a single-process
snapshot store, not a clustered authentication database or a guarantee against
storage hardware failure.

Passwords use PBKDF2-HMAC-SHA-256, random 32-byte salts, at least 600,000 iterations
and constant-time digest comparisons. Production has no KDF-cost override.
Passwords need at least 10 Unicode characters and at most 1024 UTF-8 bytes. They
are not trimmed or truncated. A small local common-value blocklist rejects known
weak values; it is not a complete breached-password database. There is no MFA or
external identity provider yet.

At most two KDF operations run concurrently. Login attempts are capped at five
per account, thirty per client IP and sixty globally per minute; raw account
names are not retained in the limiter. Unknown users still run a dummy KDF.
Defaults are 128 users, 1024 sessions and a 24-hour session lifetime. Configured
upper bounds are 256 users, 4096 sessions and thirty days. Each user retains at
most sixteen sessions. Expired sessions are pruned when a new login is committed.
The JSON snapshot is capped at 8 MiB and shares structural JSON limits, so the
individual upper bounds need not all fit simultaneously with maximum grants.
Only session hashes are stored; raw session cookies are not persisted.
