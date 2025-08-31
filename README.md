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
* Single Binary: Written in Go, Resticara is distributed as a single binary—making it extremely easy to deploy.
 * Systemd Timer Generation: Create and activate systemd timers with `resticara gentimer`.

## Installation [WIP]
You can download the latest version of Resticara from the [Releases](https://github.com/VuteTech/Resticara/releases) page. Available as a `zip` file, `deb` or `rpm` packages for easy installation on various systems.

## Configuration
The configuration is done through `config.ini` file placed in `/etc/resticara/` . Check out the `config.ini-dist` file in the repository for an example configuration.

## Pruning repositories
Use the prune command to remove unneeded data from configured restic repositories.

```
resticara prune all                   # prune all repositories
resticara prune b2:bucket:wpsites/    # prune a single repository
```

## Generating systemd timers
Run `resticara gentimer` to generate systemd service and timer files for each configured backup, writing them to the systemd unit directory. Existing timers are restarted to pick up changes and any timers without a matching configuration are disabled and removed. Prune timers run every 30 days by default, or a custom interval can be set with `retention_prune` in the configuration (either globally under `[general]` or per backup).

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
To set up email notifications, edit the corresponding fields in the `config.ini` file.

## Contributing
Contributions are welcome! Feel free to open an issue or create a pull request.

## License
Resticara is released under the Gnu GPL v3 License. See LICENSE for more details.
