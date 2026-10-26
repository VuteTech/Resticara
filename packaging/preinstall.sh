#!/bin/sh
# Before resticara 0.3, the deb package did not mark /etc/resticara/config.ini and
# the templates as configuration files. dpkg would then ask what to do with them on
# the upgrade, which fails without a terminal (unattended upgrades). Keep the local
# config and changed templates aside until postinstall puts them back, and let dpkg
# replace templates that are exactly as an older release shipped them.
set -e

[ "$1" = upgrade ] || exit 0
command -v dpkg-query >/dev/null 2>&1 || exit 0
if dpkg-query -W -f='${Conffiles}\n' resticara 2>/dev/null | grep -q ' /etc/resticara/config.ini '; then
	exit 0
fi

# sha256 sums of every template version shipped before they became configuration files
shipped="
  0a9701a4acb5c083a11532601c2dc02348fac56aa7a91f88b3b1bba17dde9c91
  0f07f3dbecd9b0c5582bb44bad200feec6b9d62655664780f82ce6181b987d63
  108393ce32086d19c24e9b66b05eda883b511ba1568635771547897f89ce0d79
  1a88618b5ffa780745c15fc00d8586030e4540523c2bc0b653b9cb0fa56bcbde
  308b4431655754b08fe529fa61401b05a34c79bc27cd6cdc13aff72b34ba647e
  484f35bdf2abcaf9e803042ac48e257e3bfbfa183b65bf77f3022a1c39bd8315
  4bc9fbb5004c5a9bf840068ae4ffbb5d545a4724c3d996a27a90e73402d20f32
  5417c7f45ba2b9ebc586c7218cd07170bdfcb540db9ff45f89b45a8ab5275f29
  9141144b4aa974a72deea7d9e4d41824c87b8bd566fe36fd1a26b764ffaa509e
  a4820563762bcfd8c191e04967915e6e6e36a4ada257d8c879ce9e550647c696
  bf28b6ab79f8510cb3a37b81785587db39ded910703bd4aba5f4644856e9a15e
  d1859173980a6c7e2b387bc9268f5312dc1cf85c0824d7304cf83fe61de69cd2
  effe5b44c21e00b8bd66c80a8c976fc1154ee3ae748331a31f260c60cbbb1912
"

for file in /etc/resticara/config.ini /etc/resticara/templates/*; do
	[ -f "$file" ] || continue
	case "$file" in
	*/templates/*)
		sum=$(sha256sum "$file" | cut -d' ' -f1)
		case "$shipped" in
		*"$sum"*)
			rm -f "$file"
			continue
			;;
		esac
		;;
	esac
	mv "$file" "$file.resticara-upgrade"
done
exit 0
