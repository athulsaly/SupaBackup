NO-GO

1. **Secret Leakage in Process Table** (`internal/engine/postgres.go:27`): `pg_dump` receives the database URL (`job.URL`) as a direct command-line argument. This exposes the plaintext password to `ps aux` and other process monitors. The connection string should be passed via `PGPASSWORD` (or equivalent environment variables) to keep it out of the process arguments.
2. **Missing Supabase Flags** (`internal/engine/postgres.go:27`): `pg_dump` is missing critical flags required for managed databases like Supabase. It must include `--no-owner` and `--no-privileges` to prevent restore errors caused by lack of superuser rights, and it should exclude managed schemas (e.g., `--exclude-schema=supabase_functions`).
3. **Secret Leakage in Logs for MySQL DSNs** (`internal/config/config.go:196`): The `RedactURL` function uses a regex (`://([^:]+):([^@]+)@`) that fails to redact MySQL DSNs (e.g., `user:pass@tcp(...)`). If a connection fails, the plaintext password will not be matched and will be written directly to the logs and the status JSON.


## Round 2 (Commit 406fe12)

**GO**

All NO-GO items have been successfully addressed:
1. **Secret Leakage in Process Table**: Fixed. `internal/engine/postgres.go` now parses the URL and securely passes the password via the `PGPASSWORD` environment variable, avoiding command-line argument exposure.
2. **Missing Supabase Flags**: Addressed. The `exclude_schemas` option was added for job-specific schema exclusion. As noted, `--no-owner` and `--no-privileges` are correctly omitted here because they are ignored by `pg_dump` when `--format=custom` is used (they apply to `pg_restore`).
3. **Secret Leakage in Logs for MySQL DSNs**: Fixed. The redaction logic was updated to correctly catch and redact DSN-style credentials in error logs and status output.

The implementation is now safe and correct. Ready for merge pending CI re-test.


## Dashboard (Commit 3ec0064)

**NO-GO**

The dashboard implementation is solid in its XSS prevention, vanilla JS UI, and password-based disabling mechanism, but contains the following critical security and concurrency issues that block merging:

1. **Missing Secure Flag on Session Cookie** (`internal/web/auth.go:130`): `CreateSessionCookie` sets `HttpOnly` and `SameSite`, but omits `Secure: true`. The session cookie must require HTTPS to prevent network interception of tokens.
2. **Missing CSRF Validation on Logout** (`internal/web/routes.go:82`): The `/logout` route is defined outside the API dispatcher and does not validate the `csrf_token` that the UI sends, making the session vulnerable to cross-site logout.
3. **Data Race in Config Hot-Reload** (`internal/web/api.go:307`): `HandleSaveConfig` completely replaces the `sc.Config` pointer without holding a mutex. Meanwhile, `internal/scheduler/scheduler.go:185` (`RunJob`) reads from `s.config` without locking, creating a concurrent read/write data race that can crash the daemon when a job runs during a reload.
4. **Symlink Path Traversal in Downloads** (`internal/web/api.go:208`): `HandleDownloadBackup` uses `os.Stat(targetPath)` which automatically resolves symlinks. A malicious symlink placed inside the backup directory pointing to a sensitive system file would be served for download. You must use `os.Lstat` or `filepath.EvalSymlinks` to ensure the final absolute path strictly resides within the isolated `jobDir`.


## Dashboard round 2

**GO**

All four critical issues have been successfully addressed:
1. **Secure Cookie:** Added `DetermineCookieSecure` to automatically apply the `Secure` flag on HTTPS or proxy environments, with a `DASHBOARD_COOKIE_SECURE` override.
2. **Logout CSRF:** `POST /logout` now correctly checks and validates the `X-CSRF-Token` against the active session.
3. **Config Data Race:** Scheduler's `RunJob` now snapshots `s.config` under the mutex lock to prevent concurrent data races during hot reloads. `ServerContext` also correctly protects config reassignment with a lock.
4. **Symlink Traversal:** `HandleDownloadBackup` now resolves real paths via `filepath.EvalSymlinks()` and rigorously checks if the resolved path stays within the backup directory.

The dashboard implementation is secure and ready for merge.
