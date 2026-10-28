#!/bin/sh
# Installs agent-web and starts it on 127.0.0.1 at boot (Linux: systemd user
# service with lingering) or at login (macOS: launchd agent).
#
#   curl -fsSL https://github.com/jclab-joseph/agent-web/releases/latest/download/install.sh | sh
#   curl -fsSL https://github.com/jclab-joseph/agent-web/releases/download/v0.1.0/install.sh | sh -s -- --port 9000
#
# The copy attached to a release installs that release. The copy in the
# repository installs the latest release. Run with --help for the options.
#
# The service runs as you, not as root: sessions use your Claude Code / Codex
# login and your files. Rerunning the script upgrades in place.

set -eu

REPO="jclab-joseph/agent-web"
# Replaced with the release tag when the release is built.
RELEASE_VERSION="@VERSION@"

NAME="agent-web"
LAUNCHD_LABEL="io.github.jclab-joseph.agent-web"
DEFAULT_PORT=8787

say() { printf '%s\n' "$*" >&2; }
info() { printf '\033[1m==>\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[33mwarning:\033[0m %s\n' "$*" >&2; }
die() {
	printf '\033[31merror:\033[0m %s\n' "$*" >&2
	exit 1
}

usage() {
	default_version=latest
	[ "$RELEASE_VERSION" = "@VERSION@" ] || default_version="$RELEASE_VERSION"
	cat >&2 <<EOF
Install agent-web and run it on 127.0.0.1 in the background.

Usage: install.sh [options]

Options:
  --version <tag>   Release to install (default: ${default_version})
  --port <port>     Port on 127.0.0.1 (default: ${DEFAULT_PORT}, or the port of an existing install)
  --bin-dir <dir>   Where to put the binary (default: ~/.local/bin)
  --no-service      Only install the binary; do not start it at boot
  --uninstall       Stop and remove the service and the binary (keeps your sessions)
  -h, --help        Show this help

Environment: AGENT_WEB_VERSION, AGENT_WEB_PORT, AGENT_WEB_BIN_DIR work like the options.
AGENT_WEB_DOWNLOAD_BASE downloads the release files from a mirror instead of GitHub.
EOF
}

# --- platform -----------------------------------------------------------------

detect_platform() {
	case "$(uname -s)" in
	Linux) OS=linux ;;
	Darwin) OS=darwin ;;
	*) die "unsupported OS: $(uname -s) (agent-web supports Linux and macOS)" ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	*) die "unsupported CPU: $(uname -m) (agent-web supports amd64 and arm64)" ;;
	esac
	# A shell running under Rosetta reports x86_64 on Apple silicon.
	if [ "$OS" = darwin ] && [ "$ARCH" = amd64 ] &&
		[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
		ARCH=arm64
	fi
}

# --- downloads ------------------------------------------------------------------

fetch() { # url dest
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "curl or wget is required"
	fi
}

fetch_stdout() { # url
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 "$1"
	else
		wget -q -O - "$1"
	fi
}

resolve_version() {
	if [ -n "${VERSION:-}" ]; then
		return
	fi
	if [ "$RELEASE_VERSION" != "@VERSION@" ]; then
		VERSION="$RELEASE_VERSION"
		return
	fi
	info "Looking up the latest release"
	VERSION="$(fetch_stdout "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null |
		sed -n 's/^[[:space:]]*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)" || true
	[ -n "$VERSION" ] || die "could not find the latest release; pass --version <tag>"
}

# download_asset saves one file of the release.
download_asset() { # name dest
	base="${AGENT_WEB_DOWNLOAD_BASE:-https://github.com/${REPO}/releases/download/${VERSION}}"
	fetch "${base%/}/$1" "$2"
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "sha256sum or shasum is required to verify the download"
	fi
}

install_binary() {
	archive="${NAME}_${VERSION}_${OS}_${ARCH}"
	TMP="$(mktemp -d)"
	trap 'rm -rf "$TMP"' EXIT INT TERM

	info "Downloading ${archive}.tar.gz"
	download_asset "${archive}.tar.gz" "${TMP}/${archive}.tar.gz" ||
		die "could not download ${archive}.tar.gz from release ${VERSION} of ${REPO} (does the release exist?)"
	download_asset checksums.txt "${TMP}/checksums.txt" || die "could not download checksums.txt"

	want="$(grep " ${archive}.tar.gz\$" "${TMP}/checksums.txt" | cut -d' ' -f1)"
	[ -n "$want" ] || die "${archive}.tar.gz is not listed in checksums.txt"
	got="$(sha256_of "${TMP}/${archive}.tar.gz")"
	[ "$want" = "$got" ] || die "checksum mismatch for ${archive}.tar.gz (expected ${want}, got ${got})"

	tar -xzf "${TMP}/${archive}.tar.gz" -C "$TMP"
	[ -f "${TMP}/${archive}/${NAME}" ] || die "the archive does not contain ${NAME}"

	mkdir -p "$BIN_DIR"
	# Rename over the old binary, so a running service is not disturbed
	# until it is restarted.
	cp "${TMP}/${archive}/${NAME}" "${BIN_DIR}/.${NAME}.new"
	chmod 755 "${BIN_DIR}/.${NAME}.new"
	mv -f "${BIN_DIR}/.${NAME}.new" "$BIN"
	info "Installed $("$BIN" --version)"
	info "  -> ${BIN}"
}

# --- health ---------------------------------------------------------------------

# probe prints the /api/info response on the port, or nothing.
probe() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsS --max-time 2 "http://127.0.0.1:${PORT}/api/info" 2>/dev/null || true
	else
		wget -q -T 2 -O - "http://127.0.0.1:${PORT}/api/info" 2>/dev/null || true
	fi
}

port_in_use() {
	if command -v curl >/dev/null 2>&1; then
		curl -s -o /dev/null --max-time 2 "http://127.0.0.1:${PORT}/" 2>/dev/null
	else
		wget -q -T 2 -O /dev/null "http://127.0.0.1:${PORT}/" 2>/dev/null
	fi
}

# check_port fails early when something other than agent-web holds the port.
check_port() {
	if port_in_use && ! probe | grep -q '"authRequired"'; then
		die "127.0.0.1:${PORT} is already used by another program; choose another port with --port"
	fi
}

wait_healthy() {
	i=0
	while [ $i -lt 30 ]; do
		if probe | grep -q "\"version\":\"${VERSION}\""; then
			info "agent-web ${VERSION} is running: http://127.0.0.1:${PORT}"
			return 0
		fi
		sleep 0.5
		i=$((i + 1))
	done
	warn "agent-web did not answer on 127.0.0.1:${PORT} yet. Check the logs: ${LOG_HINT}"
	return 1
}

# The service gets the PATH of the shell running the installer, so it finds
# claude, codex, node and git the same way you do.
service_path() {
	printf '%s' "$PATH"
}

# previous_port prints the port an existing install listens on.
previous_port() {
	f="$1"
	[ -f "$f" ] || return 0
	sed -n 's/.*127\.0\.0\.1:\([0-9][0-9]*\).*/\1/p' "$f" | head -n 1
}

# --- Linux: systemd user service ---------------------------------------------------

systemd_dir() { printf '%s/systemd/user' "${XDG_CONFIG_HOME:-$HOME/.config}"; }

systemd_user_ok() {
	command -v systemctl >/dev/null 2>&1 || return 1
	# `curl | sh` over su or some ssh setups leaves this unset.
	if [ -z "${XDG_RUNTIME_DIR:-}" ] && [ -d "/run/user/$(id -u)" ]; then
		XDG_RUNTIME_DIR="/run/user/$(id -u)"
		export XDG_RUNTIME_DIR
	fi
	systemctl --user show-environment >/dev/null 2>&1
}

linux_service() {
	unit="$(systemd_dir)/${NAME}.service"
	envfile="${XDG_CONFIG_HOME:-$HOME/.config}/${NAME}/service.env"
	LOG_HINT="journalctl --user -u ${NAME} -e"
	[ -n "$PORT" ] || PORT="$(previous_port "$unit")"
	[ -n "$PORT" ] || PORT=$DEFAULT_PORT
	if ! systemd_user_ok; then
		warn "systemd user services are not available here, so agent-web will not start at boot."
		say "  Start it yourself: ${BIN} --addr 127.0.0.1:${PORT}"
		return 0
	fi
	# Our own running service answers as agent-web and passes this check.
	check_port
	running=0
	systemctl --user is-active --quiet "${NAME}.service" && running=1

	mkdir -p "$(dirname "$unit")" "$(dirname "$envfile")"
	{
		echo "# Written by install.sh; rerunning it rewrites this file."
		echo "PATH=$(service_path)"
		[ -z "${LANG:-}" ] || echo "LANG=${LANG}"
	} >"$envfile"
	cat >"$unit" <<EOF
# Written by install.sh; rerunning it rewrites this file.
[Unit]
Description=agent-web (Claude Code / Codex web UI)
After=network.target

[Service]
Type=simple
EnvironmentFile=${envfile}
ExecStart=${BIN} --addr 127.0.0.1:${PORT}
Restart=on-failure
RestartSec=3
# SIGTERM to agent-web only: it closes the CLIs itself so they stay resumable.
KillMode=mixed
TimeoutStopSec=30

[Install]
WantedBy=default.target
EOF
	systemctl --user daemon-reload
	systemctl --user enable "${NAME}.service" >/dev/null 2>&1
	if [ $running = 1 ]; then
		info "Restarting the service (open sessions are resumed on your next message)"
		systemctl --user restart "${NAME}.service"
	else
		systemctl --user start "${NAME}.service"
	fi
	info "Service: ${unit}"

	# User services start at login unless lingering is on; with it they
	# start at boot.
	if [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)" != yes ]; then
		if loginctl enable-linger "$(id -un)" 2>/dev/null; then
			info "Enabled lingering: agent-web starts at boot, before you log in"
		elif command -v sudo >/dev/null 2>&1 && [ -r /dev/tty ]; then
			info "Starting at boot needs lingering for your user; sudo may ask for your password"
			# shellcheck disable=SC2024 # the redirect gives sudo a terminal for the password
			if sudo loginctl enable-linger "$(id -un)" </dev/tty; then
				info "Enabled lingering: agent-web starts at boot, before you log in"
			else
				warn "could not enable lingering; agent-web starts when you log in instead"
			fi
		else
			warn "could not enable lingering; agent-web starts when you log in. To start it at boot, run: sudo loginctl enable-linger $(id -un)"
		fi
	fi
	wait_healthy || true
}

linux_uninstall() {
	unit="$(systemd_dir)/${NAME}.service"
	if [ -f "$unit" ]; then
		if systemd_user_ok; then
			systemctl --user disable --now "${NAME}.service" >/dev/null 2>&1 || true
		fi
		rm -f "$unit"
		rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/${NAME}/service.env"
		systemd_user_ok && systemctl --user daemon-reload || true
		info "Removed the service"
	fi
	DATA_HINT="${XDG_CONFIG_HOME:-$HOME/.config}/${NAME}"
}

# --- macOS: launchd agent -------------------------------------------------------------

xml_escape() { printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }

darwin_plist() {
	log="$1"
	cat <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Written by install.sh; rerunning it rewrites this file. -->
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>${LAUNCHD_LABEL}</string>
	<key>ProgramArguments</key>
	<array>
		<string>$(xml_escape "$BIN")</string>
		<string>--addr</string>
		<string>127.0.0.1:${PORT}</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>$(xml_escape "$(service_path)")</string>
		<key>HOME</key>
		<string>$(xml_escape "$HOME")</string>
		<key>LANG</key>
		<string>$(xml_escape "${LANG:-en_US.UTF-8}")</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>5</integer>
	<key>ExitTimeOut</key>
	<integer>30</integer>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>$(xml_escape "$log")</string>
	<key>StandardErrorPath</key>
	<string>$(xml_escape "$log")</string>
</dict>
</plist>
EOF
}

darwin_service() {
	plist="$HOME/Library/LaunchAgents/${LAUNCHD_LABEL}.plist"
	log="$HOME/Library/Logs/${NAME}.log"
	LOG_HINT="tail -f '${log}'"
	domain="gui/$(id -u)"
	[ -n "$PORT" ] || PORT="$(previous_port "$plist")"
	[ -n "$PORT" ] || PORT=$DEFAULT_PORT
	running=0
	launchctl print "${domain}/${LAUNCHD_LABEL}" >/dev/null 2>&1 && running=1
	if [ $running = 1 ]; then
		launchctl bootout "${domain}/${LAUNCHD_LABEL}" 2>/dev/null || true
		# bootout returns before the job has exited.
		i=0
		while launchctl print "${domain}/${LAUNCHD_LABEL}" >/dev/null 2>&1 && [ $i -lt 40 ]; do
			sleep 0.5
			i=$((i + 1))
		done
	fi
	check_port

	mkdir -p "$(dirname "$plist")" "$(dirname "$log")"
	darwin_plist "$log" >"$plist"
	if ! launchctl bootstrap "$domain" "$plist" 2>/dev/null; then
		warn "could not load the launchd agent (no GUI login session?). It is installed and starts at your next login."
		say "  Or start it now: ${BIN} --addr 127.0.0.1:${PORT}"
		return 0
	fi
	launchctl enable "${domain}/${LAUNCHD_LABEL}" 2>/dev/null || true
	info "Service: ${plist} (starts when you log in)"
	wait_healthy || true
}

darwin_uninstall() {
	plist="$HOME/Library/LaunchAgents/${LAUNCHD_LABEL}.plist"
	if [ -f "$plist" ]; then
		launchctl bootout "gui/$(id -u)/${LAUNCHD_LABEL}" 2>/dev/null || true
		rm -f "$plist"
		info "Removed the service"
	fi
	DATA_HINT="$HOME/Library/Application Support/${NAME}"
}

# --- main ------------------------------------------------------------------------------

main() {
	VERSION="${AGENT_WEB_VERSION:-}"
	PORT="${AGENT_WEB_PORT:-}"
	BIN_DIR="${AGENT_WEB_BIN_DIR:-$HOME/.local/bin}"
	SERVICE=1
	UNINSTALL=0
	while [ $# -gt 0 ]; do
		case "$1" in
		--version)
			[ $# -ge 2 ] || die "--version needs a value"
			VERSION="$2"
			shift 2
			;;
		--port)
			[ $# -ge 2 ] || die "--port needs a value"
			PORT="$2"
			shift 2
			;;
		--bin-dir)
			[ $# -ge 2 ] || die "--bin-dir needs a value"
			BIN_DIR="$2"
			shift 2
			;;
		--no-service)
			SERVICE=0
			shift
			;;
		--uninstall)
			UNINSTALL=1
			shift
			;;
		-h | --help)
			usage
			exit 0
			;;
		*)
			usage
			die "unknown option: $1"
			;;
		esac
	done
	case "$PORT" in
	'' | [0-9] | [0-9][0-9] | [0-9][0-9][0-9] | [0-9][0-9][0-9][0-9] | [0-9][0-9][0-9][0-9][0-9]) ;;
	*) die "invalid port: $PORT" ;;
	esac
	[ "$(id -u)" != 0 ] || warn "running as root: agent-web will run sessions as root. Install it as your own user instead."
	[ -n "${HOME:-}" ] || die "HOME is not set"
	BIN="${BIN_DIR}/${NAME}"
	LOG_HINT=""
	DATA_HINT=""

	detect_platform

	if [ $UNINSTALL = 1 ]; then
		if [ "$OS" = linux ]; then linux_uninstall; else darwin_uninstall; fi
		if [ -f "$BIN" ]; then
			rm -f "$BIN"
			info "Removed ${BIN}"
		fi
		say "Your sessions are kept in ${DATA_HINT}; delete that directory to remove them too."
		exit 0
	fi

	resolve_version
	info "Installing agent-web ${VERSION} (${OS}/${ARCH})"
	install_binary

	command -v claude >/dev/null 2>&1 || command -v codex >/dev/null 2>&1 ||
		warn "neither claude nor codex is on your PATH; install and log in to Claude Code (or Codex) before using agent-web"
	case ":${PATH}:" in
	*":${BIN_DIR}:"*) ;;
	*) warn "${BIN_DIR} is not on your PATH; add it to run '${NAME}' by name" ;;
	esac

	if [ $SERVICE = 0 ]; then
		[ -n "$PORT" ] || PORT=$DEFAULT_PORT
		say "Start it with: ${BIN} --addr 127.0.0.1:${PORT}"
		exit 0
	fi
	if [ "$OS" = linux ]; then linux_service; else darwin_service; fi
}

main "$@"
