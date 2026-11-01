#!/bin/sh
# ============================================================================
#  EVOLV installer — the workspace that rewrites itself
#  curl -fsSL https://raw.githubusercontent.com/dxn111/EVOLV/main/install.sh | sh
# ============================================================================
set -eu

REPO="dxn111/EVOLV"
BIN_NAME="evolv"
INSTALL_DIR_LOCAL="$HOME/.local/bin"
VERSION="${EVOLV_VERSION:-latest}"

# ── pretty printing ─────────────────────────────────────────────────────────
if [ -t 1 ] && [ -n "${TERM:-}" ] && [ "${TERM}" != "dumb" ]; then
  BOLD=$'\033[1m'; DIM=$'\033[2m'; RESET=$'\033[0m'
  VIOLET=$'\033[38;5;141m'; CYAN=$'\033[38;5;80m'; PINK=$'\033[38;5;213m'
  GREEN=$'\033[38;5;114m'; YELLOW=$'\033[38;5;221m'; RED=$'\033[38;5;203m'
else
  BOLD=""; DIM=""; RESET=""; VIOLET=""; CYAN=""; PINK=""; GREEN=""; YELLOW=""; RED=""
fi

say()  { printf "%s\n" "$*"; }
ok()   { printf "  %s✔%s %s\n" "$GREEN" "$RESET" "$*"; }
warn() { printf "  %s!%s %s\n" "$YELLOW" "$RESET" "$*"; }
die()  { printf "  %s✘%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }

# ── banner ──────────────────────────────────────────────────────────────────
banner() {
  printf "%s" "
${VIOLET}  ███████╗██╗   ██╗ ██████╗ ██╗    ██╗   ██╗${RESET}
${VIOLET}  ██╔════╝██║   ██║██╔═══██╗██║    ██║   ██║${RESET}
${CYAN}  █████╗  ██║   ██║██║   ██║██║    ██║   ██║${RESET}
${CYAN}  ██╔══╝  ╚██╗ ██╔╝██║   ██║██║    ██║   ██║${RESET}
${PINK}  ███████╗ ╚████╔╝ ╚██████╔╝███████╗╚██████╔╝${RESET}
${PINK}  ╚══════╝  ╚═══╝   ╚═════╝ ╚══════╝ ╚═════╝${RESET}
  ${DIM}the workspace that rewrites itself · github.com/${REPO}${RESET}

"
}

# ── spinner (works without ncurses) ─────────────────────────────────────────
SPIN_PID=""
spin() {
  # $1 = label
  [ -t 1 ] || { printf "  … %s\n" "$1"; return 0; }
  (
    i=0
    frames="⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
    while kill -0 "$SPIN_PID" 2>/dev/null; do
      i=$(( (i + 1) % 10 ))
      printf "\r  ${CYAN}%c${RESET} %s" "$(printf '%s' "$frames" | cut -c $((i+1)) 2>/dev/null || echo '*')" "$1"
      sleep 0.1
    done
  ) &
  SPINNER=$!
}

end_spin() {
  [ -n "${SPINNER:-}" ] && kill "$SPINNER" 2>/dev/null || true
  wait "${SPINNER:-0}" 2>/dev/null || true
  SPINNER=""
  [ -t 1 ] && printf "\r\033[K" || true
}

# ── detect platform ─────────────────────────────────────────────────────────
detect_platform() {
  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64|amd64)  ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    armv7*|armv6*) ARCH="arm" ;;
    *) die "unsupported architecture: $ARCH" ;;
  esac
  case "$OS" in
    linux|darwin) ;;
    mingw*|msys*|cygwin*) OS="windows" ;;
    *) die "unsupported OS: $OS (windows: use 'go install github.com/dxn111/EVOLV/evolv@latest' or WSL)" ;;
  esac
}

# ── fetch with progress ─────────────────────────────────────────────────────
fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    die "need curl or wget to install"
  fi
}

# ── resolve latest release tag ──────────────────────────────────────────────
latest_tag() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest" 2>/dev/null | sed 's#.*/tag/##'
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- --max-redirect=0 "https://github.com/${REPO}/releases/latest" 2>/dev/null | sed -n 's#.*/tag/##p' | head -1
  fi
}

# ── install paths ───────────────────────────────────────────────────────────
choose_install_dir() {
  # prefer ~/.local/bin (no sudo), fall back to /usr/local/bin, then ./bin
  if mkdir -p "$INSTALL_DIR_LOCAL" 2>/dev/null && [ -w "$INSTALL_DIR_LOCAL" ]; then
    INSTALL_DIR="$INSTALL_DIR_LOCAL"
  elif [ -w /usr/local/bin ] || [ "$(id -u)" = "0" ]; then
    INSTALL_DIR="/usr/local/bin"
  else
    warn "cannot write to $INSTALL_DIR_LOCAL or /usr/local/bin"
    INSTALL_DIR="$PWD/bin"
    mkdir -p "$INSTALL_DIR"
  fi
}

ensure_path() {
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) return 0 ;;
  esac
  # try to add to shell profile
  ADDED=0
  for f in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile"; do
    if [ -f "$f" ] && [ -w "$f" ]; then
      printf '\n# evolv (self-evolving AI workspace)\nexport PATH="%s:$PATH"\n' "$INSTALL_DIR" >> "$f"
      ADDED=1
      break
    fi
  done
  if [ "$ADDED" = "1" ]; then
    ok "added ${INSTALL_DIR} to your PATH (restart shell or: export PATH=\"${INSTALL_DIR}:\$PATH\")"
  else
    warn "add this to your shell rc:  export PATH=\"${INSTALL_DIR}:\$PATH\""
  fi
}

# ── main ────────────────────────────────────────────────────────────────────
# ── flags ───────────────────────────────────────────────────────────────────
# --version / -v : print what the installer is and what it would install
show_version() {
  say "evolv installer v2 (docs: https://github.com/${REPO}/tree/main/docs)"
  TAG=$(latest_tag || true)
  if [ -n "$TAG" ]; then
    say "latest release: ${TAG}"
  else
    say "latest release: could not reach github.com (offline?) — install will build from source"
  fi
  say "EVOLV_VERSION env pin: ${VERSION}"
}

for arg in "$@"; do
  case "$arg" in
    --version|-v) show_version; exit 0 ;;
    --help|-h)
      say "usage: install.sh [--version] [--help]"
      say "  EVOLV_VERSION=v0.21.0 install.sh   pin a release instead of latest"
      exit 0 ;;
    EVOLV_VERSION=*) VERSION="${arg#EVOLV_VERSION=}" ;;
  esac
done

main() {
  banner
  detect_platform
  ok "detected ${OS}/${ARCH}"

  TARGET="${BIN_NAME}_${OS}_${ARCH}"
  [ "$OS" = "windows" ] && TARGET="${TARGET}.exe"
  ARCHIVE="${BIN_NAME}_${VERSION#v}_${OS}_${ARCH}.tar.gz"
  [ "$VERSION" = "latest" ] && ARCHIVE="${BIN_NAME}_${OS}_${ARCH}.tar.gz"

  # 1) try prebuilt release binary
  TAG=""
  spin "resolving latest release"
  TAG=$(latest_tag || true)
  end_spin
  if [ -n "$TAG" ] && [ "$TAG" != "https://github.com/${REPO}/releases" ]; then
    VERSION_TAG="$TAG"
    ARCHIVE="evolv_${VERSION_TAG#v}_${OS}_${ARCH}.tar.gz"
    URL="https://github.com/${REPO}/releases/download/${VERSION_TAG}/${ARCHIVE}"
    spin "downloading EVOLV ${VERSION_TAG} (${OS}/${ARCH})"
    if fetch "$URL" "/tmp/evolv.tar.gz" 2>/dev/null; then
      end_spin; ok "downloaded release ${VERSION_TAG}"
      HAVE_RELEASE=1
    else
      end_spin; warn "no prebuilt ${ARCHIVE} — falling back to source build"
      HAVE_RELEASE=0
    fi
  else
    warn "no releases published yet — building from source"
    HAVE_RELEASE=0
  fi

  if [ "${HAVE_RELEASE:-0}" = "1" ]; then
    tar -xzf /tmp/evolv.tar.gz -C /tmp
    choose_install_dir
    spin "installing ${BIN_NAME} → ${INSTALL_DIR}"
    mv "/tmp/${BIN_NAME}" "${INSTALL_DIR}/${BIN_NAME}" 2>/dev/null || cp "/tmp/${BIN_NAME}" "${INSTALL_DIR}/${BIN_NAME}"
    chmod +x "${INSTALL_DIR}/${BIN_NAME}"
    end_spin
  else
    # 2) fallback: build from source with Go
    command -v git >/dev/null 2>&1 || die "git required to build from source"
    if ! command -v go >/dev/null 2>&1; then
      warn "Go toolchain not found — installing Go 1.24 (this needs ~250MB)"
      GO_VER="1.24.5"
      case "$OS-$ARCH" in
        linux-amd64)  GO_TGZ="go${GO_VER}.linux-amd64.tar.gz" ;;
        linux-arm64)  GO_TGZ="go${GO_VER}.linux-arm64.tar.gz" ;;
        darwin-amd64) GO_TGZ="go${GO_VER}.darwin-amd64.tar.gz" ;;
        darwin-arm64) GO_TGZ="go${GO_VER}.darwin-arm64.tar.gz" ;;
        *) die "cannot auto-install Go for ${OS}-${ARCH}; install from https://go.dev/dl and re-run" ;;
      esac
      fetch "https://go.dev/dl/${GO_TGZ}" /tmp/go.tgz || die "Go download failed"
      [ -d "$HOME/sdk/go" ] || mkdir -p "$HOME/sdk"
      tar -C "$HOME/sdk" -xzf /tmp/go.tgz
      export PATH="$HOME/sdk/go/bin:$PATH"
      ok "Go $(go version | sed 's/go version //') installed"
    fi
    choose_install_dir
    spin "cloning + building EVOLV from source"
    rm -rf /tmp/evolv-src && git clone --depth 1 "https://github.com/${REPO}.git" /tmp/evolv-src >/dev/null 2>&1
    ( cd /tmp/evolv-src && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "${INSTALL_DIR}/${BIN_NAME}" ./cmd/evolv )
    end_spin
  fi

  chmod +x "${INSTALL_DIR}/${BIN_NAME}" 2>/dev/null || true
  ok "installed ${VIOLET}${BOLD}${INSTALL_DIR}/${BIN_NAME}${RESET}"

  ensure_path

  # final card
  printf "%s" "
  ${BOLD}┌──────────────────────────────────────────────────────────┐${RESET}
  ${BOLD}│${RESET}  ${GREEN}✔${RESET} EVOLV is installed.                                    ${BOLD}│${RESET}
  ${BOLD}│${RESET}                                                          ${BOLD}│${RESET}
  ${BOLD}│${RESET}    ${VIOLET}${BOLD}\$ ${CYAN}evolv${RESET}                                              ${BOLD}│${RESET}
  ${BOLD}│${RESET}                                                          ${BOLD}│${RESET}
  ${BOLD}│${RESET}  Boots the organism on localhost and opens your browser. ${BOLD}│${RESET}
  ${BOLD}│${RESET}  Free models included — ${GREEN}no API key, no signup${RESET} needed.      ${BOLD}│${RESET}
  ${BOLD}│${RESET}                                                          ${BOLD}│${RESET}
  ${BOLD}│${RESET}  ${DIM}flags: --port N · --no-open · --reset · --version${RESET}        ${BOLD}│${RESET}
  ${BOLD}└──────────────────────────────────────────────────────────┘${RESET}

  ${DIM}support: dxn1.0day@gmail.com · discord @dxn1_ai${RESET}
"
}

main "$@"
