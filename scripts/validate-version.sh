#!/bin/sh
set -eu

version=${1:-}
mode=${2:-any}

usage() {
	echo "usage: scripts/validate-version.sh YY.MM.DD.NN[-Release] [any|release]" >&2
	exit 2
}

if [ "$version" = "" ]; then
	usage
fi

case "$mode" in
any|release)
	;;
*)
	usage
	;;
esac

if ! printf '%s\n' "$version" | grep -Eq '^[0-9]{2}\.[0-9]{2}\.[0-9]{2}\.[0-9]{2}(-Release)?$'; then
	echo "invalid version: $version" >&2
	echo "expected YY.MM.DD.NN or YY.MM.DD.NN-Release" >&2
	exit 1
fi

if [ "$mode" = "release" ]; then
	case "$version" in
	*-Release)
		;;
	*)
		echo "release version must end with -Release: $version" >&2
		exit 1
		;;
	esac
fi
