# Security Policy

## Supported Versions

| Version | Supported | Notes |
| :--- | :--- | :--- |
| 1.3.0 | :white_check_mark: | Current release with hardened local API security |
| < 1.3.0 | :x: | Recommended to upgrade |

---

## Security Model (v1.3.0+)

Zapretify implements defense-in-depth protection for all IPC and local server endpoints:

1. **Header-Based Cryptographic Authentication (`X-Zapretify-Token`)**:
   - At startup, the application generates a cryptographically secure 256-bit random token.
   - All `/api/*` routes strictly require this token via the `X-Zapretify-Token` HTTP header.
   - Query string token authorization (`?token=...`) is rejected for API mutations to prevent token leakage via referrers, web logs, or browser histories.
   - Tokens are validated using `crypto/subtle.ConstantTimeCompare` to eliminate timing side-channel attacks.

2. **Strict Host & Origin Validation**:
   - The HTTP middleware strictly checks the `Host` header against the bound loopback interface (`127.0.0.1:<port>`), eliminating DNS rebinding attacks.
   - The `Origin` header is strictly validated. Opaque or sandboxed origins (`Origin: null`, `file://`, `data:`) are rejected with `403 Forbidden`.
   - Browser `Sec-Fetch-Site` metadata is checked; cross-site requests are rejected.

3. **HTTP Verb & Request Hardening**:
   - State-modifying endpoints (`/api/zapret/start`, `/api/zapret/stop`, `/api/zapret/benchmark`, `/api/config`, `/api/open-url`, `/api/zapret/ipset-mode`, etc.) strictly enforce `POST`.
   - Request bodies are bounded with `http.MaxBytesReader` (1 MB) to prevent denial-of-service memory exhaustion.
   - JSON payload validation clones active configurations before deserialization, preventing zeroing of non-modified fields.

4. **Input Sanitation & Filesystem Protection**:
   - List and filename parameters are restricted to `.txt` files located strictly within the designated `lists/` directory.
   - Path traversal sequences (`..`, `/`, `\`), NTFS Alternate Data Streams (`:`), and Windows DOS reserved device names (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`) are rejected.
   - Strategy identifiers and testing parameters are strictly validated against the internal whitelist and strategy registry before being executed.

5. **Safe URL & Process Execution**:
   - URLs requested via `/api/open-url` are parsed and strictly verified against an allowed host whitelist (`github.com`, `telegram.org`, `t.me`) and permitted schemes (`http`, `https`, `tg`).
   - Browser launches use `windows.ShellExecuteW` directly without passing raw strings into `cmd.exe` or PowerShell shells.
   - Winws child processes are tracked by exact PID (`winws.pid`). Global process termination commands (`taskkill /IM winws.exe`) have been removed to prevent interfering with other DPI-circumvention utilities (GoodbyeDPI, Flowseal, etc.).

---

## Reporting a Vulnerability

If you discover a security vulnerability in Zapretify, please do not disclose it via a public GitHub issue. Instead:
- Use GitHub Private Vulnerability Reporting under the **Security** tab of the repository; or
- Contact the maintainers directly via Telegram or email.

We appreciate responsible disclosure and aim to investigate and address confirmed vulnerabilities promptly.
