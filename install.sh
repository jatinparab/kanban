#!/bin/sh
set -eu

REPO="jatinparab/kanban"
VERSION="latest"
INSTALL_DIR="${KANBAN_INSTALL_DIR:-$HOME/.local/bin}"
INSTALL_SKILL=1
SKILL_ONLY=0
TMP_DIR=""

if [ -t 1 ] && [ "${NO_COLOR:-}" = "" ]; then
  BOLD='\033[1m'; CYAN='\033[36m'; GREEN='\033[32m'; YELLOW='\033[33m'; RESET='\033[0m'
else
  BOLD=''; CYAN=''; GREEN=''; YELLOW=''; RESET=''
fi

say()  { printf "%b\n" "${CYAN}›${RESET} $*"; }
ok()   { printf "%b\n" "${GREEN}✓${RESET} $*"; }
warn() { printf "%b\n" "${YELLOW}!${RESET} $*"; }
die()  { printf "%s\n" "Error: $*" >&2; exit 1; }

usage() {
  cat <<EOF
Install kanban — durable Markdown task boards for coding agents.

Usage: ./install.sh [OPTIONS]

  --version VERSION   Install a release tag (default: latest)
  --bin-dir DIRECTORY Install the binary here (default: ~/.local/bin)
  --no-skill          Install only the CLI
  --skill-only        Install only the agent skill through skills.sh
  -h, --help          Show this help

Environment: KANBAN_INSTALL_DIR, NO_COLOR
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) [ "$#" -ge 2 ] || die "--version requires a value"; VERSION=$2; shift 2 ;;
    --bin-dir) [ "$#" -ge 2 ] || die "--bin-dir requires a value"; INSTALL_DIR=$2; shift 2 ;;
    --no-skill) INSTALL_SKILL=0; shift ;;
    --skill-only) SKILL_ONLY=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1 (try --help)" ;;
  esac
done

cleanup() { [ -z "$TMP_DIR" ] || rm -rf "$TMP_DIR"; }
trap cleanup EXIT HUP INT TERM

has_tty() { (: </dev/tty) 2>/dev/null; }

download() {
  url=$1; output=$2
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 --retry-delay 1 "$url" -o "$output"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$url" -O "$output"
  else
    die "curl or wget is required"
  fi
}

printf "%b\n" "${BOLD}${CYAN}┌─────────────────────────────────────────┐${RESET}"
printf "%b\n" "${BOLD}${CYAN}│  kanban  ·  durable plans for agents   │${RESET}"
printf "%b\n" "${BOLD}${CYAN}└─────────────────────────────────────────┘${RESET}"

if [ "$SKILL_ONLY" -eq 0 ]; then
  case "$(uname -s)" in
    Linux) OS=linux ;;
    Darwin) OS=darwin ;;
    *) die "unsupported operating system: $(uname -s)" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    arm64|aarch64) ARCH=arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac

  ARCHIVE="kanban_${OS}_${ARCH}.tar.gz"
  if [ "$VERSION" = "latest" ]; then
    RELEASE_URL="https://github.com/${REPO}/releases/latest/download"
    LABEL="latest release"
  else
    case "$VERSION" in v*) : ;; *) VERSION="v${VERSION}" ;; esac
    RELEASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
    LABEL="$VERSION"
  fi

  TMP_DIR=$(mktemp -d 2>/dev/null || mktemp -d -t kanban)
  say "Fetching ${LABEL} for ${OS}/${ARCH}"
  download "${RELEASE_URL}/${ARCHIVE}" "$TMP_DIR/$ARCHIVE"
  download "${RELEASE_URL}/checksums.txt" "$TMP_DIR/checksums.txt"

  EXPECTED=$(awk -v file="$ARCHIVE" '$2 == file { print $1; exit }' "$TMP_DIR/checksums.txt")
  [ -n "$EXPECTED" ] || die "release checksum for $ARCHIVE was not found"
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL=$(sha256sum "$TMP_DIR/$ARCHIVE" | awk '{print $1}')
  elif command -v shasum >/dev/null 2>&1; then
    ACTUAL=$(shasum -a 256 "$TMP_DIR/$ARCHIVE" | awk '{print $1}')
  else
    die "sha256sum or shasum is required to verify the download"
  fi
  [ "$EXPECTED" = "$ACTUAL" ] || die "checksum verification failed"
  ok "Verified release checksum"

  tar -xzf "$TMP_DIR/$ARCHIVE" -C "$TMP_DIR"
  [ -f "$TMP_DIR/kanban" ] || die "release archive did not contain kanban"
  mkdir -p "$INSTALL_DIR"
  install -m 0755 "$TMP_DIR/kanban" "$INSTALL_DIR/kanban"
  ok "Installed CLI to $INSTALL_DIR/kanban"

  case ":$PATH:" in
    *":$INSTALL_DIR:"*) : ;;
    *) warn "$INSTALL_DIR is not on PATH; add it to your shell profile" ;;
  esac
fi

if [ "$INSTALL_SKILL" -eq 1 ]; then
  if ! command -v npx >/dev/null 2>&1; then
    warn "Node.js/npx was not found, so the agent skill was not installed"
    printf "%s\n" "  Later, run: npx skills add $REPO --skill kanban -g"
  elif ! has_tty; then
    warn "No interactive terminal is available, so skill installation was skipped"
    printf "%s\n" "  Later, run: npx skills add $REPO --skill kanban -g"
  else
    say "Choose which coding agents should learn kanban"
    if npx --yes skills add "$REPO" --skill kanban -g </dev/tty; then
      ok "Agent skill installation finished via skills.sh"
    else
      warn "skills.sh could not install the skill to every selected agent"
      printf "%s\n" "  Retry with: npx skills add $REPO --skill kanban -g"
    fi
  fi
fi

printf "\n%b\n" "${BOLD}${GREEN}Kanban is ready.${RESET} Start with: kanban init \"My project\""
