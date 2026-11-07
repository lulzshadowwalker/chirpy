## Table of contents

- [What I learned](#what-i-learned)
  - [Routing with `http.ServeMux`](#routing-with-httpservemux)
  - [Request context (`context.Context`)](#request-context-contextcontext)
  - [Payload safety with `http.MaxBytesReader`](#payload-safety-with-httpmaxbytesreader)
  - [Webhooks and inbound event handling](#webhooks-and-inbound-event-handling)
  - [Password storage and hashing](#password-storage-and-hashing)
    - [Salts and rainbow tables](#salts-and-rainbow-tables)
    - [Argon2id compared to bcrypt, SHA, and MD5](#argon2id-compared-to-bcrypt-sha-and-md5)
  - [Access tokens compared to refresh tokens](#access-tokens-compared-to-refresh-tokens)

## What I learned

### Routing with `http.ServeMux`

> Pattern syntax
> `[METHOD ][HOST]/[PATH]` (all three parts are optional)

- Fixed URL paths match only the exact path. For example, `/about` matches `/about` and nothing else.

- Subtree paths end with a trailing slash `/`. They match any path sharing that prefix. For example, `/images/` matches `/images/logo.png`. This pattern works well for static file servers and grouped sub-routes.

- The longest match wins when multiple patterns match a request. Specific handlers override generic ones, so `/images/` wins over `/` for `/images/logo.png`.

- Host-specific patterns start with a domain name, such as `api.example.com/`. The server routes these requests by checking the incoming `Host` header.

### Request context (`context.Context`)

- The call `ctx := r.Context()` extracts the context from the request. The context lifetime matches the incoming network connection.

- Client disconnects, closed browser tabs, and network drops cancel `ctx` immediately.

- Pass `ctx` to downstream I/O calls, such as `db.QueryContext(ctx, ...)` and `http.NewRequestWithContext`. The server then stops running operations when the client disconnects.

- PostgreSQL drivers such as `pgx` and `pq` listen for `ctx.Done()`. When canceled, the driver sends an abort signal over the network to stop the query on the database server. This frees up database memory, locks, and CPU time.

### Payload safety with `http.MaxBytesReader`

- Bit shifts paired with `iota` define clean byte multipliers.

```go
const (
    _   = 1 << (10 * iota)
    KiB
    MiB
    GiB
)

const maxBodySize = 4 * MiB
```

- Wrap `r.Body` to stop clients from sending endless payloads into server memory.

```go
r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
```

- When the request payload exceeds the configured byte count, the reader stops reading, sets the connection to close, and returns `*http.MaxBytesError`.

### Webhooks and inbound event handling

- In webhook handlers, you do not design the endpoint schema. You write the handler to match the payload shape sent by the external service, such as Stripe.

- Webhook handlers must be idempotent. External services retry requests when network calls fail or time out. You can prevent duplicate runs by recording processed event IDs in a database table.

- Return a `2xx` HTTP status code only after the handler finishes processing the payload. External services treat any `2xx` code as a success and stop retrying that event.

### Password storage and hashing

#### Salts and rainbow tables

- Encryption is reversible. Anyone who gets the database and the decryption key can read every password in plain text. Hashing cannot be reversed.

- A salt is a unique, random string generated for each user. The server appends the salt to the password before hashing, then stores the salt in plain text next to the hash.

- Salts prevent duplicate hashes. If two users pick the same password, their hashes will differ.

- Salts prevent rainbow table attacks. Attackers cannot use precomputed tables of common hashes because every user requires a separate table generated with that user's salt.

- Libraries such as Argon2id and bcrypt store the salt, algorithm version, and cost factors inside the output string.

#### Argon2id compared to bcrypt, SHA, and MD5

- Password hashing requires slow algorithms that consume memory. Fast hashes let GPUs run billions of guesses per second against leaked databases.

```text
SHA-256 and MD5 (Unsafe for passwords):
- Designed for speed. Attackers can test billions of guesses per second on modern GPUs.
- Lack built-in salting and parameter configuration. MD5 also produces hash collisions.

bcrypt (Usable fallback):
- Uses configurable CPU work factors, but remains vulnerable to custom ASIC and FPGA hardware.
- Truncates input at 72 bytes. Any byte past 72 is ignored or triggers an error, which UTF-8 characters exhaust quickly.
- Embedded NUL bytes (\0) cause early string termination in older implementations.

Argon2id (Current standard):
- Enforces high RAM use per attempt (typically 19 to 64 MiB), which limits GPU and ASIC parallel guessing.
- Provides three configuration knobs: memory (m), iterations (t), and parallelism (p).
- Combines Argon2d for GPU resistance and Argon2i for side-channel resistance.
- Imposes no input length limits.
- Encodes all parameters directly into the output:
  $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
```

### Access tokens compared to refresh tokens

- A JSON Web Token (JWT) is a signed JSON payload. The server checks the cryptographic signature to verify the data without making a database query.

|  | Access tokens (JWT) | Refresh tokens |
| --- | --- | --- |
| state | stateless, verified by signature | stateful, stored in a database |
| lifespan | short, typically 15 minutes to 24 hours | long, typically 24 hours to 60 days |
| revocation | active until the expiration time | revocable immediately by deleting or updating the database record |
