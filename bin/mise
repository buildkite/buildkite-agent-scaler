#!/usr/bin/env bash
set -eu

__mise_bootstrap() {
    local script_dir=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )
    local project_dir=$( cd -- "$( dirname -- "$script_dir" )" &> /dev/null && pwd )
    export MISE_BOOTSTRAP_PROJECT_DIR="$project_dir"
    local cache_home="${XDG_CACHE_HOME:-$HOME/.cache}/mise"
    export MISE_INSTALL_PATH="$cache_home/mise-2025.6.4"
    install() {
        #!/bin/sh
        set -eu

        #region logging setup
        if [ "${MISE_DEBUG-}" = "true" ] || [ "${MISE_DEBUG-}" = "1" ]; then
          debug() {
            echo "$@" >&2
          }
        else
          debug() {
            :
          }
        fi

        if [ "${MISE_QUIET-}" = "1" ] || [ "${MISE_QUIET-}" = "true" ]; then
          info() {
            :
          }
        else
          info() {
            echo "$@" >&2
          }
        fi

        error() {
          echo "$@" >&2
          exit 1
        }
        #endregion

        #region environment setup
        get_os() {
          os="$(uname -s)"
          if [ "$os" = Darwin ]; then
            echo "macos"
          elif [ "$os" = Linux ]; then
            echo "linux"
          else
            error "unsupported OS: $os"
          fi
        }

        get_arch() {
          musl=""
          if type ldd >/dev/null 2>/dev/null; then
            libc=$(ldd /bin/ls | grep 'musl' | head -1 | cut -d ' ' -f1)
            if [ -n "$libc" ]; then
              musl="-musl"
            fi
          fi
          arch="$(uname -m)"
          if [ "$arch" = x86_64 ]; then
            echo "x64$musl"
          elif [ "$arch" = aarch64 ] || [ "$arch" = arm64 ]; then
            echo "arm64$musl"
          elif [ "$arch" = armv7l ]; then
            echo "armv7$musl"
          else
            error "unsupported architecture: $arch"
          fi
        }

        get_ext() {
          if [ -n "${MISE_INSTALL_EXT:-}" ]; then
            echo "$MISE_INSTALL_EXT"
          elif [ -n "${MISE_VERSION:-}" ] && echo "$MISE_VERSION" | grep -q '^v2024'; then
            # 2024 versions don't have zstd tarballs
            echo "tar.gz"
          elif tar_supports_zstd; then
            echo "tar.zst"
          elif command -v zstd >/dev/null 2>&1; then
            echo "tar.zst"
          else
            echo "tar.gz"
          fi
        }

        tar_supports_zstd() {
          # tar is bsdtar or version is >= 1.31
          if tar --version | grep -q 'bsdtar' && command -v zstd >/dev/null 2>&1; then
            true
          elif tar --version | grep -q '1\.(3[1-9]|[4-9][0-9]'; then
            true
          else
            false
          fi
        }

        shasum_bin() {
          if command -v shasum >/dev/null 2>&1; then
            echo "shasum"
          elif command -v sha256sum >/dev/null 2>&1; then
            echo "sha256sum"
          else
            error "mise install requires shasum or sha256sum but neither is installed. Aborting."
          fi
        }

        get_checksum() {
          version=$1
          os="$(get_os)"
          arch="$(get_arch)"
          ext="$(get_ext)"
          url="https://github.com/jdx/mise/releases/download/v${version}/SHASUMS256.txt"

          # For current version use static checksum otherwise
          # use checksum from releases
          if [ "$version" = "v2025.6.4" ]; then
            checksum_linux_x86_64="609f8b24e2c5208124ceddfa79c9b428efc36af239ca79db5da140ee965a1834  ./mise-v2025.6.4-linux-x64.tar.gz"
            checksum_linux_x86_64_musl="50deeb8b60c9d725d10a53b2a5772b685bf401c3008791a168fcdb829363e67d  ./mise-v2025.6.4-linux-x64-musl.tar.gz"
            checksum_linux_arm64="57a45da192ce4ea92f8c06d1cdd37909363345de2767fd2283cdf059fd03c075  ./mise-v2025.6.4-linux-arm64.tar.gz"
            checksum_linux_arm64_musl="f04a435d01952b45520a5c0ed736d331b3db4c31da03d7d3cc7a215c064a5fad  ./mise-v2025.6.4-linux-arm64-musl.tar.gz"
            checksum_linux_armv7="fb5f4136ed7c426f95bd4d50c0cd4964f5b17f8f7cb9df72d7a79a978415f00c  ./mise-v2025.6.4-linux-armv7.tar.gz"
            checksum_linux_armv7_musl="c43c504ca4f486fa529d9b1ee1c0aab2c8308ef07b8569e7f25e0152c256606e  ./mise-v2025.6.4-linux-armv7-musl.tar.gz"
            checksum_macos_x86_64="067769fdec24631c5e586eb8cbd607da7e47fee4dc3a8a4230d15ebc50f0865d  ./mise-v2025.6.4-macos-x64.tar.gz"
            checksum_macos_arm64="705b18200adf923c7faae022f35abd0b3e0b1daa271957492168e931ba7e1ed7  ./mise-v2025.6.4-macos-arm64.tar.gz"
            checksum_linux_x86_64_zstd="98d69e44f811a9611fcb55b90076a40c146c87319b02a0e6deda33df3ed66984  ./mise-v2025.6.4-linux-x64.tar.zst"
            checksum_linux_x86_64_musl_zstd="60dc830b16fa32efbaee7b0a0340942f7b4a7475c57db2d79f7f7400abe8563b  ./mise-v2025.6.4-linux-x64-musl.tar.zst"
            checksum_linux_arm64_zstd="29efa155f2cbe70adb9e251caf149b5ca244896be00fde9f13ab535d1430b4f5  ./mise-v2025.6.4-linux-arm64.tar.zst"
            checksum_linux_arm64_musl_zstd="ece6af08308802943d2dd3c6650b96cf32c64ab4ff00ed4f26a5dd4d62eed867  ./mise-v2025.6.4-linux-arm64-musl.tar.zst"
            checksum_linux_armv7_zstd="f5c118178964e680114a8a3e8b262d696ee96a6eb934781483fdd1f15b58e545  ./mise-v2025.6.4-linux-armv7.tar.zst"
            checksum_linux_armv7_musl_zstd="b802df03baf50130c40bc47e43943a79e8879cc8833c58fa7b9ab1fc1fbf6a0e  ./mise-v2025.6.4-linux-armv7-musl.tar.zst"
            checksum_macos_x86_64_zstd="36b2937732da87611d5aa1fb12b6b9b2f64daf8e90dcbc69b027be6c75b21193  ./mise-v2025.6.4-macos-x64.tar.zst"
            checksum_macos_arm64_zstd="5bcd0a3aec2576402e5aa79cc3c95afe72500d23455022d1bdb1d7f8846c2abe  ./mise-v2025.6.4-macos-arm64.tar.zst"

            # TODO: refactor this, it's a bit messy
            if [ "$(get_ext)" = "tar.zst" ]; then
              if [ "$os" = "linux" ]; then
                if [ "$arch" = "x64" ]; then
                  echo "$checksum_linux_x86_64_zstd"
                elif [ "$arch" = "x64-musl" ]; then
                  echo "$checksum_linux_x86_64_musl_zstd"
                elif [ "$arch" = "arm64" ]; then
                  echo "$checksum_linux_arm64_zstd"
                elif [ "$arch" = "arm64-musl" ]; then
                  echo "$checksum_linux_arm64_musl_zstd"
                elif [ "$arch" = "armv7" ]; then
                  echo "$checksum_linux_armv7_zstd"
                elif [ "$arch" = "armv7-musl" ]; then
                  echo "$checksum_linux_armv7_musl_zstd"
                else
                  warn "no checksum for $os-$arch"
                fi
              elif [ "$os" = "macos" ]; then
                if [ "$arch" = "x64" ]; then
                  echo "$checksum_macos_x86_64_zstd"
                elif [ "$arch" = "arm64" ]; then
                  echo "$checksum_macos_arm64_zstd"
                else
                  warn "no checksum for $os-$arch"
                fi
              else
                warn "no checksum for $os-$arch"
              fi
            else
              if [ "$os" = "linux" ]; then
                if [ "$arch" = "x64" ]; then
                  echo "$checksum_linux_x86_64"
                elif [ "$arch" = "x64-musl" ]; then
                  echo "$checksum_linux_x86_64_musl"
                elif [ "$arch" = "arm64" ]; then
                  echo "$checksum_linux_arm64"
                elif [ "$arch" = "arm64-musl" ]; then
                  echo "$checksum_linux_arm64_musl"
                elif [ "$arch" = "armv7" ]; then
                  echo "$checksum_linux_armv7"
                elif [ "$arch" = "armv7-musl" ]; then
                  echo "$checksum_linux_armv7_musl"
                else
                  warn "no checksum for $os-$arch"
                fi
              elif [ "$os" = "macos" ]; then
                if [ "$arch" = "x64" ]; then
                  echo "$checksum_macos_x86_64"
                elif [ "$arch" = "arm64" ]; then
                  echo "$checksum_macos_arm64"
                else
                  warn "no checksum for $os-$arch"
                fi
              else
                warn "no checksum for $os-$arch"
              fi
            fi
          else
            if command -v curl >/dev/null 2>&1; then
              debug ">" curl -fsSL "$url"
              checksums="$(curl --compressed -fsSL "$url")"
            else
              if command -v wget >/dev/null 2>&1; then
                debug ">" wget -qO - "$url"
                stderr=$(mktemp)
                checksums="$(wget -qO - "$url")"
              else
                error "mise standalone install specific version requires curl or wget but neither is installed. Aborting."
              fi
            fi
            # TODO: verify with minisign or gpg if available

            checksum="$(echo "$checksums" | grep "$os-$arch.$ext")"
            if ! echo "$checksum" | grep -Eq "^([0-9a-f]{32}|[0-9a-f]{64})"; then
              warn "no checksum for mise $version and $os-$arch"
            else
              echo "$checksum"
            fi
          fi
        }

        #endregion

        download_file() {
          url="$1"
          filename="$(basename "$url")"
          cache_dir="$(mktemp -d)"
          file="$cache_dir/$filename"

          info "mise: installing mise..."

          if command -v curl >/dev/null 2>&1; then
            debug ">" curl -#fLo "$file" "$url"
            curl -#fLo "$file" "$url"
          else
            if command -v wget >/dev/null 2>&1; then
              debug ">" wget -qO "$file" "$url"
              stderr=$(mktemp)
              wget -O "$file" "$url" >"$stderr" 2>&1 || error "wget failed: $(cat "$stderr")"
            else
              error "mise standalone install requires curl or wget but neither is installed. Aborting."
            fi
          fi

          echo "$file"
        }

        install_mise() {
          version="${MISE_VERSION:-v2025.6.4}"
          version="${version#v}"
          os="$(get_os)"
          arch="$(get_arch)"
          ext="$(get_ext)"
          install_path="${MISE_INSTALL_PATH:-$HOME/.local/bin/mise}"
          install_dir="$(dirname "$install_path")"
          tarball_url="https://github.com/jdx/mise/releases/download/v${version}/mise-v${version}-${os}-${arch}.${ext}"

          cache_file=$(download_file "$tarball_url")
          debug "mise-setup: tarball=$cache_file"

          debug "validating checksum"
          cd "$(dirname "$cache_file")" && get_checksum "$version" | "$(shasum_bin)" -c >/dev/null

          # extract tarball
          mkdir -p "$install_dir"
          rm -rf "$install_path"
          cd "$(mktemp -d)"
          if [ "$(get_ext)" = "tar.zst" ] && ! tar_supports_zstd; then
            zstd -d -c "$cache_file" | tar -xf -
          else
            tar -xf "$cache_file"
          fi
          mv mise/bin/mise "$install_path"
          info "mise: installed successfully to $install_path"
        }

        after_finish_help() {
          case "${SHELL:-}" in
          */zsh)
            info "mise: run the following to activate mise in your shell:"
            info "echo \"eval \\\"\\\$($install_path activate zsh)\\\"\" >> \"${ZDOTDIR-$HOME}/.zshrc\""
            info ""
            info "mise: run \`mise doctor\` to verify this is setup correctly"
            ;;
          */bash)
            info "mise: run the following to activate mise in your shell:"
            info "echo \"eval \\\"\\\$($install_path activate bash)\\\"\" >> ~/.bashrc"
            info ""
            info "mise: run \`mise doctor\` to verify this is setup correctly"
            ;;
          */fish)
            info "mise: run the following to activate mise in your shell:"
            info "echo \"$install_path activate fish | source\" >> ~/.config/fish/config.fish"
            info ""
            info "mise: run \`mise doctor\` to verify this is setup correctly"
            ;;
          *)
            info "mise: run \`$install_path --help\` to get started"
            ;;
          esac
        }

        install_mise
        if [ "${MISE_INSTALL_HELP-}" != 0 ]; then
          after_finish_help
        fi

        cd "$MISE_BOOTSTRAP_PROJECT_DIR"
    }
    local MISE_INSTALL_HELP=0
    test -f "$MISE_INSTALL_PATH" || install
}
__mise_bootstrap
exec "$MISE_INSTALL_PATH" "$@"
