#!/bin/sh
# Puts back the files preinstall kept aside, then prints the next steps.
set -e

restored=""
for kept in /etc/resticara/config.ini.resticara-upgrade /etc/resticara/templates/*.resticara-upgrade; do
	[ -f "$kept" ] || continue
	mv "$kept" "${kept%.resticara-upgrade}"
	restored="$restored ${kept%.resticara-upgrade}"
done
if [ -n "$restored" ]; then
	echo "Kept your files:$restored"
	echo "The templates shipped with this version are in /usr/share/doc/resticara/templates/."
fi
chmod 600 /etc/resticara/config.ini 2>/dev/null || true

cat <<'MSG'
resticara is installed. Next steps:
  1. Edit /etc/resticara/config.ini (see /usr/share/doc/resticara/README.md).
  2. Put the restic password and storage credentials in /etc/resticara/restic.env
     and restrict it with: chmod 600 /etc/resticara/restic.env
  3. Check the configuration:   resticara validate
  4. Install the systemd timers: resticara gentimer
After an upgrade, run "resticara gentimer" again to update the timers.
MSG
exit 0
