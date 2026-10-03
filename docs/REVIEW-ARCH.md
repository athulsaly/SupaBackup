APPROVED (with minor direct edits)

1. I aligned the YAML configuration keys (`url_env`, `webhook_url_env`) in `ARCHITECTURE.md` to match the `PRD.md` format.
2. I updated the required Alpine packages to `postgresql17-client` (for pg_dump >= 17) and `mariadb-client`.
3. I added a note to verify that the MariaDB client's dump works correctly against MySQL 8.
4. I verified `PUID` and `PGID` default to 568 (TrueNAS apps user/group) and updated the wording in `ARCHITECTURE.md` to explicitly state this default.
5. I verified that secrets will exclusively be loaded from environment variables or `*_FILE` equivalents.
6. I confirmed the design includes a Docker `HEALTHCHECK` backed by a status file/JSON.
