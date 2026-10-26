![Resticara logo](https://repository-images.githubusercontent.com/683147638/770302ee-0cd8-4394-a039-7250d003a0a0)
# Resticara

## Overview
Resticara is a wrapper around [Restic](https://restic.net/), designed to simplify the deployment of Restic for straightforward tasks like website, maildirs, or SQL database backups. Resticara aims to make the backup process less tedious, more streamlined, and more flexible, right out of the box.

## [Blog post](https://petrovs.info/post/2023-09-11-resticara/)

## Features
* Restic Wrapper: Utilizes the proven, fast, and secure backup program Restic.
* Simplified Configuration: Uses config.ini for easy setup and configuration.
* Syslog Integration: Logging to syslog is enabled by default for better traceability.
* Email Notifications: Can be configured to send emails upon backup completion or failure.
* Matrix Notifications: Send backup status messages to a Matrix room.
* Telegram Notifications: Send backup status messages to a Telegram chat via bot.
* Single Binary: Written in Go, Resticara is distributed as a single binary, making it extremely easy to deploy.
 * Systemd Timer Generation: Create and activate systemd timers with `resticara gentimer`.

## Installation [WIP]
You can download the latest version of Resticara from the [Releases](https://github.com/VuteTech/Resticara/releases) page. Available as a `zip` file, `deb` or `rpm` packages for easy installation on various systems.

## Configuration
The configuration is done through `config.ini` file placed in `/etc/resticara/` . Check out the `config.ini-dist` file in the repository for an example configuration.

Every backup is a section named `type:name`:

* `[dir:name]` backs up directories. Repeat `directory =` for each directory; paths may contain spaces.
* `[mysql:name]` backs up a `mysqldump`. `database` is a database name or mysqldump selection options such as `--all-databases` or `--databases db1 db2`. The dump always uses `--single-transaction`; add more options with `mysqldump_args`, for example `mysqldump_args = --routines --events` (use `--skip-single-transaction` for MyISAM tables).

Both need `bucket` (the restic repository) and `retention_daily`, `retention_weekly` and `retention_monthly`. Changing the directories of a job starts a new snapshot group in restic, so the old snapshots are no longer removed by the retention rules; remove them once with `restic forget --path`.

### Host name
`hostID` in `[general]` names this machine in reports. When it is set to a name (anything but `hostname`), resticara also passes it to restic as `--host`: snapshots are recorded under that name, and the retention rules only remove this host's snapshots, so several servers can share a repository safely. With `hostID = hostname` restic uses the machine's hostname, as before.

If a machine already has snapshots under its hostname and `hostID` is set to a different name later, the old snapshots are no longer covered by the retention rules. The report then shows a note with the command that assigns them to `hostID` once, for example:

```
restic -r b2:example-bucket:website/ rewrite --new-host server-1 --forget --host old-hostname --path /var/www/example.com
```

### Excluding files
`dir:` jobs can leave files out of the backup. Set these in `[general]` to apply them to every `dir:` job, or in a job section; the values from both are combined:

* `exclude = <pattern>`: one line per pattern, see [restic's exclude patterns](https://restic.readthedocs.io/en/stable/040_backup.html#excluding-files). A pattern without a leading `/` matches at any depth, `**` matches any number of directories and a leading `!` keeps files that an earlier pattern excluded.
* `exclude_file = <path>`: a file with one pattern per line.
* `exclude_caches = true`: skips directories marked with a `CACHEDIR.TAG` file. A job can turn it off again with `exclude_caches = false`.
* `exclude_if_present = <file name>`: skips every directory that contains a file with this name, for example `.nobackup`.

Typical patterns for WordPress sites are `wp-content/cache` and `wp-content/upgrade`. Excluding files keeps the snapshot paths, so retention keeps working on the existing snapshots.

Check a configuration with:

```
resticara validate
```

It lists every error at once, warns about unknown keys (usually typos), notification channels that are enabled but incomplete, and directories that do not exist on this machine. `run`, `prune` and `gentimer` refuse to start when the configuration has errors.

## Requirements
Restic 0.17 or newer is required (MySQL backups use `--stdin-from-command`, so a failed `mysqldump` fails the backup instead of storing a truncated dump).

## Restic credentials
The repository password and storage credentials are read from the environment. For the systemd timers, put them in `restic.env` next to `config.ini` (e.g. `/etc/resticara/restic.env`), readable only by root:

```
RESTIC_PASSWORD=...
B2_ACCOUNT_ID=...
B2_ACCOUNT_KEY=...
```

## Backup status
A run is reported as `Backup successful`, `Backup completed with warnings` (restic saved the snapshot but could not read some files, e.g. mail files moved during the backup) or `BACKUP FAILED!`. Resticara exits with a non-zero code on failure, so systemd marks the unit as failed.

Every report starts with one line per job (`OK`, `WARNING` or `FAILED`, and how long the job took), followed by the restic output. The email subject and the first line of Matrix and Telegram messages name the host and the jobs that need attention, for example `[server-1] BACKUP FAILED: mysql:website`.

To get notifications only when something needs attention, set `notify = problems` in `[general]`; successful runs are then only logged to syslog. The default is `notify = always`.

Customized templates keep working. Besides the existing fields, templates can use `.Subject`, `.Status` (`success`, `warning` or `failure`) and, per entry of `.Commands`, `.Status`, `.StatusLabel` and `.Duration`.

## Pruning repositories
Use the prune command to remove unneeded data from configured restic repositories.

```
resticara prune all                   # prune all repositories
resticara prune b2:bucket:wpsites/    # prune a single repository
```

## Generating systemd timers
Run `resticara gentimer` as root to generate systemd service and timer files that run the `resticara` binary used for the command, writing them to the systemd unit directory. A single daily `resticara-backup.timer` runs all configured backups one after another and sends one report, and each backup gets a prune timer. Existing timers are restarted to pick up changes and any timers without a matching configuration are disabled and removed. Prune timers first run an hour after they are started and then every 30 days by default, or a custom interval can be set with `retention_prune` in the configuration (either globally under `[general]` or per backup).

## TODO
* Webhooks: ability to integrate with various webhooks for enhanced automation.
* Support for more operating systems.
* Better syntax of the Syslog logs.
* Integrated Prometheus exporter
* Option of running tasks for different repositories in parallel
* A website and documentation

## Logging
Resticara logs all its activities to syslog by default, so you can easily monitor its actions and diagnose any potential issues.

## Email Notifications
To set up email notifications, edit the corresponding fields in the `config.ini` file. Port 587 uses STARTTLS, port 465 implicit TLS.

## Matrix and Telegram Notifications
Matrix can log in with `username`/`pass` (the session is logged out after each message) or use an `access_token`. Telegram reports longer than Telegram's 4096 character limit are sent as truncated plain text.

## Packages
The `.deb` and `.rpm` packages install `/etc/resticara/config.ini` readable only by root and treat it and the templates as configuration files, so upgrades keep local changes. Resticara warns when `config.ini` or `restic.env` can be read by other users.

## Development
```
go test ./...                                     # unit tests
go test -tags integration ./test/integration/...  # integration tests against real restic repositories
```
The integration tests build the binary, create local restic repositories in temporary directories and replace `mysqldump` and `systemctl` with stubs. They need restic 0.17 or newer in `PATH` and are skipped without it.

## Contributing
Contributions are welcome! Feel free to open an issue or create a pull request.

## License
Resticara is released under the Gnu GPL v3 License. See LICENSE for more details.
